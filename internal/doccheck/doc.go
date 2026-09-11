// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package doccheck holds the documents to the code.
//
// Nothing here is compiled into a binary; the package exists so its tests
// have a name. They parse the non-test Go under cmd/ and internal/ with
// go/parser and assert two things a reader relies on: every environment
// variable the code reads is named in example.env, and every subcommand the
// CLI dispatches is documented in README.md and docs/architecture.md. A
// variable or a command added in code without its line of documentation is a
// red test rather than a surprise for whoever runs or deploys the software.
package doccheck
