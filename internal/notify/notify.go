// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package notify sends messages to people who are not using the app at the
// time.
//
// Everything else the platform does is a reply to a request. A project
// invitation tells a person who is not present that they have been given
// access to a project, and that message has to reach them another way.
//
// Delivery and markup are outside this package. latere.ai/x/pkg/email picks a
// transport, refuses header injection, and renders a message into the HTML
// and plain-text bodies every Latere service's mail shares. This package holds
// the parts specific to replichai: what each message says, the logo and footer
// it is framed with, and the rule that a failed send must never undo the
// membership that caused it.
package notify

import (
	"context"
	"log/slog"
	"strings"

	"latere.ai/x/pkg/email"
)

// BillingContact is where a reader writes about a payment or a hold. Mail is
// sent from a no-reply address, so a message about money names this address in
// its body and sets it as the Reply-To, where a reply reaches it instead of
// bouncing.
const BillingContact = "billing@latere.ai"

// The footer every message ends with: who runs the service and its legal
// pages. These are the hosted service's own; a deployment run by somebody
// else changes them here.
const copyright = "© 2026 Latere AI"

var legalLinks = []email.Span{
	email.Link("Privacy", "https://latere.ai/legal/privacy"),
	email.Link("Terms", "https://latere.ai/legal/terms"),
	email.Link("Impressum", "https://latere.ai/legal/impressum"),
}

// The logo above every message. The files live in the frontend's public
// directory, so the app serves them at these paths from its own address, and
// a mail client fetches them from there: Gmail shows neither SVG nor inline
// data images. Each PNG is twice the size it is shown at.
const (
	logoPath     = "/email/lockup-replichai-ink.png"
	logoDarkPath = "/email/lockup-replichai-paper.png"
	logoAlt      = "Latere ReplicHAI"
	logoWidth    = 214
	logoHeight   = 50
)

// Transport delivers one rendered message. It is the subset of
// latere.ai/x/pkg/email.Sender this package needs, declared here so a test can
// substitute one without a network and without the shared package's transport
// selection.
type Transport interface {
	Send(ctx context.Context, m email.Message) error
}

// Notifier sends the platform's messages. A nil *Notifier is usable and sends
// nothing: a deployment that wires no transport still runs, and callers need
// no nil check.
type Notifier struct {
	tx      Transport
	baseURL string
	layout  email.Layout
}

// New builds a Notifier over a transport. baseURL is the app's address, and
// every link in a message is built from it, as is the logo's address. A
// message linking to localhost is worse than no message.
func New(tx Transport, baseURL string) *Notifier {
	base := strings.TrimRight(baseURL, "/")
	return &Notifier{tx: tx, baseURL: base, layout: newLayout(base)}
}

// newLayout is the frame every message is rendered into. With no base address
// there is nowhere a mail client could fetch the logo from, so the message has
// none rather than a broken image.
func newLayout(base string) email.Layout {
	l := email.Layout{Copyright: copyright, Links: legalLinks}
	if base == "" {
		return l
	}
	l.Logo = email.Image{Src: base + logoPath, Alt: logoAlt, Width: logoWidth, Height: logoHeight}
	l.LogoDark = email.Image{Src: base + logoDarkPath, Alt: logoAlt, Width: logoWidth, Height: logoHeight}
	return l
}

// ProjectInvitation tells someone they have been added to a project.
//
// It is an announcement, not an offer. The membership already exists, created
// by an owner who was authorized to create it, so there is nothing to accept
// and no token to carry. The message names who added them and links to the
// project.
//
// The error is returned for logging only, never for handling; see the doc on
// the route that calls this. A nil Notifier returns nil, because a deployment
// with no mail configured has not failed.
func (n *Notifier) ProjectInvitation(ctx context.Context, to, projectID, projectName, inviter string) error {
	if n == nil || n.tx == nil {
		return nil
	}
	subject, htmlBody, textBody := renderProjectInvitation(n.layout, to, projectName, inviter, n.projectURL(projectID))
	return n.tx.Send(ctx, email.Message{To: to, Subject: subject, HTML: htmlBody, Text: textBody})
}

// AccountFrozen tells someone their account is in view-only mode after a
// refunded or charged-back payment, and that a top-up lifts the hold (022d).
//
// A chargeback arrives while nobody is using the app, so this email, rather
// than the in-app banner, is what brings the debt to the person's attention.
// A reply goes to BillingContact, since a reader who answers is most likely
// disputing the hold. As with the invitation, the error is returned only for
// logging, and a nil Notifier returns nil.
func (n *Notifier) AccountFrozen(ctx context.Context, to string) error {
	if n == nil || n.tx == nil {
		return nil
	}
	subject, htmlBody, textBody := renderAccountFrozen(n.layout, to, n.walletURL())
	return n.tx.Send(ctx, email.Message{To: to, Subject: subject, HTML: htmlBody, Text: textBody, ReplyTo: BillingContact})
}

// walletURL returns the app's wallet screen, where a reader tops up. It is
// not an API path.
func (n *Notifier) walletURL() string {
	return n.baseURL + "/wallet"
}

// renderAccountFrozen builds the freeze notice. The layout escapes every value
// it renders, so the recipient's address and the wallet URL cannot become
// markup.
func renderAccountFrozen(l email.Layout, to, walletURL string) (subject, htmlBody, textBody string) {
	subject = "Your ReplicHAI account is on hold"
	htmlBody, textBody = l.Render(email.Content{
		Title:     "Your account is on hold",
		Preheader: "A payment was refunded. Top up to lift the hold.",
		Blocks: []email.Block{
			email.Paragraph(email.Text("A recent payment was refunded or charged back, so your balance is now negative and your account is view-only.")),
			email.Facts(
				email.Fact{Label: "You can", Value: "open your projects and download your work"},
				email.Fact{Label: "You can't", Value: "start new sessions until the balance is settled"},
			),
			email.Paragraph(email.Text("Topping up lifts the hold automatically.")),
			email.Button("Top up your account", walletURL),
			email.Note(
				email.Text("If you think this is a mistake, write to "),
				email.Link(BillingContact, "mailto:"+BillingContact),
				email.Text(" and we'll look into it."),
			),
		},
		Reason: "You received this because a payment on the ReplicHAI account " + to + " was refunded or charged back.",
	})
	return subject, htmlBody, textBody
}

// projectURL returns the app's deep link to a project, for a reader of a
// message. It is not the API path.
//
// /projects/{id} is a gated JSON endpoint, so the first invitations linked to
// a page a browser could only receive an error from. The app serves /p/,
// where a signed-out reader is sent through sign-in and returned to the
// project they were invited to.
func (n *Notifier) projectURL(projectID string) string {
	return n.baseURL + "/p/" + projectID
}

// renderProjectInvitation builds the subject and both bodies. The layout
// escapes every value it renders, so no caller can cause a project name to
// become markup in a mail client.
//
// The project may have no name, and the inviter may have none either, because
// the identity provider does not always carry a display name. Each then reads
// as a generic phrase rather than an empty name, worded for where it sits in
// the sentence.
func renderProjectInvitation(l email.Layout, to, projectName, inviter, projectURL string) (subject, htmlBody, textBody string) {
	// An unnamed project reads "added you to a replication project", not
	// "added you to the project a replication project".
	project := strings.TrimSpace(projectName)
	added := []email.Span{email.Text(" added you to the project "), email.Strong(project)}
	if project == "" {
		project = "a replication project"
		added = []email.Span{email.Text(" added you to " + project)}
	}
	// The inviter opens the first sentence and sits mid-sentence in the
	// footer, so the fallback has a capitalized form and a lowercase one.
	inviter = strings.TrimSpace(inviter)
	who, whoInReason := email.Strong(inviter), inviter
	if inviter == "" {
		who, whoInReason = email.Text("A colleague"), "a colleague"
	}

	opening := append([]email.Span{who}, added...)
	opening = append(opening, email.Text(" on ReplicHAI. You already have access, so there's nothing to accept."))

	subject = "You've been added to " + project + " on ReplicHAI"
	htmlBody, textBody = l.Render(email.Content{
		Title:     "You've been added to " + project,
		Preheader: "You already have access. Nothing to accept.",
		Blocks: []email.Block{
			email.Paragraph(opening...),
			email.Button("Open the project", projectURL),
			email.Note(email.Text("Replication sessions are paid from the project's budget, which the project owner funds. You don't pay anything yourself.")),
		},
		Reason: "You received this because " + whoInReason + " added " + to + " to a ReplicHAI project. If you weren't expecting it, you can ignore this email.",
	})
	return subject, htmlBody, textBody
}

// LogFailure records a message that could not be delivered. It is a function
// rather than an inline log line so every unsent notification is logged in
// the same form, whichever caller sent it.
func LogFailure(kind, to string, err error) {
	slog.Warn("notify: message not sent", "kind", kind, "to", to, "error", err)
}
