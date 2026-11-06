// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"testing"

	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/paper"
)

// offers is a deployment with Anthropic as the default, paid by the project,
// and Latere, paid by the person, which offers two exact models.
func offers(modelsFail bool) []RuntimeOffer {
	return []RuntimeOffer{
		{Name: "anthropic-managed", Label: "Anthropic", Default: true},
		{Name: "latere", Label: "Latere", PersonPays: true,
			Efforts: func(_ context.Context, principal string) ([]EffortOffer, error) {
				if principal != "sub-1" {
					return nil, errors.New("not signed in")
				}
				return []EffortOffer{{Tier: "best", Effort: "sustained", Model: "claude-opus-5.5", Provider: "anthropic", Input: 4_000_000, Output: 20_000_000}}, nil
			},
			Models: func(context.Context, string) ([]string, error) {
				if modelsFail {
					return nil, errors.New("platform unreachable")
				}
				return []string{"anthropic/claude-sonnet-4.6", "openai/gpt-6.1-sol"}, nil
			}},
	}
}

func startWith(t *testing.T, d Deps, body string) (int, string, job.Job) {
	t.Helper()
	h := NewRouter(d).Handler()
	const url = "https://arxiv.org/abs/2302.03714"
	_, projectID := createWithPaper(t, h, url, "sub-1")
	key, err := paper.Key(url)
	if err != nil {
		t.Fatal(err)
	}
	w := do(t, h, "POST", "/projects/"+projectID+"/sessions", `{"paper_key":"`+key+`"`+body+`}`, "sub-1")
	var result struct {
		JobID string `json:"job_id"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	var j job.Job
	if result.JobID != "" {
		if j, err = d.Jobs.Get(context.Background(), result.JobID); err != nil {
			t.Fatal(err)
		}
	} else if jobs, _ := d.Jobs.ListByProject(context.Background(), projectID); len(jobs) != 0 {
		t.Fatalf("a refused start left %d sessions", len(jobs))
	}
	return w.Code, result.Error, j
}

func TestASessionRunsOnTheRuntimeAndModelChosen(t *testing.T) {
	d := testDeps(allowAll)
	d.Runtimes = offers(false)
	code, _, j := startWith(t, d, `,"tier":"balanced","runtime":"latere","model":"openai/gpt-6.1-sol"`)
	if code != http.StatusCreated || j.Runtime != "latere" || j.Model != "openai/gpt-6.1-sol" || j.Payer != job.PayerPerson {
		t.Fatalf("start = %d, job %+v", code, j)
	}
}

func TestASessionNamingNoRuntimeRunsOnTheDefault(t *testing.T) {
	d := testDeps(allowAll)
	d.Runtimes = offers(false)
	code, _, j := startWith(t, d, `,"tier":"fast"`)
	if code != http.StatusCreated || j.Runtime != "anthropic-managed" || j.Model != "" || j.Payer != job.PayerProject {
		t.Fatalf("start = %d, job %+v", code, j)
	}
}

func TestARuntimeOrModelNotOfferedIsRefusedBeforeEnqueue(t *testing.T) {
	cases := []struct {
		name, body, code string
		status           int
		fail             bool
		noOffers         bool
	}{
		{"an unknown runtime", `,"runtime":"openai"`, "bad_runtime", http.StatusBadRequest, false, false},
		{"a model the person may not use", `,"runtime":"latere","model":"meta/llama-5"`, "bad_model", http.StatusBadRequest, false, false},
		{"a model on a runtime with no choice", `,"model":"anthropic/claude-sonnet-4.6"`, "bad_model", http.StatusBadRequest, false, false},
		{"a model list that cannot be read", `,"runtime":"latere","model":"openai/gpt-6.1-sol"`, "models_unavailable", http.StatusBadGateway, true, false},
		{"a runtime where none is offered", `,"runtime":"latere"`, "bad_runtime", http.StatusBadRequest, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := testDeps(allowAll)
			if !c.noOffers {
				d.Runtimes = offers(c.fail)
			}
			status, code, _ := startWith(t, d, c.body)
			if status != c.status || code != c.code {
				t.Fatalf("start = %d %q, want %d %q", status, code, c.status, c.code)
			}
		})
	}
}

func TestModelsListTheOfferedRuntimes(t *testing.T) {
	d := tierDeps(&stubResolver{})
	d.Runtimes = offers(false)
	w := do(t, NewRouter(d).Handler(), "GET", "/models", "", "sub-1")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /models = %d", w.Code)
	}
	var got struct {
		Runtimes []runtimeView `json:"runtimes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Runtimes) != 2 {
		t.Fatalf("runtimes = %+v", got.Runtimes)
	}
	anthropic, latere := got.Runtimes[0], got.Runtimes[1]
	if !anthropic.Default || anthropic.Payer != "project" || len(anthropic.Efforts) != 3 || anthropic.Efforts[0].Effort != "sustained" || anthropic.Efforts[0].Model != "claude-opus-5" {
		t.Fatalf("anthropic = %+v", anthropic)
	}
	if latere.Default || latere.Payer != "person" || latere.Label != "Latere" || len(latere.Models) != 2 || latere.Efforts[0].Input != 4_000_000 {
		t.Fatalf("latere = %+v", latere)
	}
	w = do(t, NewRouter(d).Handler(), "GET", "/models", "", "sub-2")
	var other struct {
		Runtimes []runtimeView `json:"runtimes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &other); err != nil {
		t.Fatal(err)
	}
	if other.Runtimes[1].Err != "runtime_unavailable" || other.Runtimes[1].Models != nil {
		t.Fatalf("a person the runtime cannot act for = %+v", other.Runtimes[1])
	}
}
