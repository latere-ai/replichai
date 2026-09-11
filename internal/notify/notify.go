// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package notify is what replichai says to somebody who is not looking at it.
//
// Everything else the platform does is a reply to a request: a person asks, the
// server answers, and the conversation ends. A project invitation is the one
// place the platform asserts something *about a person who is not present* —
// you have been given access to this work — and it needs a way out to them.
//
// Delivery is not this package's business. latere.ai/x/pkg/email picks a
// transport and refuses header injection; here lives the part that is
// replichai's own: what an invitation says, and the rule that failing to say it
// must never undo the membership that caused it.
package notify

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strings"
)

// Transport delivers one rendered message. It is the slice of
// latere.ai/x/pkg/email.Sender this package needs, named here so a test can
// substitute one without a network and without the shared package's selection
// logic.
type Transport interface {
	Send(ctx context.Context, to, subject, htmlBody string) error
}

// Notifier sends what the platform has to tell somebody. A nil *Notifier is
// usable and silent: a deployment that wires no transport still runs, and every
// caller is spared a nil check at the call site.
type Notifier struct {
	tx      Transport
	baseURL string
}

// New builds a Notifier over a transport. baseURL is where the app lives, and
// is what every link in a message is built from — a message with a link to
// localhost is worse than no message.
func New(tx Transport, baseURL string) *Notifier {
	return &Notifier{tx: tx, baseURL: strings.TrimRight(baseURL, "/")}
}

// ProjectInvitation tells someone they have been added to a project.
//
// It is an announcement, not an offer: the membership already exists, created
// by an owner who was authorised to create it, so there is nothing here to
// accept and no token to carry. What the message has to do is name who invited
// them and point at the work.
//
// The error is returned for logging, never for handling — see the doc on the
// route that calls this. A nil Notifier reports success, because a deployment
// with no mail configured has not failed at anything.
func (n *Notifier) ProjectInvitation(ctx context.Context, to, projectID, projectName, inviter string) error {
	if n == nil || n.tx == nil {
		return nil
	}
	subject, body := renderProjectInvitation(projectName, inviter, n.projectURL(projectID))
	return n.tx.Send(ctx, to, subject, body)
}

// AccountFrozen tells someone their account is in view-only mode after a
// clawed-back payment, and that a top-up lifts it (022d).
//
// A chargeback lands when nobody is looking at the app, so this email — not the
// in-app banner — is what turns a silent debt into an action. Like the
// invitation, the error is returned only for logging, and a nil Notifier is
// silently successful.
func (n *Notifier) AccountFrozen(ctx context.Context, to string) error {
	if n == nil || n.tx == nil {
		return nil
	}
	subject, body := renderAccountFrozen(n.walletURL())
	return n.tx.Send(ctx, to, subject, body)
}

// walletURL is where a reader tops up — the app's wallet screen, not an API path.
func (n *Notifier) walletURL() string {
	return n.baseURL + "/wallet"
}

// renderAccountFrozen builds the freeze notice. The only interpolated value is
// the app's own wallet URL, escaped here at the one place it is composed.
func renderAccountFrozen(walletURL string) (subject, htmlBody string) {
	subject = "Your replichai account is on hold"
	htmlBody = fmt.Sprintf(
		`<p>A recent payment was refunded or charged back, so your replichai `+
			`account balance is now negative and the account is in <strong>view-only</strong> mode: `+
			`you can still see your projects and download your work, but you cannot start `+
			`new sessions until the balance is settled.</p>`+
			`<p>Topping up clears the hold automatically:</p>`+
			`<p><a href="%s" style="display:inline-block;padding:12px 24px;background:#4F46E5;color:#fff;`+
			`text-decoration:none;border-radius:6px;font-weight:bold;">Top up your account</a></p>`+
			`<p>If you think this is a mistake, reply to this email and we will look into it.</p>`,
		html.EscapeString(walletURL),
	)
	return subject, htmlBody
}

// projectURL is where a project lives *for a reader of a message* — the app's
// deep link, not the API's.
//
// /projects/{id} is a gated JSON endpoint, so the first invitations linked
// somewhere a browser could only ever receive an error from. The app owns /p/,
// where a signed-out reader is sent through sign-in and returned to the project
// they were invited to.
func (n *Notifier) projectURL(projectID string) string {
	return n.baseURL + "/p/" + projectID
}

// renderProjectInvitation builds the subject and body. Everything interpolated
// is escaped here, at the one place it is composed, so no caller can be the
// reason a project name becomes markup in somebody's mail client.
//
// The inviter may be nameless — the identity provider does not always carry a
// display name — in which case the message says so rather than printing an
// empty strong tag.
func renderProjectInvitation(projectName, inviter, projectURL string) (subject, htmlBody string) {
	name := strings.TrimSpace(projectName)
	if name == "" {
		name = "a replication project"
	}
	who := strings.TrimSpace(inviter)
	if who == "" {
		who = "A colleague"
	}
	subject = fmt.Sprintf("You've been added to %s on replichai", name)
	htmlBody = fmt.Sprintf(
		`<p><strong>%s</strong> added you to <strong>%s</strong> on replichai.</p>`+
			`<p>You can open it here:</p>`+
			`<p><a href="%s" style="display:inline-block;padding:12px 24px;background:#4F46E5;color:#fff;`+
			`text-decoration:none;border-radius:6px;font-weight:bold;">Open the project</a></p>`+
			`<p>You already have access — there is nothing to accept. Running a replication `+
			`session spends budget, so if you have none yet, ask the project's owner to sponsor `+
			`sessions or a platform admin to grant you some.</p>`+
			`<p>If you were not expecting this, you can ignore this email.</p>`,
		html.EscapeString(who), html.EscapeString(name), html.EscapeString(projectURL),
	)
	return subject, htmlBody
}

// LogFailure records a message that could not be delivered. It is a function
// rather than an inline log line so every unsent notification reads the same
// way in the log, whatever sent it.
func LogFailure(kind, to string, err error) {
	slog.Warn("notify: message not sent", "kind", kind, "to", to, "error", err)
}
