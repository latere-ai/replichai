// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package notify sends messages to people who are not using the app at the
// time.
//
// Everything else the platform does is a reply to a request. A project
// invitation tells a person who is not present that they have been given
// access to a project, and that message has to reach them another way.
//
// Delivery is outside this package. latere.ai/x/pkg/email picks a transport
// and refuses header injection. This package holds the parts specific to
// replichai: what each message says, and the rule that a failed send must
// never undo the membership that caused it.
package notify

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strings"
)

// Transport delivers one rendered message. It is the subset of
// latere.ai/x/pkg/email.Sender this package needs, declared here so a test can
// substitute one without a network and without the shared package's transport
// selection.
type Transport interface {
	Send(ctx context.Context, to, subject, htmlBody string) error
}

// Notifier sends the platform's messages. A nil *Notifier is usable and sends
// nothing: a deployment that wires no transport still runs, and callers need
// no nil check.
type Notifier struct {
	tx      Transport
	baseURL string
}

// New builds a Notifier over a transport. baseURL is the app's address, and
// every link in a message is built from it. A message linking to localhost is
// worse than no message.
func New(tx Transport, baseURL string) *Notifier {
	return &Notifier{tx: tx, baseURL: strings.TrimRight(baseURL, "/")}
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
	subject, body := renderProjectInvitation(projectName, inviter, n.projectURL(projectID))
	return n.tx.Send(ctx, to, subject, body)
}

// AccountFrozen tells someone their account is in view-only mode after a
// refunded or charged-back payment, and that a top-up lifts the hold (022d).
//
// A chargeback arrives while nobody is using the app, so this email, rather
// than the in-app banner, is what brings the debt to the person's attention.
// As with the invitation, the error is returned only for logging, and a nil
// Notifier returns nil.
func (n *Notifier) AccountFrozen(ctx context.Context, to string) error {
	if n == nil || n.tx == nil {
		return nil
	}
	subject, body := renderAccountFrozen(n.walletURL())
	return n.tx.Send(ctx, to, subject, body)
}

// walletURL returns the app's wallet screen, where a reader tops up. It is
// not an API path.
func (n *Notifier) walletURL() string {
	return n.baseURL + "/wallet"
}

// renderAccountFrozen builds the freeze notice. The only interpolated value is
// the app's own wallet URL, escaped here at the one place it is composed.
func renderAccountFrozen(walletURL string) (subject, htmlBody string) {
	subject = "Your replichai account is on hold"
	htmlBody = fmt.Sprintf(
		`<p>A recent payment was refunded or charged back. Your replichai account `+
			`balance is now negative and the account is in <strong>view-only</strong> mode. `+
			`You can still see your projects and download your work, but you cannot start `+
			`new sessions until the balance is settled.</p>`+
			`<p>Topping up lifts the hold automatically:</p>`+
			`<p><a href="%s" style="display:inline-block;padding:12px 24px;background:#4F46E5;color:#fff;`+
			`text-decoration:none;border-radius:6px;font-weight:bold;">Top up your account</a></p>`+
			`<p>If you think this is a mistake, reply to this email and we will look into it.</p>`,
		html.EscapeString(walletURL),
	)
	return subject, htmlBody
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

// renderProjectInvitation builds the subject and body. Every interpolated
// value is escaped here, where the message is composed, so no caller can
// cause a project name to become markup in a mail client.
//
// The inviter may have no name, because the identity provider does not always
// carry a display name. In that case the message uses a generic phrase rather
// than an empty strong tag.
func renderProjectInvitation(projectName, inviter, projectURL string) (subject, htmlBody string) {
	name := strings.TrimSpace(projectName)
	if name == "" {
		name = "a replication project"
	}
	who := strings.TrimSpace(inviter)
	if who == "" {
		who = "A colleague"
	}
	subject = fmt.Sprintf("You have been added to %s on replichai", name)
	htmlBody = fmt.Sprintf(
		`<p><strong>%s</strong> added you to <strong>%s</strong> on replichai.</p>`+
			`<p>You can open it here:</p>`+
			`<p><a href="%s" style="display:inline-block;padding:12px 24px;background:#4F46E5;color:#fff;`+
			`text-decoration:none;border-radius:6px;font-weight:bold;">Open the project</a></p>`+
			`<p>You already have access, so there is nothing to accept. Replication sessions `+
			`are paid from the project's budget, which the project owner funds. You do not `+
			`need to pay anything yourself.</p>`+
			`<p>If you were not expecting this, you can ignore this email.</p>`,
		html.EscapeString(who), html.EscapeString(name), html.EscapeString(projectURL),
	)
	return subject, htmlBody
}

// LogFailure records a message that could not be delivered. It is a function
// rather than an inline log line so every unsent notification is logged in
// the same form, whichever caller sent it.
func LogFailure(kind, to string, err error) {
	slog.Warn("notify: message not sent", "kind", kind, "to", to, "error", err)
}
