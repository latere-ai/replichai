package job

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// scanValues is the pgx.Row seam scanJob consumes. It lets corruption at the
// database boundary be reproduced without requiring a live Postgres process.
type scanValues struct{ values []any }

func (r scanValues) Scan(dest ...any) error {
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

var _ pgx.Row = scanValues{}

func TestScanJobRejectsMalformedRateCard(t *testing.T) {
	now := time.Now()
	row := scanValues{values: []any{
		"job-1", KindAudit, "sub", "project", "ref", "paper", PhaseRunning,
		[]byte(`{}`), "", "", now, now, []byte(`{}`), []byte(`{"models":"bad"}`),
	}}

	if _, err := scanJob(row); err == nil || !strings.Contains(err.Error(), "decode rate_card") {
		t.Fatalf("scanJob error = %v, want malformed rate-card error", err)
	}
}
