// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import (
	"encoding/json/v2"
	"fmt"
	"strings"
)

// Origin records where a session's work happened. It is the only column that
// importing a locally executed session adds. In every other respect such a
// session is an ordinary session: it belongs to a project, targets a paper,
// and every read path renders it unchanged (045k).
type Origin string

// The origins. Platform is the default and what an empty value is read as, so
// every existing row keeps its meaning with no backfill.
const (
	// OriginPlatform is a session this deployment ran and metered.
	OriginPlatform Origin = "platform"
	// OriginLocal is a session somebody ran on their own machine with their
	// own model credential and pushed the record here. It never touches the
	// ledger, because the operator bought those tokens from the vendor.
	OriginLocal Origin = "local"
)

// Valid reports whether o is an origin this build defines. The set is closed:
// an unknown origin is refused rather than stored as a string nobody reads.
func (o Origin) Valid() bool { return o == OriginPlatform || o == OriginLocal }

// OrDefault reads an empty origin as platform, because every row written
// before this column existed was a platform session.
func (o Origin) OrDefault() Origin {
	if strings.TrimSpace(string(o)) == "" {
		return OriginPlatform
	}
	return o
}

// Metered reports whether this deployment charged for the session. Only a
// platform session is metered. A local session used the operator's own
// credential, so no reserve was taken and no settlement happens.
func (o Origin) Metered() bool { return o.OrDefault() == OriginPlatform }

// UnmarshalJSON rejects an origin this build does not define, rather than
// decoding it to an empty string that would be read as platform.
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
