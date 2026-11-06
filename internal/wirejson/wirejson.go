// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package wirejson holds the encoding/json/v2 option sets this module uses, so
// each call site says where its bytes come from or go to rather than repeating
// the options.
package wirejson

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
)

// Foreign reads JSON we do not produce: model output, provider and webhook
// payloads, files an agent or a person wrote. Duplicate keys (the last wins),
// invalid UTF-8 and any letter case in a member name are accepted.
var Foreign = json.JoinOptions(
	jsontext.AllowDuplicateNames(true),
	jsontext.AllowInvalidUTF8(true),
	json.MatchCaseInsensitiveNames(true),
)

// Outbound writes a request to a service we do not own. Keys are ordered so
// equal requests are byte-identical, a nil slice or map is null, which is what
// those services have always received, and text carrying invalid UTF-8 becomes
// U+FFFD instead of failing the request.
var Outbound = json.JoinOptions(
	json.Deterministic(true),
	jsontext.AllowInvalidUTF8(true),
	json.FormatNilSliceAsNull(true),
	json.FormatNilMapAsNull(true),
)

// Stored writes JSON that is stored, hashed, compared or committed: member
// order is fixed so equal values give equal bytes, and text carrying invalid
// UTF-8 becomes U+FFFD instead of failing the write.
var Stored = json.JoinOptions(
	json.Deterministic(true),
	jsontext.AllowInvalidUTF8(true),
)

// Text writes strings that carry bytes we do not control: invalid UTF-8
// becomes U+FFFD instead of failing the whole value.
var Text = jsontext.AllowInvalidUTF8(true)

// Digest writes JSON that a name or checksum is derived from. It reproduces the
// bytes encoding/json wrote before, so a name derived from a value earlier
// stays the same: keys ordered, nil slices and maps as null, and <, >, & and
// U+2028 and U+2029 escaped.
var Digest = json.JoinOptions(
	json.Deterministic(true),
	jsontext.AllowInvalidUTF8(true),
	json.FormatNilSliceAsNull(true),
	json.FormatNilMapAsNull(true),
	jsontext.EscapeForHTML(true),
	jsontext.EscapeForJS(true),
)

// Embedding writes a value that holds raw JSON values produced elsewhere,
// which are checked again on the way out.
var Embedding = json.JoinOptions(
	jsontext.AllowDuplicateNames(true),
	jsontext.AllowInvalidUTF8(true),
)

// Join combines option sets.
func Join(opts ...json.Options) json.Options { return json.JoinOptions(opts...) }
