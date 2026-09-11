// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package sandbox

import "strings"

// ShellQuote wraps a value in single quotes and escapes any single quotes it
// contains, so that a shell interprets nothing inside it. It is the only place
// quoting is defined. The bootstrap script the runtime builds and the wrapper
// the host driver runs both use it, so a task or a model id is never passed to
// a shell unquoted.
func ShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// ShellJoin renders an argv as a single command line with every element
// quoted, so that nothing in it is interpreted by the shell.
func ShellJoin(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		parts = append(parts, ShellQuote(arg))
	}
	return strings.Join(parts, " ")
}
