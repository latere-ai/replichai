// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/provider"
)

// benchRow is a pgx.Row that hands scanJob the stored values, so the decode of
// the jsonb columns runs without a database.
type benchRow struct{ values []any }

func (r benchRow) Scan(dest ...any) error {
	for i, value := range r.values {
		switch d := dest[i].(type) {
		case *string:
			*d = value.(string)
		case *Phase:
			*d = value.(Phase)
		case *Kind:
			*d = value.(Kind)
		case *[]byte:
			*d = value.([]byte)
		case *time.Time:
			*d = value.(time.Time)
		default:
			panic("unsupported scan destination")
		}
	}
	return nil
}

var _ pgx.Row = benchRow{}

func benchRunStatus() provider.RunStatus {
	return provider.RunStatus{
		State: provider.RunState(0), RunID: "sesn_01HZX3K9Q2", Runtime: "anthropic-managed",
		SessionRef: json.RawMessage(`{"runtime":"anthropic-managed","id":"sesn_01HZX3K9Q2","revision":14,"locator_version":1}`),
		Model:      "claude-opus-5", ModelProvider: "anthropic", BillingVariant: "standard",
		Usage: provider.Usage{
			InputTokens: 182000, OutputTokens: 41000, CachedInputTokens: 910000,
			CacheWrite5mTokens: 64000, CacheWrite1hTokens: 12000,
		},
	}
}

func benchPhaseAt() map[Phase]time.Time {
	at := time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)
	return map[Phase]time.Time{
		PhaseSubmitted:  at,
		PhaseResolving:  at.Add(2 * time.Second),
		PhaseRunning:    at.Add(25 * time.Second),
		PhasePublishing: at.Add(41 * time.Minute),
		PhaseDone:       at.Add(42 * time.Minute),
	}
}

func benchCard(models int) credit.Card {
	card := credit.Card{}
	for i := range models {
		id := fmt.Sprintf("claude-model-%d", i)
		card.Models = append(card.Models, credit.Rates{
			Model: id, Ref: managed.ModelRef{Provider: "anthropic", ID: id},
			Input: 15_000_000, Output: 75_000_000, CacheRead: 1_500_000,
			CacheWrite5m: 18_750_000, CacheWrite1h: 30_000_000,
		})
	}
	return card
}

// BenchmarkJSONSessionStatusMarshal is the write of a session's status column,
// which the worker repeats on every observation of a running session.
func BenchmarkJSONSessionStatusMarshal(b *testing.B) {
	status := benchRunStatus()
	first, err := encodeStatus(status)
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("payload_bytes=%d", len(first))
	b.ReportAllocs()
	b.SetBytes(int64(len(first)))
	for b.Loop() {
		if _, err := encodeStatus(status); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkJSONSessionPhaseAtMarshal is the write of the phase timeline column.
func BenchmarkJSONSessionPhaseAtMarshal(b *testing.B) {
	phaseAt := benchPhaseAt()
	first, err := encodePhaseAt(phaseAt)
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("payload_bytes=%d", len(first))
	b.ReportAllocs()
	b.SetBytes(int64(len(first)))
	for b.Loop() {
		if _, err := encodePhaseAt(phaseAt); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkJSONSessionRowUnmarshal is scanJob decoding the three jsonb columns
// (status, phase_at, rate_card) of one stored row, the read every session
// lookup and listing performs.
func BenchmarkJSONSessionRowUnmarshal(b *testing.B) {
	for _, size := range []struct {
		name   string
		models int
	}{{"small", 1}, {"medium", 12}} {
		b.Run(size.name, func(b *testing.B) {
			status, err := encodeStatus(benchRunStatus())
			if err != nil {
				b.Fatal(err)
			}
			phaseAt, err := encodePhaseAt(benchPhaseAt())
			if err != nil {
				b.Fatal(err)
			}
			card, err := json.Marshal(benchCard(size.models))
			if err != nil {
				b.Fatal(err)
			}
			now := time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)
			row := benchRow{values: []any{
				"job_7f3a91c2", KindAudit, "5d0c1f7e-3b1a-4c64-9a0e-2f4f6c1d8b11", "prj_4e1b9a", "arxiv:2603.01795", "arxiv:2603.01795",
				PhaseRunning, []byte(status), "open-hai/repro-2603-01795", "", now, now, []byte(phaseAt), card,
			}}
			total := len(status) + len(phaseAt) + len(card)
			b.Logf("payload_bytes=%d", total)
			b.ReportAllocs()
			b.SetBytes(int64(total))
			for b.Loop() {
				if _, err := scanJob(row); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
