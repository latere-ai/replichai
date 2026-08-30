// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"
)

// An unconfigured deployment gets a Notifier that logs. It must not be nil and
// it must not be an error: a server that refuses to start because nobody set a
// mail credential would make outbound mail a deployment blocker, which it is
// not (006p).
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

// A key with no domain is a misconfiguration, and it surfaces at startup rather
// than at the first invitation — the one message that reaches somebody who is
// not present is the worst one to discover a broken transport on.
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

// A fully configured transport builds.
func TestBuildNotifierWithMailgun(t *testing.T) {
	t.Setenv("MAILGUN_API_KEY", "a-real-looking-key")
	t.Setenv("MAILGUN_DOMAIN", "mg.latere.ai")
	t.Setenv("MAILGUN_REGION", "eu")
	t.Setenv("SMTP_HOST", "")

	n, err := buildNotifier("https://replichai.latere.ai")
	if err != nil || n == nil {
		t.Fatalf("buildNotifier = %v, %v", n, err)
	}
}

// SMTP is the fallback when there is no Mailgun key.
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
