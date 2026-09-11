// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package sandbox

// HostAllowedEnvironment is every variable a stage on the operator's own
// machine may inherit from the process that launched it. It is a table with a
// test behind it rather than a comment, because the alternative — handing a
// stage the whole process environment — is how an exported ANTHROPIC_API_KEY,
// GITHUB_TOKEN or AWS_SECRET_ACCESS_KEY reaches an agent running with its
// permission prompts bypassed, and how an inherited vendor key silently
// overrides the harness login the operator meant to use.
//
// What is here is what a shell, a package manager and a harness need to run at
// all: where executables are (PATH), what locale and time zone to speak, where
// scratch goes, who the user is, and which trust store to verify TLS against.
// HOME and TERM are not listed because the driver sets them itself.
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
// constructed through a lookup over HostAllowedEnvironment rather than copied
// from a slice, so there is no way to hand a driver the raw process
// environment: a composition root passes os.LookupEnv, and only the named
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
