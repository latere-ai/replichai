package experiment

import (
	"strconv"
	"strings"

	"github.com/latere-ai/replichai/internal/instrument"
)

// Comparison lays a released-source outcome beside the paper's own reported
// results. It is a report, not a verdict: re-running the paper's data through
// the paper's pipeline has a right answer, so the platform shows the two side
// by side and stops — it does not conclude the paper reproduced or failed to.
// That is a researcher's call to draw and defend (007's out-of-scope line).
//
// It deliberately carries no pass/fail field. A divergence may be a rounding
// convention, a library version, or a real error, and telling those apart is
// the work this report is for — not something a boolean could hold honestly.
// AC8 asserts the absence of such a field structurally, so a later change
// cannot add one silently.
type Comparison struct {
	Rows []ComparisonRow `json:"rows"`
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

// Compare pairs every reported statistic with its recomputed counterpart by
// metric, in the reported order, then appends any recomputed metric the paper
// did not report. Each pair carries a numeric delta only when both values parse
// cleanly as numbers; otherwise the two values stand side by side with a note,
// which is the honest output for free-form statistics like "b=42ms, p=.003".
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
