// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package verdict_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/verdict"
)

// benchVerdict builds a deterministic verdict with n boundary claims, each with
// one numeric mismatch, plus a fixed set of decisions, scorecard rows and
// releases.
func benchVerdict(n int) verdict.Verdict {
	reported, observed := 0.842, 0.8317
	v := verdict.Verdict{
		Schema:   verdict.Schema,
		Verdict:  verdict.Partial,
		Summary:  strings.Repeat("The run reproduced the main table within tolerance but could not reach the held-out split. ", 3),
		GradedAt: time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC),
		Components: verdict.Components{
			Verified: n / 2, Partial: n / 4, Blocked: n - n/2 - n/4, Total: n,
		},
	}
	for i := range n {
		loop, outcome := verdict.Inner, verdict.Verified
		if i%5 == 4 {
			loop, outcome = verdict.Outer, verdict.NotScored
		}
		v.Boundary = append(v.Boundary, verdict.Claim{
			Claim:    fmt.Sprintf("Claim %d: the model reaches the reported accuracy on benchmark split %d.", i, i%7),
			Loop:     loop,
			Outcome:  outcome,
			Evidence: fmt.Sprintf("Run 3 of the evaluation script printed 83.17 on split %d after 40 epochs with seed %d.", i%7, i),
			Citation: fmt.Sprintf("Table %d", i%6+1),
			Mismatches: []verdict.Mismatch{{
				Kind: verdict.Numeric, What: "top-1 accuracy", Reported: "84.2", Observed: "83.17",
				ReportedValue: &reported, ObservedValue: &observed, Unit: "%",
				Note: "Within one standard deviation of the seed spread.",
			}},
		})
	}
	for i := range max(2, n/8) {
		v.Decisions = append(v.Decisions, verdict.Decision{
			Question:    fmt.Sprintf("Which learning-rate schedule did the authors use in stage %d?", i),
			Where:       "Section 4.2, paragraph 3",
			Assumed:     "Cosine decay to zero with 500 warm-up steps.",
			Sensitivity: "Accuracy moves by about 0.4 points if a linear schedule was used.",
		})
	}
	for _, c := range verdict.Criteria {
		v.Scorecard = append(v.Scorecard, verdict.Score{Criterion: c, Found: true, URL: "https://github.com/example/paper-" + string(c)})
	}
	v.Released = append(v.Released,
		verdict.Release{Where: "GitHub", What: "training code", Result: verdict.Found, URL: "https://github.com/example/paper"},
		verdict.Release{Where: "OSF", What: "held-out split", Result: verdict.NotFetched},
	)
	return v
}

var verdictSizes = []struct {
	name   string
	claims int
}{
	{"small", 2},
	{"medium", 40},
	{"large", 800},
}

func benchVerdictBytes(b *testing.B, claims int) []byte {
	b.Helper()
	raw, err := json.Marshal(benchVerdict(claims))
	if err != nil {
		b.Fatal(err)
	}
	return raw
}

func BenchmarkJSONVerdictParse(b *testing.B) {
	for _, size := range verdictSizes {
		b.Run(size.name, func(b *testing.B) {
			raw := benchVerdictBytes(b, size.claims)
			b.Logf("payload_bytes=%d", len(raw))
			b.ReportAllocs()
			b.SetBytes(int64(len(raw)))
			for b.Loop() {
				if _, _, err := verdict.Parse(raw); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkJSONVerdictValidate(b *testing.B) {
	for _, size := range verdictSizes {
		b.Run(size.name, func(b *testing.B) {
			raw := benchVerdictBytes(b, size.claims)
			b.Logf("payload_bytes=%d", len(raw))
			b.ReportAllocs()
			b.SetBytes(int64(len(raw)))
			for b.Loop() {
				if _, _, err := verdict.Validate(raw); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkJSONVerdictMarshal(b *testing.B) {
	for _, size := range verdictSizes {
		b.Run(size.name, func(b *testing.B) {
			v := benchVerdict(size.claims)
			encoded, err := json.Marshal(v)
			if err != nil {
				b.Fatal(err)
			}
			b.Logf("payload_bytes=%d", len(encoded))
			b.ReportAllocs()
			b.SetBytes(int64(len(encoded)))
			for b.Loop() {
				if _, err := json.Marshal(v); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
