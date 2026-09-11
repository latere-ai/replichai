// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package experiment

import (
	"strconv"
	"strings"

	"github.com/latere-ai/replichai/internal/instrument"
)

// Comparison lays a released-source outcome beside the paper's own reported
// results. It is a report, not a verdict: re-running the paper's data through
// the paper's pipeline has a right answer, so the platform shows the two side
// by side and does not conclude whether the paper reproduced. That conclusion
// is the researcher's to draw and defend, and is out of scope under 007.
//
// It carries no pass/fail field. A divergence may be a rounding convention, a
// library version, or a real error, and telling those apart is the purpose of
// the report; a boolean cannot hold that. AC8 asserts the absence of such a
// field structurally, so a later change cannot add one silently.
type Comparison struct {
	// Source is what the comparison is about. It determines the meaning: the
	// same arithmetic reads as re-analysis of the paper for a released source
	// and as calibration of a cohort for a synthetic one (007c).
	Source Source          `json:"source"`
	Rows   []ComparisonRow `json:"rows"`
}

// Subject names what the comparison establishes, so a synthetic calibration can
// never be rendered as re-analysis of the paper. This is the rendering half of
// 007c's labelling: a released comparison is about the paper; a synthetic one
// is about the model cohort and never about the paper or about people.
func (c Comparison) Subject() string {
	switch c.Source {
	case SourceReleased:
		return "re-analysis of the paper's released data against its reported results"
	case SourceSynthetic:
		return "calibration — how closely a synthetic cohort tracked the paper's reported results, a statement about the cohort and never about the paper"
	default:
		return "comparison against the paper's reported results"
	}
}

// ComparisonRow pairs one metric's reported value with its recomputed one.
type ComparisonRow struct {
	Metric     string `json:"metric"`
	Reported   string `json:"reported"`           // from protocol.reported; "" if unpaired
	Recomputed string `json:"recomputed"`         // from the outcome; "" if unpaired
	Delta      string `json:"delta,omitempty"`    // both values' numeric difference, when both parse
	Note       string `json:"note,omitempty"`     // why a row is unpaired or a delta is absent
	Citation   string `json:"citation,omitempty"` // the reported value's paper citation
}

// CompareFor produces the comparison for an experiment's outcome, or reports
// that this experiment is not compared. Only a released or synthetic run
// against an instrument that declares reported results is laid beside
// protocol.reported: an uploaded finding is the researcher's own and has no
// paper baseline, and a source with nothing reported has nothing to compare
// against. The caller resolves the instrument, the runner fills the outcome,
// and CompareFor decides whether a comparison is meaningful.
func CompareFor(e *Experiment, inst Instrument) (Comparison, bool) {
	// Released re-analysis and synthetic calibration are both laid beside the
	// paper's reported results; an uploaded finding is the researcher's own,
	// with no paper baseline to compare against.
	switch e.Source {
	case SourceReleased, SourceSynthetic:
	default:
		return Comparison{}, false
	}
	if e.Outcome == nil || inst.Decl.Protocol == nil {
		return Comparison{}, false
	}
	c := Compare(*e.Outcome, inst.Decl.Protocol.Reported)
	c.Source = e.Source
	return c, true
}

// Compare pairs every reported statistic with its recomputed counterpart by
// metric, in the reported order, then appends any recomputed metric the paper
// did not report. Each pair carries a numeric delta only when both values parse
// cleanly as numbers; otherwise the two values stand side by side with a note,
// which is the correct output for free-form statistics such as "b=42ms, p=.003".
func Compare(o Outcome, reported []instrument.Reported) Comparison {
	recomputed := make(map[string]string, len(o.Recomputed))
	for _, s := range o.Recomputed {
		recomputed[s.Metric] = s.Value
	}

	var rows []ComparisonRow
	seen := make(map[string]bool, len(reported))
	for _, r := range reported {
		seen[r.Metric] = true
		row := ComparisonRow{Metric: r.Metric, Reported: r.Value, Citation: r.Citation}
		if got, ok := recomputed[r.Metric]; ok {
			row.Recomputed = got
			if d, ok := numericDelta(r.Value, got); ok {
				row.Delta = d
			} else {
				row.Note = "values are not both numeric; compare by inspection"
			}
		} else {
			row.Note = "reported but not recomputed"
		}
		rows = append(rows, row)
	}

	for _, s := range o.Recomputed {
		if !seen[s.Metric] {
			rows = append(rows, ComparisonRow{
				Metric: s.Metric, Recomputed: s.Value, Note: "recomputed but not reported",
			})
		}
	}
	return Comparison{Rows: rows}
}

// numericDelta returns recomputed − reported when both parse as plain numbers.
func numericDelta(reported, recomputed string) (string, bool) {
	a, err1 := strconv.ParseFloat(strings.TrimSpace(reported), 64)
	b, err2 := strconv.ParseFloat(strings.TrimSpace(recomputed), 64)
	if err1 != nil || err2 != nil {
		return "", false
	}
	return strconv.FormatFloat(b-a, 'g', -1, 64), true
}
