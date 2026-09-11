// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package local

import (
	"fmt"
	"strings"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
)

// PackageManifest is the file in which the bootstrap records the package
// versions it resolved. It lives inside the language environment rather than
// the workspace, so it is not committed. The host reads it afterwards into the
// run's environment record, because the versions a run installed are a fact
// about the run and may differ from the versions it asked for.
const PackageManifest = "replichai-packages.txt"

// PackageIndexHosts are the index hosts pip is told to accept without
// verifying the certificate itself.
//
// Without this nothing installs. pip verifies certificates through the
// operating system's trust service, which the sandbox does not open, so every
// certificate fails whatever bundle pip is pointed at, while curl and Python's
// own client on the same connection succeed. Verification still happens, but
// outside pip: the sandbox terminates TLS to these hosts and validates the real
// index certificate itself, outside the sandbox, before handing pip a
// connection signed by the sandbox's own authority. pip skips checking only
// the local proxy it was given; the index certificate is still checked.
func PackageIndexHosts() []string {
	return []string{"pypi.org", "files.pythonhosted.org"}
}

// bootstrapArgv is the fixed script that prepares a session before any agent
// runs. It is built here rather than by a harness adapter because the packages
// a session needs come from the specification and no model is involved.
//
// Every declared package is installed into the session's own environment,
// which lives outside the workspace, so nothing an install writes can be
// committed by the agent and a `git add -A` cannot put an interpreter into the
// reproduction's history.
func (r *Runtime) bootstrapArgv(spec managed.SessionSpec, session *Session) []string {
	var script strings.Builder
	script.WriteString("set -eu\n")

	pip, npm := split(spec.Sandbox.Packages)
	if len(pip) > 0 {
		fmt.Fprintf(&script, "python3 -m venv %s\n", sandbox.ShellQuote(session.Venv()))
		trusted := make([]string, 0, len(PackageIndexHosts())*2)
		for _, host := range PackageIndexHosts() {
			trusted = append(trusted, "--trusted-host", host)
		}
		fmt.Fprintf(&script, "%s install --disable-pip-version-check --quiet %s %s\n",
			sandbox.ShellQuote(session.Venv()+"/bin/pip"), sandbox.ShellJoin(trusted), sandbox.ShellJoin(pip))
		fmt.Fprintf(&script, "%s freeze > %s\n",
			sandbox.ShellQuote(session.Venv()+"/bin/pip"), sandbox.ShellQuote(session.Venv()+"/"+PackageManifest))
	}
	if len(npm) > 0 {
		fmt.Fprintf(&script, "cd %s && npm install --silent --no-fund --no-audit %s\n",
			sandbox.ShellQuote(session.Venv()), sandbox.ShellJoin(npm))
	}
	// A specification's own bootstrap entries are argv arrays, never shell
	// fragments, so nothing in one is handed to a shell to interpret.
	for _, argv := range spec.Sandbox.Bootstrap {
		script.WriteString(sandbox.ShellJoin(argv) + "\n")
	}
	if script.Len() == len("set -eu\n") {
		script.WriteString("true\n")
	}
	return []string{"/bin/sh", "-c", script.String()}
}

// bootstrapPaths are the paths the bootstrap may write: its own environment,
// the temporary directory and the harness home. The workspace is excluded
// because nothing installed belongs in the reproduction, and the evidence
// directory is excluded as it is for every other stage.
func (r *Runtime) bootstrapPaths(session *Session) []sandbox.Path {
	return []sandbox.Path{
		{Host: session.Venv(), Guest: "/venv", Access: sandbox.ReadWrite},
		{Host: session.Tmp(), Guest: "/tmp-session", Access: sandbox.ReadWrite},
		{Host: session.Home(), Guest: "/home", Access: sandbox.ReadWrite},
	}
}

// split sorts declared packages by manager. A manager the driver did not
// declare never reaches here: ValidateCapabilities refused the specification
// before anything was created.
func split(packages []managed.Package) (pip, npm []string) {
	for _, pkg := range packages {
		name := pkg.Name
		if pkg.Version != "" {
			name += "==" + pkg.Version
		}
		switch pkg.Manager {
		case "pip":
			pip = append(pip, name)
		case "npm":
			if pkg.Version != "" {
				name = pkg.Name + "@" + pkg.Version
			}
			npm = append(npm, name)
		}
	}
	return pip, npm
}
