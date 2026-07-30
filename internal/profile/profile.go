// Package profile is who a principal says they are: the researcher identity a
// public sign-off is published under — display name, institution, the contact
// address the public may write to, and the scholarly identifiers that make an
// attestation read as an endorsement rather than a username (006k).
//
// It is deliberately not part of internal/access. A grant is an authorisation
// record and stores no display data; this is display data and grants nothing.
// Nothing here is verified: the platform does not check that someone works where
// they say they work, exactly as a preprint server does not. What it does
// guarantee is the binding — a profile is always written against the calling
// principal, never one named in a request body.
//
// A profile is only ever a *default*. What a published sign-off reports is the
// snapshot copied onto the project when it was signed (006k), so editing a
// profile never rewrites an attestation that is already public.
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
)

// Profile is one principal's self-asserted researcher identity. Every field is
// optional at rest — the publish wizard, not the store, is where a contact
// becomes required (006k), because that requirement belongs to the act of
// publishing rather than to having a profile.
type Profile struct {
	Principal   string    `json:"-"` // the owning principal; never read from a body
	DisplayName string    `json:"display_name,omitempty"`
	Institution string    `json:"institution,omitempty"`
	Contact     string    `json:"contact_email,omitempty"` // published for inquiries
	ORCID       string    `json:"orcid,omitempty"`
	Scholar     string    `json:"scholar_url,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitzero"`
}

// Store holds one profile per principal.
type Store interface {
	// Get returns a principal's profile. A principal who has never saved one is
	// not an error: the zero profile is returned, so the settings screen and the
	// publish wizard have something to render on first use.
	Get(ctx context.Context, principal string) (Profile, error)
	// Save writes a principal's profile, replacing any previous one. It validates
	// first, so an invalid identifier never reaches the table.
	Save(ctx context.Context, p Profile) (Profile, error)
}

// orcidPattern is the ORCID iD form: four groups of four, the last character a
// checksum digit or X.
var orcidPattern = regexp.MustCompile(`^\d{4}-\d{4}-\d{4}-\d{3}[\dX]$`)

// scholarHostPattern anchors the whole host, so a lookalike that merely *starts*
// with the real one (`scholar.google.com.evil.test`) is refused. The optional
// second group admits the country forms (`scholar.google.co.uk`).
var scholarHostPattern = regexp.MustCompile(`^scholar\.google(\.[a-z]{2,3}){1,2}$`)

// Normalize trims every field and canonicalises what has a canonical form, so a
// pasted address with a trailing space and a typed one compare equal.
func (p Profile) Normalize() Profile {
	p.DisplayName = strings.TrimSpace(p.DisplayName)
	p.Institution = strings.TrimSpace(p.Institution)
	p.Contact = strings.ToLower(strings.TrimSpace(p.Contact))
	p.ORCID = strings.ToUpper(strings.TrimSpace(p.ORCID))
	p.Scholar = strings.TrimSpace(p.Scholar)
	return p
}

// Validate reports the first field that is present and malformed. An absent
// optional field is valid — a missing identifier is better on a public record
// than a broken one, and this is what keeps the second true.
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
	return nil
}

// ValidateContact reports whether an address is a single, plain email — the
// address a stranger will be handed on a public page. A display-name form
// ("Ada <ada@x.org>") is refused: rendered into a mailto: it is a footgun, and
// the name belongs in the name field.
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

// RedactEmail masks the local part of an address for publication: the first and
// last characters survive, the middle becomes stars, the domain is kept. It is
// how a signer is identified on a public card without their working address
// being put on the wire.
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
