// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package doccheck tests that the documentation and the deploy manifests
// match the code and the release record.
//
// The package has no non-test code. Its tests parse the non-test Go under
// cmd/ and internal/ with go/parser and check that every environment
// variable the code reads is named in example.env, and that every subcommand
// the CLI dispatches is documented in docs/cli.md and docs/architecture.md. A
// variable or a command added in code without its documentation fails the
// test. They also check that no manifest or default names an image of ours by
// :latest, and that deploy/prod pins the newest release in CHANGELOG.md.
package doccheck
