// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package profile stores a principal's self-asserted researcher identity: the
// identity a public sign-off is published under. It holds the display name,
// the institution, the contact address the public may write to, and the
// scholarly identifiers that let an attestation be read as an endorsement
// rather than a username (006n).
//
// The package is deliberately separate from internal/access. A grant is an
// authorisation record and stores no display data; a profile is display data
// and grants nothing. Nothing in a profile is verified. The platform does not
// check that someone works where they say they work, just as a preprint
// server does not. What the package does guarantee is the binding: a profile
// is always written against the calling principal, never against one named in
// a request body.
//
// A profile is only a default. A published sign-off reports the snapshot
// copied onto the project when it was signed (006n), so editing a profile
// never rewrites an attestation that is already public.
package profile

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Errors this package returns. Each names one field so the HTTP layer can map it
// to a distinct code and the UI can point at the input that is wrong.
var (
	// ErrNoPrincipal reports a save with no owning principal.
	ErrNoPrincipal = errors.New("a profile needs a principal")
	// ErrInvalidContact reports a contact address that is not an email.
	ErrInvalidContact = errors.New("contact must be an email address")
	// ErrInvalidORCID reports an identifier that is not an ORCID iD.
	ErrInvalidORCID = errors.New("orcid must look like 0000-0002-1825-0097")
	// ErrInvalidScholar reports a URL that is not a Google Scholar profile.
	ErrInvalidScholar = errors.New("scholar must be a scholar.google URL")
	// ErrInvalidHandle reports a handle that is not a usable public name.
	ErrInvalidHandle = errors.New("handle must be 3-39 characters of a-z, 0-9 and -")
	// ErrHandleReserved reports a handle that would shadow one of the app's own
	// paths.
	ErrHandleReserved = errors.New("that handle is reserved")
	// ErrHandleTaken reports a handle somebody else already holds. It is
	// returned by the store, which is the only place that can know.
	ErrHandleTaken = errors.New("that handle is already taken")
	// ErrNotFound reports a public profile that does not exist, is private, or
	// has no handle. One error covers all three deliberately, so the public
	// endpoint cannot be used to enumerate handles.
	ErrNotFound = errors.New("no such public profile")
)

// Profile is one principal's self-asserted researcher identity. Every field is
// optional in the store. The publish wizard, not the store, requires a
// contact (006n), because that requirement belongs to publishing rather than
// to having a profile.
type Profile struct {
	Principal   string    `json:"-"` // the owning principal; never read from a body
	DisplayName string    `json:"display_name,omitempty"`
	Institution string    `json:"institution,omitempty"`
	Contact     string    `json:"contact_email,omitempty"` // published for inquiries
	ORCID       string    `json:"orcid,omitempty"`
	Scholar     string    `json:"scholar_url,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitzero"`

	// Handle is this researcher's public name, the /u/{handle} path their
	// published sign-offs link to (006r). It is unique across the platform,
	// chosen and editable by its owner, and empty until they pick one.
	//
	// A handle is needed because a profile is keyed by an OIDC subject, which
	// cannot go in a URL: it is unreadable, and it is the identity provider's
	// internal identifier. A handle is an identifier somebody can put in a CV.
	Handle string `json:"handle,omitempty"`
	// Public reports whether this profile has a page. It is false by default.
	// Turning it on reveals no new information, since every field is already
	// published on any sign-off; it reveals the aggregation, and that is a
	// decision for the owner rather than the platform.
	Public bool `json:"public"`
}

// Store holds one profile per principal.
type Store interface {
	// Get returns a principal's profile. A principal who has never saved one is
	// not an error: the zero profile is returned, so the settings screen and the
	// publish wizard have something to render on first use.
	Get(ctx context.Context, principal string) (Profile, error)
	// Save writes a principal's profile, replacing any previous one. It validates
	// first, so an invalid identifier never reaches the table, and returns
	// ErrHandleTaken when the handle belongs to somebody else.
	Save(ctx context.Context, p Profile) (Profile, error)
	// ByHandle resolves a public profile by its handle, for the page anyone
	// may read (006r). It returns ErrNotFound for an unknown handle and for a
	// private one alike, because distinguishing them would allow enumerating
	// who holds which handle.
	ByHandle(ctx context.Context, handle string) (Profile, error)
	// PrincipalHandle returns the public handle of a principal, if they have
	// one and their profile is public. A sign-off uses it to link to its signer
	// without storing a handle on the project. The link resolves through the
	// profile as it is now, so it stays current rather than becoming a second,
	// stale copy (006n).
	PrincipalHandle(ctx context.Context, principal string) (string, bool, error)
}

// handlePattern matches what may appear in a public URL: lowercase letters,
// digits and hyphens, between 3 and 39 characters, so a handle is long enough
// to be a name and short enough to fit on one line.
var handlePattern = regexp.MustCompile(`^[a-z0-9-]{3,39}$`)

// reservedHandles lists the app's own first path segments. A profile at /u/
// cannot shadow a route today, but the list is kept because these are also
// the names most likely to be taken by somebody impersonating the platform.
var reservedHandles = map[string]bool{
	"admin": true, "api": true, "public": true, "settings": true, "projects": true,
	"u": true, "me": true, "login": true, "logout": true, "callback": true,
	"healthz": true, "shared": true, "jobs": true, "papers": true, "resolve": true,
	"profiles": true, "new": true, "about": true, "support": true, "replichai": true,
	"latere": true,
}

// orcidPattern is the ORCID iD form: four groups of four, the last character a
// checksum digit or X.
var orcidPattern = regexp.MustCompile(`^\d{4}-\d{4}-\d{4}-\d{3}[\dX]$`)

// scholarHostPattern anchors the whole host, so a lookalike that only starts
// with the real one, such as scholar.google.com.evil.test, is refused. The
// optional second group admits the country forms, such as
// scholar.google.co.uk.
var scholarHostPattern = regexp.MustCompile(`^scholar\.google(\.[a-z]{2,3}){1,2}$`)

// Normalize trims every field and canonicalises what has a canonical form, so a
// pasted address with a trailing space and a typed one compare equal.
func (p Profile) Normalize() Profile {
	p.DisplayName = strings.TrimSpace(p.DisplayName)
	p.Institution = strings.TrimSpace(p.Institution)
	p.Contact = strings.ToLower(strings.TrimSpace(p.Contact))
	p.ORCID = strings.ToUpper(strings.TrimSpace(p.ORCID))
	p.Scholar = strings.TrimSpace(p.Scholar)
	p.Handle = strings.ToLower(strings.TrimSpace(p.Handle))
	return p
}

// Validate reports the first field that is present and malformed. An absent
// optional field is valid. A missing identifier is better on a public record
// than a broken one, and refusing malformed values keeps broken ones off the
// record.
func (p Profile) Validate() error {
	if p.Contact != "" {
		if err := ValidateContact(p.Contact); err != nil {
			return err
		}
	}
	if p.ORCID != "" && !orcidPattern.MatchString(p.ORCID) {
		return ErrInvalidORCID
	}
	if p.Scholar != "" {
		if err := ValidateScholar(p.Scholar); err != nil {
			return err
		}
	}
	if p.Handle != "" {
		if err := ValidateHandle(p.Handle); err != nil {
			return err
		}
	}
	return nil
}

// ValidateHandle reports whether a handle may appear in a public URL. It
// checks the shape first and the reserved list second, so the more specific
// message wins.
func ValidateHandle(handle string) error {
	if !handlePattern.MatchString(handle) {
		return ErrInvalidHandle
	}
	if reservedHandles[handle] {
		return ErrHandleReserved
	}
	return nil
}

// PublicProfile is what a visitor sees at /u/{handle}: the self-asserted
// identity and the public work signed under it.
//
// The contact is rendered in full, as a sign-off card renders it, because it
// was published for inquiries. The account email never appears. The redacted
// signer email on an attestation covers that need, and it stays there, where
// it identifies a signature rather than naming a person.
type PublicProfile struct {
	Handle      string         `json:"handle"`
	DisplayName string         `json:"display_name,omitempty"`
	Institution string         `json:"institution,omitempty"`
	Contact     string         `json:"contact_email,omitempty"`
	ORCID       string         `json:"orcid,omitempty"`
	Scholar     string         `json:"scholar_url,omitempty"`
	Projects    []PublicSigned `json:"projects"`
}

// PublicSigned is one public project this researcher signed.
type PublicSigned struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	SignedOffAt *time.Time `json:"signed_off_at,omitempty"`
}

// ValidateContact reports whether an address is a single plain email, the
// address a visitor is given on a public page. A display-name form such as
// "Ada <ada@x.org>" is refused: it renders badly into a mailto: link, and the
// name belongs in the name field.
func ValidateContact(contact string) error {
	addr, err := mail.ParseAddress(contact)
	if err != nil || addr.Name != "" || addr.Address != strings.ToLower(strings.TrimSpace(contact)) {
		return ErrInvalidContact
	}
	return nil
}

// ValidateScholar reports whether a URL is an https Google Scholar profile. The
// host check is what stops the field becoming an arbitrary link on a public
// attestation.
func ValidateScholar(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return ErrInvalidScholar
	}
	if !scholarHostPattern.MatchString(strings.ToLower(u.Hostname())) {
		return ErrInvalidScholar
	}
	return nil
}

// RedactEmail masks the local part of an address for publication: the first
// and last characters are kept, the middle becomes stars, and the domain is
// kept. A signer is identified on a public card this way without their
// working address being published.
//
// A one- or two-character local part is masked whole rather than left readable.
// A string that is not an address is returned empty, so a malformed value can
// never leak through unmasked.
func RedactEmail(email string) string {
	email = strings.TrimSpace(email)
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return ""
	}
	local, domain := email[:at], email[at+1:]
	if len(local) <= 2 {
		return strings.Repeat("*", len(local)) + "@" + domain
	}
	return fmt.Sprintf("%c%s%c@%s", local[0], strings.Repeat("*", len(local)-2), local[len(local)-1], domain)
}
