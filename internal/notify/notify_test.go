// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/pkg/email"
)

var ctx = context.Background()

// capture is a transport that keeps the last message instead of sending it.
type capture struct {
	msg   email.Message
	calls int
	err   error
}

func (c *capture) Send(_ context.Context, m email.Message) error {
	c.calls++
	if c.err != nil {
		return c.err
	}
	c.msg = m
	return nil
}

// footer is the last line of every text part: the copyright and the legal
// pages.
const footer = "© 2026 Latere AI · Privacy https://latere.ai/legal/privacy · " +
	"Terms https://latere.ai/legal/terms · Impressum https://latere.ai/legal/impressum\n"

// The freeze notice says the account is on hold and links to the wallet,
// where a top-up lifts the hold (022d). A nil Notifier sends nothing, so a
// deployment with no mail still runs.
func TestAccountFrozenLinksToTheWalletAndIsSilentWhenUnwired(t *testing.T) {
	tx := &capture{}
	n := New(tx, "https://replichai.latere.ai/")

	if err := n.AccountFrozen(ctx, "debtor@x.com"); err != nil {
		t.Fatalf("AccountFrozen: %v", err)
	}
	if tx.msg.To != "debtor@x.com" {
		t.Errorf("to = %q", tx.msg.To)
	}
	if !strings.Contains(tx.msg.Subject, "on hold") {
		t.Errorf("subject = %q, want it to say the account is on hold", tx.msg.Subject)
	}
	if !strings.Contains(tx.msg.HTML, "https://replichai.latere.ai/wallet") {
		t.Errorf("body = %q, want a link to the wallet", tx.msg.HTML)
	}
	// The mail comes from a no-reply address. The way to dispute the hold is
	// the billing contact, named in the body as an address to write to rather
	// than as a request to reply.
	for _, body := range []string{tx.msg.HTML, tx.msg.Text} {
		if strings.Contains(strings.ToLower(body), "reply to this") {
			t.Errorf("body = %q, asks for a reply", body)
		}
	}
	if !strings.Contains(tx.msg.HTML, `href="mailto:`+BillingContact+`"`) {
		t.Errorf("body = %q, want a mailto link to %s", tx.msg.HTML, BillingContact)
	}

	// A nil Notifier reports success and sends nothing.
	var nilN *Notifier
	if err := nilN.AccountFrozen(ctx, "debtor@x.com"); err != nil {
		t.Errorf("nil Notifier: %v", err)
	}
}

// The link must be one a browser can open. /projects/{id} is the gated JSON
// API, so an invitation pointing there shows the reader {"error":...}. That
// is what shipped in v0.4.1, which is why this test asserts the path rather
// than only that some URL is present.
func TestProjectInvitationNamesEverybodyAndLinksToTheProject(t *testing.T) {
	tx := &capture{}
	n := New(tx, "https://replichai.latere.ai")

	if err := n.ProjectInvitation(ctx, "collab@x.com", "p-1", "Reproducing Study 1", "Ada Lovelace"); err != nil {
		t.Fatalf("ProjectInvitation: %v", err)
	}
	if tx.msg.To != "collab@x.com" {
		t.Errorf("to = %q", tx.msg.To)
	}
	if !strings.Contains(tx.msg.Subject, "Reproducing Study 1") {
		t.Errorf("subject = %q, want the project named", tx.msg.Subject)
	}
	for _, want := range []string{"Ada Lovelace", "Reproducing Study 1", "https://replichai.latere.ai/p/p-1"} {
		if !strings.Contains(tx.msg.HTML, want) {
			t.Errorf("body is missing %q: %s", want, tx.msg.HTML)
		}
	}
	// The API path is never used, because it answers a browser with JSON.
	if strings.Contains(tx.msg.HTML, "/projects/p-1") {
		t.Errorf("the invitation links at the gated API: %s", tx.msg.HTML)
	}
}

// A trailing slash on the configured base must not produce a double slash in
// the link people click, nor in the logo's address.
func TestProjectURLHasNoDoubleSlash(t *testing.T) {
	tx := &capture{}
	n := New(tx, "https://replichai.latere.ai/")
	_ = n.ProjectInvitation(ctx, "a@b.com", "p-1", "Proj", "Ada")
	if strings.Contains(tx.msg.HTML, "latere.ai//") || strings.Contains(tx.msg.Text, "latere.ai//") {
		t.Errorf("link is malformed: %s", tx.msg.HTML)
	}
	if !strings.Contains(tx.msg.HTML, "https://replichai.latere.ai/p/p-1") {
		t.Errorf("link missing from body: %s", tx.msg.HTML)
	}
}

// The membership already exists, so there is nothing to accept. The message
// must not imply otherwise, nor tell the reader to wait for an admin.
func TestInvitationPromisesNothingItCannotKeep(t *testing.T) {
	tx := &capture{}
	n := New(tx, "https://replichai.latere.ai")
	_ = n.ProjectInvitation(ctx, "a@b.com", "p-1", "Proj", "Ada")

	body := strings.ToLower(tx.msg.HTML)
	for _, forbidden := range []string{"accept invitation", "platform invitation from an admin", "wait for"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("body says %q, which is not true of a project invitation: %s", forbidden, tx.msg.HTML)
		}
	}
	if !strings.Contains(body, "nothing to accept") {
		t.Errorf("body should say the access already exists: %s", tx.msg.HTML)
	}
	// It must say how a session is funded, since budget is what actually
	// limits taking part (006q).
	if !strings.Contains(body, "budget") {
		t.Errorf("body should explain that running a session needs budget: %s", tx.msg.HTML)
	}
}

// A project name or an inviter name comes out of a database and must never
// become markup in somebody's mail client.
func TestInvitationEscapesUntrustedInput(t *testing.T) {
	tx := &capture{}
	n := New(tx, "https://replichai.latere.ai")
	_ = n.ProjectInvitation(ctx, "a@b.com", "p-1",
		`<script>alert(1)</script>`, `<img src=x onerror=alert(2)>`)

	// The layout's own logo is an <img>, so the check is for the payload.
	for _, raw := range []string{"<script>", "<img src=x"} {
		if strings.Contains(tx.msg.HTML, raw) {
			t.Errorf("body carries unescaped %q: %s", raw, tx.msg.HTML)
		}
	}
	for _, escaped := range []string{"&lt;script&gt;", "&lt;img src=x"} {
		if !strings.Contains(tx.msg.HTML, escaped) {
			t.Errorf("body is missing the escaped form %q: %s", escaped, tx.msg.HTML)
		}
	}
}

// A project id is generated by the platform, but the link is still
// interpolated into an href, so it is escaped like every other value.
func TestProjectLinkIsEscaped(t *testing.T) {
	tx := &capture{}
	n := New(tx, "https://replichai.latere.ai")
	_ = n.ProjectInvitation(ctx, "a@b.com", `p-1"onmouseover="alert(1)`, "Proj", "Ada")
	if strings.Contains(tx.msg.HTML, `"onmouseover="`) {
		t.Errorf("href allows an attribute breakout: %s", tx.msg.HTML)
	}
}

// An unnamed project and a nameless inviter still produce a readable message
// rather than empty emphasis.
func TestInvitationFallsBackOnMissingNames(t *testing.T) {
	tx := &capture{}
	n := New(tx, "https://replichai.latere.ai")
	_ = n.ProjectInvitation(ctx, "a@b.com", "p-1", "   ", "")

	if strings.Contains(tx.msg.HTML, `"></strong>`) {
		t.Errorf("body renders empty emphasis: %s", tx.msg.HTML)
	}
	if !strings.Contains(tx.msg.HTML, "A colleague") || !strings.Contains(tx.msg.HTML, "a replication project") {
		t.Errorf("body = %s, want readable fallbacks", tx.msg.HTML)
	}
	if tx.msg.Subject != "You've been added to a replication project on ReplicHAI" {
		t.Errorf("subject = %q", tx.msg.Subject)
	}
	// Each fallback is worded for where it sits: the project is not called
	// "the project a replication project", and the inviter is lowercase
	// mid-sentence.
	for _, want := range []string{
		"You've been added to a replication project\n",
		"A colleague added you to a replication project on ReplicHAI.",
		"You received this because a colleague added a@b.com to a ReplicHAI project.",
	} {
		if !strings.Contains(tx.msg.Text, want) {
			t.Errorf("text part lacks %q:\n%s", want, tx.msg.Text)
		}
	}
}

// The text part is what a client that does not show HTML displays, so it
// carries the whole invitation: the title, the sentence naming who added the
// reader, the link, the funding note and why the mail came.
func TestProjectInvitationTextPart(t *testing.T) {
	tx := &capture{}
	n := New(tx, "https://replichai.latere.ai")
	if err := n.ProjectInvitation(ctx, "collab@x.com", "p-1", "Reproducing Study 1", "Ada Lovelace"); err != nil {
		t.Fatalf("ProjectInvitation: %v", err)
	}
	want := "You've been added to Reproducing Study 1\n\n" +
		"Ada Lovelace added you to the project Reproducing Study 1 on ReplicHAI. " +
		"You already have access, so there's nothing to accept.\n\n" +
		"Open the project: https://replichai.latere.ai/p/p-1\n\n" +
		"Replication sessions are paid from the project's budget, which the project owner funds. " +
		"You don't pay anything yourself.\n\n" +
		"--\nYou received this because Ada Lovelace added collab@x.com to a ReplicHAI project. " +
		"If you weren't expecting it, you can ignore this email.\n" + footer
	if tx.msg.Text != want {
		t.Errorf("text part:\n%s\nwant:\n%s", tx.msg.Text, want)
	}
	if tx.msg.Subject != "You've been added to Reproducing Study 1 on ReplicHAI" {
		t.Errorf("subject = %q", tx.msg.Subject)
	}
	if !strings.Contains(tx.msg.HTML, "You already have access. Nothing to accept.") {
		t.Errorf("HTML lacks the preheader: %s", tx.msg.HTML)
	}
}

// The hold notice's text part says what the reader can and cannot do, links
// to the wallet, and names the billing contact.
func TestAccountFrozenTextPart(t *testing.T) {
	tx := &capture{}
	n := New(tx, "https://replichai.latere.ai")
	if err := n.AccountFrozen(ctx, "debtor@x.com"); err != nil {
		t.Fatalf("AccountFrozen: %v", err)
	}
	want := "Your account is on hold\n\n" +
		"A recent payment was refunded or charged back, so your balance is now negative " +
		"and your account is view-only.\n\n" +
		"You can: open your projects and download your work\n" +
		"You can't: start new sessions until the balance is settled\n\n" +
		"Topping up lifts the hold automatically.\n\n" +
		"Top up your account: https://replichai.latere.ai/wallet\n\n" +
		"If you think this is a mistake, write to " + BillingContact + " and we'll look into it.\n\n" +
		"--\nYou received this because a payment on the ReplicHAI account debtor@x.com " +
		"was refunded or charged back.\n" + footer
	if tx.msg.Text != want {
		t.Errorf("text part:\n%s\nwant:\n%s", tx.msg.Text, want)
	}
	if tx.msg.Subject != "Your ReplicHAI account is on hold" {
		t.Errorf("subject = %q", tx.msg.Subject)
	}
}

// A reply to the hold notice is most likely a dispute, so it goes to billing.
// The invitation sets no Reply-To: there is nobody to answer.
func TestOnlyTheHoldNoticeRepliesToBilling(t *testing.T) {
	tx := &capture{}
	n := New(tx, "https://replichai.latere.ai")
	if err := n.AccountFrozen(ctx, "debtor@x.com"); err != nil {
		t.Fatalf("AccountFrozen: %v", err)
	}
	if tx.msg.ReplyTo != BillingContact {
		t.Errorf("hold notice Reply-To = %q, want %q", tx.msg.ReplyTo, BillingContact)
	}
	if err := n.ProjectInvitation(ctx, "a@b.com", "p-1", "Proj", "Ada"); err != nil {
		t.Fatalf("ProjectInvitation: %v", err)
	}
	if tx.msg.ReplyTo != "" {
		t.Errorf("invitation Reply-To = %q, want none", tx.msg.ReplyTo)
	}
}

// The logo is served by the app itself, so a mail client fetches it from the
// configured address: the ink lockup for a light background, the paper one
// for a dark background. A trailing slash on the address must not double the
// slash in either.
func TestLogoIsFetchedFromTheApp(t *testing.T) {
	for _, base := range []string{"https://replichai.example.org", "https://replichai.example.org/"} {
		tx := &capture{}
		n := New(tx, base)
		if err := n.AccountFrozen(ctx, "a@b.com"); err != nil {
			t.Fatalf("AccountFrozen: %v", err)
		}
		for _, want := range []string{
			`src="https://replichai.example.org/email/lockup-replichai-ink.png"`,
			`src="https://replichai.example.org/email/lockup-replichai-paper.png"`,
			`alt="Latere ReplicHAI"`,
			`width="214" height="50"`,
		} {
			if !strings.Contains(tx.msg.HTML, want) {
				t.Errorf("base %q: HTML lacks %s", base, want)
			}
		}
	}
}

// With no base address there is nowhere to fetch the logo from, so the
// message has none rather than an image a mail client cannot load.
func TestNoLogoWithoutABaseAddress(t *testing.T) {
	tx := &capture{}
	n := New(tx, "")
	if err := n.ProjectInvitation(ctx, "a@b.com", "p-1", "Proj", "Ada"); err != nil {
		t.Fatalf("ProjectInvitation: %v", err)
	}
	if strings.Contains(tx.msg.HTML, "<img") {
		t.Errorf("HTML carries a logo with no address to load it from: %s", tx.msg.HTML)
	}
}

func TestTransportFailureIsReported(t *testing.T) {
	tx := &capture{err: errors.New("provider is down")}
	n := New(tx, "https://replichai.latere.ai")
	if err := n.ProjectInvitation(ctx, "a@b.com", "p-1", "Proj", "Ada"); err == nil {
		t.Error("a failed send must be reported so the caller can log it")
	}
}

// A deployment that wires no transport sends nothing and reports no error.
// The same holds for one that never built a Notifier at all.
func TestAnUnwiredNotifierIsSilent(t *testing.T) {
	if err := New(nil, "https://replichai.latere.ai").ProjectInvitation(ctx, "a@b.com", "p-1", "Proj", "Ada"); err != nil {
		t.Errorf("a nil transport should be silent, got %v", err)
	}
	var none *Notifier
	if err := none.ProjectInvitation(ctx, "a@b.com", "p-1", "Proj", "Ada"); err != nil {
		t.Errorf("a nil Notifier should be silent, got %v", err)
	}
	tx := &capture{}
	n := New(tx, "")
	_ = n.ProjectInvitation(ctx, "a@b.com", "p-1", "Proj", "Ada")
	if tx.calls != 1 {
		t.Errorf("a wired transport must be called even with no base URL, got %d call(s)", tx.calls)
	}
}

func TestLogFailureDoesNotPanic(t *testing.T) {
	LogFailure("project invitation", "a@b.com", errors.New("boom"))
}

// The logos the layout links are the frontend's own files, at twice the size
// the layout shows them, so a new image cannot drift from its stated size.
func TestLogoFilesMatchTheirSize(t *testing.T) {
	l := newLayout("https://replichai.latere.ai")
	for _, img := range []email.Image{l.Logo, l.LogoDark} {
		path := filepath.Join("..", "..", "frontend", "public", strings.TrimPrefix(img.Src, "https://replichai.latere.ai/"))
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s is not a file of the frontend: %v", path, err)
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
		if err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if format != "png" || cfg.Width != 2*img.Width || cfg.Height != 2*img.Height {
			t.Errorf("%s is a %dx%d %s, want a %dx%d png", path, cfg.Width, cfg.Height, format, 2*img.Width, 2*img.Height)
		}
	}
}
