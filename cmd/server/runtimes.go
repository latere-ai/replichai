// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	xotel "latere.ai/x/pkg/otel"

	managed "latere.ai/x/managed-agents"

	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/runtimes"
	"github.com/latere-ai/replichai/internal/runtimes/builtin"
	"github.com/latere-ai/replichai/internal/signin"
)

// labels are the names a person reads for each runtime.
var labels = map[managed.RuntimeName]string{
	builtin.AnthropicManaged: "Anthropic",
	builtin.Latere:           "Latere",
	"memory":                 "Memory",
}

// personPaid are the runtimes a person pays for from their own Latere wallet
// (048): the server acts as them, and the project's pot is never asked.
var personPaid = []managed.RuntimeName{builtin.Latere}

// offeredRuntimes reads REPLICHAI_RUNTIMES, a comma list of the runtimes a
// person may choose, and puts the selected default among them. An empty list
// offers the default alone. The local runtime runs on the operator's own
// machine and is never offered by a server.
func offeredRuntimes(catalog *runtimes.Catalog, raw string, selected managed.RuntimeName) ([]managed.RuntimeName, error) {
	offered := []managed.RuntimeName{selected}
	for part := range strings.SplitSeq(raw, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		name, err := catalog.Canonical(part)
		if err != nil {
			return nil, fmt.Errorf("REPLICHAI_RUNTIMES: %w", err)
		}
		if name == "local" {
			return nil, errors.New("REPLICHAI_RUNTIMES: the local runtime runs on a person's own machine and is not offered by a server")
		}
		if !slices.Contains(offered, name) {
			offered = append(offered, name)
		}
	}
	return offered, nil
}

// personSignIns builds the store of kept Latere sign-ins and the minter that
// acts from them, when a runtime the person pays is offered, and nothing
// otherwise. A server that offers one without a key to seal sign-ins with,
// or without its own sign-in client's secret, refuses to start: it could not
// act for anyone.
func personSignIns(st stores, offered []managed.RuntimeName, actsAsOperator bool) (signin.Store, *signin.Minter, error) {
	if actsAsOperator || !slices.ContainsFunc(offered, func(name managed.RuntimeName) bool { return slices.Contains(personPaid, name) }) {
		return nil, nil, nil
	}
	sealer, err := signin.NewSealer(os.Getenv("REPLICHAI_SIGNIN_KEY"))
	if err != nil {
		return nil, nil, fmt.Errorf("a runtime the person pays is offered, so REPLICHAI_SIGNIN_KEY must seal their sign-ins: %w", err)
	}
	if os.Getenv("AUTH_CLIENT_SECRET") == "" {
		return nil, nil, errors.New("a runtime the person pays is offered, so AUTH_CLIENT_SECRET must let the server refresh their sign-ins")
	}
	var store signin.Store = signin.NewMemStore(sealer)
	if st.pool != nil {
		store = signin.NewPGStore(st.pool, sealer)
	}
	minter := signin.NewMinter(store, signin.Config{
		Issuer:       envOr("AUTH_ISSUER", "https://auth.latere.ai"),
		ClientID:     os.Getenv("AUTH_CLIENT_ID"),
		ClientSecret: os.Getenv("AUTH_CLIENT_SECRET"),
		HTTP:         &http.Client{Timeout: 30 * time.Second, Transport: xotel.Transport(nil)},
	})
	return store, minter, nil
}

// devLatereCommand is the operator's own Latere token command, for a local
// server under the loopback dev bypass only: there every request is the
// operator, so a session on the Latere platform acts as them without a kept
// sign-in. Outside dev it is ignored, so a server never acts for everyone
// with one person's credential.
func devLatereCommand(dev bool, command string) string {
	if !dev {
		return ""
	}
	return command
}

// signInOf is the runner's source of a person's bearer, nil without kept
// sign-ins.
func signInOf(minter *signin.Minter) func(string) func(context.Context) (string, error) {
	if minter == nil {
		return nil
	}
	return minter.TokenFunc
}

// loginScopes asks for a refresh token only where the server keeps sign-ins.
func loginScopes(keepSignIns bool) []string {
	scopes := []string{"openid", "email", "profile"}
	if keepSignIns {
		scopes = append(scopes, "offline_access")
	}
	return scopes
}

// runtimeOffers describes each offered runtime for the session start and
// GET /models. A runtime the person pays reads its Effort levels and models
// as the person asking; any other prices its levels from the rate card.
func runtimeOffers(offered []managed.RuntimeName, selected managed.RuntimeName, built map[managed.RuntimeName]managed.Runtime, minter *signin.Minter) []httpapi.RuntimeOffer {
	offers := make([]httpapi.RuntimeOffer, 0, len(offered))
	for _, name := range offered {
		label := labels[name]
		if label == "" {
			label = string(name)
		}
		offer := httpapi.RuntimeOffer{Name: string(name), Label: label, Default: name == selected}
		if slices.Contains(personPaid, name) {
			runtime := built[name]
			// asPerson acts as the person asking through their kept
			// sign-in; with none kept, as under the dev bypass, the runtime
			// acts with its own configured source.
			asPerson := func(ctx context.Context, principal string) context.Context {
				if minter == nil {
					return ctx
				}
				return builtin.WithLatereSignIn(ctx, minter.TokenFunc(principal))
			}
			offer.PersonPays = true
			offer.Efforts = func(ctx context.Context, principal string) ([]httpapi.EffortOffer, error) {
				efforts, err := builtin.LatereEfforts(asPerson(ctx, principal), runtime)
				if err != nil {
					return nil, err
				}
				out := make([]httpapi.EffortOffer, 0, len(efforts))
				for _, e := range efforts {
					offer := httpapi.EffortOffer{Tier: e.Preference, Effort: e.Level, Model: e.Model.ID, Provider: e.Model.Provider, Input: e.Input, Output: e.Output}
					if !e.Available {
						offer.Err = "level_unavailable"
					}
					out = append(out, offer)
				}
				return out, nil
			}
			offer.Models = func(ctx context.Context, principal string) ([]string, error) {
				models, err := runtime.Models(asPerson(ctx, principal))
				if err != nil {
					return nil, err
				}
				names := make([]string, 0, len(models))
				for _, model := range models {
					if len(model.Preferences) == 0 {
						names = append(names, model.Ref.Provider+"/"+model.Ref.ID)
					}
				}
				return names, nil
			}
		}
		offers = append(offers, offer)
	}
	return offers
}
