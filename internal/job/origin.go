package job

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Origin says where a session's work actually happened. It is the one column
// the ingest of a locally-executed session adds, and everything else about
// such a session is an ordinary session: it lands under a project, against a
// paper, and every read path renders it unchanged (045k).
type Origin string

// The origins. Platform is the default and the zero-ish value, so every
// existing row keeps its meaning with no backfill.
const (
	// OriginPlatform is a session this deployment ran and metered.
	OriginPlatform Origin = "platform"
	// OriginLocal is a session somebody ran on their own machine with
	// their own model credential, and pushed the record here. Nothing about
	// it touches the ledger: those tokens were bought from the vendor by
	// the operator.
	OriginLocal Origin = "local"
)

// Valid reports whether o is an origin this build defines. The set is closed:
// an unknown origin is refused rather than stored as a string nobody reads.
func (o Origin) Valid() bool { return o == OriginPlatform || o == OriginLocal }

// OrDefault reads an empty origin as platform, which is what every row written
// before this column existed is.
func (o Origin) OrDefault() Origin {
	if strings.TrimSpace(string(o)) == "" {
		return OriginPlatform
	}
	return o
}

// Metered reports whether this deployment charged for the session. Only a
// platform session is: a local one spent the operator's own credential, so no
// reserve was taken and no settlement will happen.
func (o Origin) Metered() bool { return o.OrDefault() == OriginPlatform }

// UnmarshalJSON refuses an origin this build does not know, rather than
// letting it arrive as an empty string that reads as platform.
func (o *Origin) UnmarshalJSON(raw []byte) error {
	var name string
	if err := json.Unmarshal(raw, &name); err != nil {
		return err
	}
	candidate := Origin(strings.ToLower(strings.TrimSpace(name)))
	if candidate != "" && !candidate.Valid() {
		return fmt.Errorf("unknown session origin %q", name)
	}
	*o = candidate
	return nil
}
