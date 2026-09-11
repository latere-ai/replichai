// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package sandbox

// HostAllowedEnvironment lists every variable a stage on the operator's own
// machine may inherit from the process that launched it. It is a table checked
// by a test rather than a comment, because the alternative of passing a stage
// the whole process environment is how an exported ANTHROPIC_API_KEY,
// GITHUB_TOKEN or AWS_SECRET_ACCESS_KEY would reach an agent running with its
// permission prompts bypassed, and how an inherited vendor key would silently
// override the harness login the operator meant to use.
//
// The list holds what a shell, a package manager and a harness need in order
// to run at all: where executables are (PATH), the locale and time zone, where
// scratch files go, who the user is, and which trust store to verify TLS
// against. HOME and TERM are not listed because the driver sets them itself.
var HostAllowedEnvironment = []string{
	"PATH",
	"LANG", "LANGUAGE",
	"LC_ALL", "LC_COLLATE", "LC_CTYPE", "LC_MESSAGES", "LC_MONETARY", "LC_NUMERIC", "LC_TIME",
	"TZ",
	"TMPDIR",
	"USER", "LOGNAME",
	"SHELL",
	"SSL_CERT_FILE", "SSL_CERT_DIR",
}

// HostEnvironment builds the environment a host stage starts from. It is
// constructed by looking up each name in HostAllowedEnvironment rather than by
// copying a slice, so there is no way to pass a driver the raw process
// environment. A composition root passes os.LookupEnv, and only the listed
// variables come through. A nil lookup yields an empty environment, which the
// host driver refuses to launch a stage with.
func HostEnvironment(lookup func(string) (string, bool)) []string {
	if lookup == nil {
		return nil
	}
	environment := make([]string, 0, len(HostAllowedEnvironment))
	for _, name := range HostAllowedEnvironment {
		if value, ok := lookup(name); ok {
			environment = append(environment, name+"="+value)
		}
	}
	return environment
}
