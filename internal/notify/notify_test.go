package notify

import (
	"context"
	"errors"
	"strings"
	"testing"
)

var ctx = context.Background()

// capture is a transport that keeps the last message instead of sending it.
type capture struct {
	to, subject, body string
	calls             int
	err               error
}

func (c *capture) Send(_ context.Context, to, subject, htmlBody string) error {
	c.calls++
	if c.err != nil {
		return c.err
	}
	c.to, c.subject, c.body = to, subject, htmlBody
	return nil
}

func TestProjectInvitationNamesEverybodyAndLinksToTheProject(t *testing.T) {
	tx := &capture{}
	n := New(tx, "https://replichai.latere.ai")

	if err := n.ProjectInvitation(ctx, "collab@x.com", "p-1", "Reproducing Study 1", "Ada Lovelace"); err != nil {
		t.Fatalf("ProjectInvitation: %v", err)
	}
	if tx.to != "collab@x.com" {
		t.Errorf("to = %q", tx.to)
	}
	if !strings.Contains(tx.subject, "Reproducing Study 1") {
		t.Errorf("subject = %q, want the project named", tx.subject)
	}
	for _, want := range []string{"Ada Lovelace", "Reproducing Study 1", "https://replichai.latere.ai/projects/p-1"} {
		if !strings.Contains(tx.body, want) {
			t.Errorf("body is missing %q: %s", want, tx.body)
		}
	}
}

// A trailing slash on the configured base must not produce a double slash in
// the link people click.
func TestProjectURLHasNoDoubleSlash(t *testing.T) {
	tx := &capture{}
	n := New(tx, "https://replichai.latere.ai/")
	_ = n.ProjectInvitation(ctx, "a@b.com", "p-1", "Proj", "Ada")
	if strings.Contains(tx.body, "latere.ai//projects") {
		t.Errorf("link is malformed: %s", tx.body)
	}
	if !strings.Contains(tx.body, "https://replichai.latere.ai/projects/p-1") {
		t.Errorf("link missing from body: %s", tx.body)
	}
}

// There is nothing to accept — the membership already exists — and the message
// must not imply otherwise, nor tell somebody to wait for an admin.
func TestInvitationPromisesNothingItCannotKeep(t *testing.T) {
	tx := &capture{}
	n := New(tx, "https://replichai.latere.ai")
	_ = n.ProjectInvitation(ctx, "a@b.com", "p-1", "Proj", "Ada")

	body := strings.ToLower(tx.body)
	for _, forbidden := range []string{"accept invitation", "platform invitation from an admin", "wait for"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("body says %q, which is not true of a project invitation: %s", forbidden, tx.body)
		}
	}
	if !strings.Contains(body, "nothing to accept") {
		t.Errorf("body should say the access already exists: %s", tx.body)
	}
	// It should say how to get budget, since that is the real wall (006q).
	if !strings.Contains(body, "budget") {
		t.Errorf("body should explain that running a session needs budget: %s", tx.body)
	}
}

// A project name or an inviter name comes out of a database and must never
// become markup in somebody's mail client.
func TestInvitationEscapesUntrustedInput(t *testing.T) {
	tx := &capture{}
	n := New(tx, "https://replichai.latere.ai")
	_ = n.ProjectInvitation(ctx, "a@b.com", "p-1",
		`<script>alert(1)</script>`, `<img src=x onerror=alert(2)>`)

	for _, raw := range []string{"<script>", "<img src"} {
		if strings.Contains(tx.body, raw) {
			t.Errorf("body carries unescaped %q: %s", raw, tx.body)
		}
	}
	for _, escaped := range []string{"&lt;script&gt;", "&lt;img"} {
		if !strings.Contains(tx.body, escaped) {
			t.Errorf("body is missing the escaped form %q: %s", escaped, tx.body)
		}
	}
}

// A project id is ours, but the link is still interpolated into an href, so it
// is escaped like everything else.
func TestProjectLinkIsEscaped(t *testing.T) {
	tx := &capture{}
	n := New(tx, "https://replichai.latere.ai")
	_ = n.ProjectInvitation(ctx, "a@b.com", `p-1"onmouseover="alert(1)`, "Proj", "Ada")
	if strings.Contains(tx.body, `"onmouseover="`) {
		t.Errorf("href allows an attribute breakout: %s", tx.body)
	}
}

// An unnamed project and a nameless inviter still produce a readable message
// rather than empty emphasis.
func TestInvitationFallsBackOnMissingNames(t *testing.T) {
	tx := &capture{}
	n := New(tx, "https://replichai.latere.ai")
	_ = n.ProjectInvitation(ctx, "a@b.com", "p-1", "   ", "")

	if strings.Contains(tx.body, "<strong></strong>") {
		t.Errorf("body renders empty emphasis: %s", tx.body)
	}
	if !strings.Contains(tx.body, "A colleague") || !strings.Contains(tx.body, "a replication project") {
		t.Errorf("body = %s, want readable fallbacks", tx.body)
	}
	if !strings.Contains(tx.subject, "a replication project") {
		t.Errorf("subject = %q", tx.subject)
	}
}

func TestTransportFailureIsReported(t *testing.T) {
	tx := &capture{err: errors.New("provider is down")}
	n := New(tx, "https://replichai.latere.ai")
	if err := n.ProjectInvitation(ctx, "a@b.com", "p-1", "Proj", "Ada"); err == nil {
		t.Error("a failed send must be reported so the caller can log it")
	}
}

// A deployment that wires no transport is silent, not broken — and neither is
// one that never built a Notifier at all.
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
