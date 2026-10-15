// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

// Payer is who pays for a session (048). It is decided by the runtime a
// session runs on and written at enqueue, beside the runtime, so a later
// change to a runtime's entry never re-bills a session that already ran.
type Payer string

const (
	// PayerProject is a session the project's pot pays for: held at start,
	// settled at the end, priced from the rate card (011).
	PayerProject Payer = "project"
	// PayerPerson is a session the person who started it pays for from their
	// own Latere wallet. The platform charges them; the ledger never sees it,
	// and its cost is what the platform reported.
	PayerPerson Payer = "person"
)

// PaysProject reports whether the project's pot pays. An empty payer is the
// project's, which is what every row written before the column was.
func (p Payer) PaysProject() bool { return p != PayerPerson }
