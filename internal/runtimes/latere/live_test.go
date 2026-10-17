// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package latere

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/audit"
	"github.com/latere-ai/replichai/internal/cloud"
	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/signin"
)

// TestRealLatereEndToEnd runs one small graded session on the live Latere
// platform, as the person signed in with `replichai login`, or the one whose
// credential LATERE_API_KEY or LATERE_TOKEN_COMMAND names when either is set,
// and charges their wallet a few cents. It is opt-in
// (REPLICHAI_LATERE_E2E=1), because it reaches the network and spends money;
// it prints the transcript when the session does not pass, before the session
// is released, so a failure says why. REPLICHAI_LATERE_ENVIRONMENT names the
// environment the session runs on, and REPLICHAI_LATERE_MODEL the model, read
// as --model reads it.
//
// LATERE_TOKEN_COMMAND must print a bearer addressed to api.latere.ai.
// `latere print-token` is not one: it prints the Latere CLI's login token,
// which only Latere Auth accepts, and the platform answers 401.
func TestRealLatereEndToEnd(t *testing.T) {
	if os.Getenv("REPLICHAI_LATERE_E2E") != "1" {
		t.Skip("set REPLICHAI_LATERE_E2E=1 and sign in with `replichai login` to run a real session")
	}
	var tokens TokenSource
	switch {
	case os.Getenv("LATERE_API_KEY") != "":
		tokens = PersonalAccessToken("https://auth.latere.ai", os.Getenv("LATERE_API_KEY"), defaultHTTPClient())
	case os.Getenv("LATERE_TOKEN_COMMAND") != "":
		tokens = Command(os.Getenv("LATERE_TOKEN_COMMAND"))
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		authURL := os.Getenv("REPLICHAI_AUTH_URL")
		if authURL == "" {
			authURL = cloud.DefaultAuthURL
		}
		session := cloud.New(home, authURL)
		tokens = Expiring(func(ctx context.Context) (string, time.Time, error) {
			return session.ActorToken(ctx, signin.PlatformAudience, cloud.ActorTTL)
		})
	}
	model := os.Getenv("REPLICHAI_LATERE_MODEL")
	if model == "" {
		model = "balanced"
	}
	r, err := New(Config{Tokens: tokens})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if _, err := r.Capabilities(ctx); err != nil {
		t.Fatal(err)
	}
	selector := audit.ModelSelector(model)
	task, rubric := "Create outputs/result.txt containing only the number that is the sum of 2 and 3. Then end your turn.",
		"1. outputs/result.txt exists.\n2. It contains the number 5 and nothing else."
	// REPLICHAI_LATERE_ENVIRONMENT=science checks the statistics image on the
	// hosted plane instead: R and lme4 must be there already, so the session
	// fits a mixed model without installing anything.
	environment := os.Getenv("REPLICHAI_LATERE_ENVIRONMENT")
	if environment == "science" {
		task = "Run this command in the shell, exactly: Rscript -e 'suppressMessages(library(lme4)); cat(round(fixef(lmer(Reaction ~ Days + (Days|Subject), sleepstudy))[[\"Days\"]], 2))' " +
			"Write what it prints, the number alone, to outputs/result.txt. Install nothing. Then end your turn."
		rubric = "1. outputs/result.txt exists.\n2. It contains the number 10.47 and nothing else.\n3. The transcript shows no package being installed."
	}
	spec := managed.SessionSpec{
		Model:        selector,
		Instructions: "You are a careful research engineer. Work in the working directory.",
		Task:         task,
		Sandbox:      managed.SandboxSpec{Environment: environment, Network: managed.NetworkPolicy{Mode: managed.NetworkOpen}},
		Evaluation:   &managed.EvaluationSpec{Rubric: rubric, MaxRevisions: 1},
	}
	ref, err := r.Start(ctx, spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		if err := r.Release(context.WithoutCancel(ctx), ref); err != nil {
			t.Errorf("Release: %v", err)
		}
	}()
	var status managed.Status
	for !status.State.Terminal() {
		if status, err = r.Poll(ctx, ref); err != nil {
			t.Fatalf("Poll: %v", err)
		}
		if !status.State.Terminal() {
			time.Sleep(5 * time.Second)
		}
	}
	artifacts, err := r.ListArtifacts(ctx, ref)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	t.Logf("session %s on %s/%s: %s %s %q, %d artifacts, %d micro-USD", ref.ID, status.Model.Provider, status.Model.ID,
		status.State, status.TerminalReason, status.Error, len(artifacts), status.Usage.Extra[CostMeter])
	if !status.Passed() {
		events, _ := r.Events(ctx, ref)
		for _, event := range events {
			if event.Kind == "model.request" {
				continue
			}
			t.Logf("%s %s %s %.400s", event.Kind, event.Role, event.Tool, event.Content)
		}
		t.Fatalf("the session did not pass: %+v", status.Verdict)
	}
}
