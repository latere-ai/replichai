// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package publish

import (
	"testing"

	"github.com/latere-ai/replichai/internal/managed"
)

// A run the platform moved between models records every model that served,
// and a run one model served keeps its record as it was (048).
func TestARunRecordsEveryModelThatServed(t *testing.T) {
	sonnet := managed.ModelRef{Provider: "anthropic", ID: "claude-sonnet-5.5"}
	gpt := managed.ModelRef{Provider: "openai", ID: "gpt-6.1-sol"}
	status := managed.Status{Model: sonnet, Usage: managed.Usage{Extra: map[managed.Meter]int64{managed.MeterChargedCost: 4200}}}
	events := []managed.Event{
		{ID: "1", Usage: &managed.Usage{Model: sonnet}},
		{ID: "2"},
		{ID: "3", Usage: &managed.Usage{Model: gpt}},
		{ID: "4", Usage: &managed.Usage{Model: sonnet}},
	}
	record := RunOf(status).WithModels(events)
	if len(record.Models) != 2 || record.Models[0].ID != "claude-sonnet-5.5" || record.Models[1].ID != "gpt-6.1-sol" {
		t.Fatalf("models = %+v", record.Models)
	}
	if record.VendorCostMicroUSD != 4200 {
		t.Fatalf("a platform's charge = %d, want it as the run's cost", record.VendorCostMicroUSD)
	}
	if one := RunOf(status).WithModels(events[:2]); one.Models != nil {
		t.Fatalf("a run one model served lists %+v", one.Models)
	}
	if none := RunOf(managed.Status{}).WithModels(nil); none.Models != nil {
		t.Fatal("a run with no calls lists models")
	}
}
