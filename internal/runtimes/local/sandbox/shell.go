// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package sandbox

import "strings"

// ShellQuote wraps a value in single quotes, escaping any it contains, so a
// shell interprets nothing inside it. It is the one place quoting is defined,
// shared by the bootstrap script the runtime builds and the wrapper the host
// driver runs, so a task or a model id is never handed to a shell unquoted.
func ShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// ShellJoin renders an argv as a single quoted command line, every element
// quoted, so nothing in it is interpreted by the shell.
func ShellJoin(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		parts = append(parts, ShellQuote(arg))
	}
	return strings.Join(parts, " ")
}
