// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package doccheck tests that the documentation matches the code.
//
// The package has no non-test code. Its tests parse the non-test Go under
// cmd/ and internal/ with go/parser and check two things: every environment
// variable the code reads is named in example.env, and every subcommand the
// CLI dispatches is documented in README.md and docs/architecture.md. A
// variable or a command added in code without its documentation fails the
// test.
package doccheck
