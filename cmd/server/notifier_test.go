// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"
)

// TestBuildNotifierWithoutMailConfigStillWorks checks that an unconfigured
// deployment gets a Notifier that logs. It must not be nil and must not be an
// error: a server that refused to start because no mail credential was set
// would make outbound mail a deployment requirement, which it is not (006p).
func TestBuildNotifierWithoutMailConfigStillWorks(t *testing.T) {
	for _, key := range []string{"", "test"} {
		t.Setenv("MAILGUN_API_KEY", key)
		t.Setenv("MAILGUN_DOMAIN", "")
		t.Setenv("SMTP_HOST", "")

		n, err := buildNotifier("https://replichai.latere.ai")
		if err != nil {
			t.Fatalf("buildNotifier with MAILGUN_API_KEY=%q: %v", key, err)
		}
		if n == nil {
			t.Fatal("buildNotifier returned nil; the members route would send nothing and say nothing")
		}
	}
}

// TestBuildNotifierRefusesAKeyWithNoDomain checks that a key with no domain is
// reported as a misconfiguration at startup rather than at the first
// invitation. The one message that reaches somebody who is not present is the
// worst one on which to discover a broken transport.
func TestBuildNotifierRefusesAKeyWithNoDomain(t *testing.T) {
	t.Setenv("MAILGUN_API_KEY", "a-real-looking-key")
	t.Setenv("MAILGUN_DOMAIN", "")
	t.Setenv("SMTP_HOST", "")

	_, err := buildNotifier("https://replichai.latere.ai")
	if err == nil {
		t.Fatal("expected a startup error for an API key with no domain")
	}
	if !strings.Contains(err.Error(), "mail") {
		t.Errorf("error = %v, want it to name mail as the failing subsystem", err)
	}
}

// TestBuildNotifierWithMailgun checks that a fully configured transport builds.
func TestBuildNotifierWithMailgun(t *testing.T) {
	t.Setenv("MAILGUN_API_KEY", "a-real-looking-key")
	t.Setenv("MAILGUN_DOMAIN", "mg.latere.ai")
	t.Setenv("MAILGUN_REGION", "eu")
	t.Setenv("MAILGUN_FROM", "Latere <noreply@latere.ai>")
	t.Setenv("SMTP_HOST", "")

	n, err := buildNotifier("https://replichai.latere.ai")
	if err != nil || n == nil {
		t.Fatalf("buildNotifier = %v, %v", n, err)
	}
}

// TestBuildNotifierRefusesABadFrom checks that a from address that does not
// parse stops the start, as a key with no domain does.
func TestBuildNotifierRefusesABadFrom(t *testing.T) {
	t.Setenv("MAILGUN_API_KEY", "a-real-looking-key")
	t.Setenv("MAILGUN_DOMAIN", "mg.latere.ai")
	t.Setenv("MAILGUN_REGION", "eu")
	t.Setenv("MAILGUN_FROM", "not an address")
	t.Setenv("SMTP_HOST", "")

	if _, err := buildNotifier("https://replichai.latere.ai"); err == nil {
		t.Fatal("expected a startup error for a from address that does not parse")
	}
}

// TestBuildNotifierWithSMTP checks that SMTP is the fallback when there is no
// Mailgun key.
func TestBuildNotifierWithSMTP(t *testing.T) {
	t.Setenv("MAILGUN_API_KEY", "")
	t.Setenv("MAILGUN_DOMAIN", "")
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "587")
	t.Setenv("SMTP_USERNAME", "u")
	t.Setenv("SMTP_PASSWORD", "p")

	n, err := buildNotifier("https://replichai.latere.ai")
	if err != nil || n == nil {
		t.Fatalf("buildNotifier = %v, %v", n, err)
	}
}
