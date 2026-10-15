// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"slices"
	"strings"

	"github.com/latere-ai/replichai/internal/credit"
)

// RuntimeOffer is one runtime a person may start a session on in this
// deployment (048). The deployment offers only the runtimes it configured.
type RuntimeOffer struct {
	// Name is the runtime's canonical name, what a session records.
	Name string
	// Label is what a person reads: Anthropic, Latere.
	Label string
	// Default marks the runtime a session that names none runs on.
	Default bool
	// PersonPays says the person's own Latere wallet pays for a session on
	// it, so the project's pot is neither held nor charged.
	PersonPays bool
	// Efforts reports the three Effort levels for the person asking: the
	// model each would run and its price. Nil reports the deployment's rate
	// card instead, as a project-paid runtime does.
	Efforts func(ctx context.Context, principal string) ([]EffortOffer, error)
	// Models lists the exact models the person asking may choose, as
	// provider/id. Nil offers no exact model, only the Effort levels.
	Models func(ctx context.Context, principal string) ([]string, error)
}

// EffortOffer is one Effort level as a runtime would run it now. Rates are
// micro-USD per million tokens and zero when unknown or free.
type EffortOffer struct {
	Tier     string `json:"tier"`
	Effort   string `json:"effort"`
	Model    string `json:"model,omitempty"`
	Provider string `json:"provider,omitempty"`
	Input    int64  `json:"input,omitempty"`
	Output   int64  `json:"output,omitempty"`
	Err      string `json:"error,omitempty"`
}

// runtimeView is a runtime on the wire.
type runtimeView struct {
	Name    string        `json:"name"`
	Label   string        `json:"label"`
	Default bool          `json:"default"`
	Payer   string        `json:"payer"`
	Efforts []EffortOffer `json:"efforts"`
	Models  []string      `json:"models,omitempty"`
	Err     string        `json:"error,omitempty"`
}

// offer finds the runtime a session start names, or the default when it
// names none. False when the name is not offered.
func (rt *Router) offer(name string) (RuntimeOffer, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, offer := range rt.runtimes {
		if (name == "" && offer.Default) || offer.Name == name {
			return offer, true
		}
	}
	return RuntimeOffer{}, false
}

// runtimeViews reports every offered runtime for the person asking. A
// runtime whose levels or models cannot be read is still listed, with the
// reason, so the choice is offered even when its prices are not.
func (rt *Router) runtimeViews(ctx context.Context, principal string) []runtimeView {
	views := make([]runtimeView, 0, len(rt.runtimes))
	for _, offer := range rt.runtimes {
		view := runtimeView{Name: offer.Name, Label: offer.Label, Default: offer.Default, Payer: "project"}
		if offer.PersonPays {
			view.Payer = "person"
		}
		if offer.Efforts != nil {
			efforts, err := offer.Efforts(ctx, principal)
			if err != nil {
				view.Err = "runtime_unavailable"
			}
			view.Efforts = efforts
		} else {
			for _, tier := range rt.tiers(ctx) {
				view.Efforts = append(view.Efforts, effortFromTier(tier))
			}
		}
		if offer.Models != nil && view.Err == "" {
			models, err := offer.Models(ctx, principal)
			if err != nil {
				view.Err = "runtime_unavailable"
			}
			view.Models = models
		}
		views = append(views, view)
	}
	return views
}

func effortFromTier(tier tierPrice) EffortOffer {
	preference, _ := credit.ParsePreference(tier.Tier)
	return EffortOffer{Tier: tier.Tier, Effort: preference.Effort(), Model: tier.Model, Provider: tier.Provider,
		Input: tier.Input, Output: tier.Output, Err: tier.Err}
}

// chosenModel checks an exact model a session start names against what the
// runtime offers the person. It answers the error code to report, empty when
// the model may be used.
func (rt *Router) chosenModel(ctx context.Context, offer RuntimeOffer, principal, model string) string {
	if model == "" {
		return ""
	}
	if offer.Models == nil {
		return "bad_model"
	}
	models, err := offer.Models(ctx, principal)
	if err != nil {
		return "models_unavailable"
	}
	if !slices.Contains(models, model) {
		return "bad_model"
	}
	return ""
}
