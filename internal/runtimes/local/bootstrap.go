package local

import (
	"fmt"
	"strings"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
)

// PackageManifest is where the bootstrap records what it actually resolved. It
// is inside the language environment rather than the workspace, so it is not
// committed, and the host reads it afterwards into the run's environment
// record: the versions a run got are a fact about the run, and asking for
// numpy is not the same as having numpy 2.1.0.
const PackageManifest = "replichai-packages.txt"

// PackageIndexHosts are the index hosts pip is told to accept without checking
// the certificate itself.
//
// It reads worse than it is, and the alternative is that nothing installs at
// all. pip verifies through the operating system's trust service, which the
// sandbox does not open — every certificate fails, whatever bundle pip is
// pointed at, while curl and Python's own client on the same connection
// succeed. So the verification moves rather than disappearing: the sandbox
// terminates TLS to these hosts, which means it validates the real index
// certificate itself, outside the sandbox, before handing pip a connection
// signed by its own authority. What pip skips is checking the local proxy it
// was given; what nobody skips is checking the index.
func PackageIndexHosts() []string {
	return []string{"pypi.org", "files.pythonhosted.org"}
}

// bootstrapArgv is the fixed script that prepares a session before any agent
// runs. It is built here rather than by a harness adapter: what a session
// needs installed is the specification's, and no model is involved.
//
// Every declared package is installed into the session's own environment,
// which lives outside the workspace — so nothing an install writes can be
// committed by the agent, and one `git add -A` cannot put an interpreter into
// the reproduction's history.
func (r *Runtime) bootstrapArgv(spec managed.SessionSpec, session *Session) []string {
	var script strings.Builder
	script.WriteString("set -eu\n")

	pip, npm := split(spec.Sandbox.Packages)
	if len(pip) > 0 {
		fmt.Fprintf(&script, "python3 -m venv %s\n", shellQuote(session.Venv()))
		trusted := make([]string, 0, len(PackageIndexHosts())*2)
		for _, host := range PackageIndexHosts() {
			trusted = append(trusted, "--trusted-host", host)
		}
		fmt.Fprintf(&script, "%s install --disable-pip-version-check --quiet %s %s\n",
			shellQuote(session.Venv()+"/bin/pip"), shellJoin(trusted), shellJoin(pip))
		fmt.Fprintf(&script, "%s freeze > %s\n",
			shellQuote(session.Venv()+"/bin/pip"), shellQuote(session.Venv()+"/"+PackageManifest))
	}
	if len(npm) > 0 {
		fmt.Fprintf(&script, "cd %s && npm install --silent --no-fund --no-audit %s\n",
			shellQuote(session.Venv()), shellJoin(npm))
	}
	// A specification's own bootstrap entries are argv arrays, never shell
	// fragments, so nothing in one is handed to a shell to interpret.
	for _, argv := range spec.Sandbox.Bootstrap {
		script.WriteString(shellJoin(argv) + "\n")
	}
	if script.Len() == len("set -eu\n") {
		script.WriteString("true\n")
	}
	return []string{"/bin/sh", "-c", script.String()}
}

// bootstrapPaths are what the bootstrap may touch: its own environment and the
// temporary directory. Not the workspace — nothing installed belongs in the
// reproduction — and not the evidence directory, like every other stage.
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

func shellJoin(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
