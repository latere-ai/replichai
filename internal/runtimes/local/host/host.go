// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
)

// Config is the configuration a composition root passes to this driver.
type Config struct {
	// Platform is the operating system. It is a parameter rather than a call
	// to runtime.GOOS so that every refusal can be tested on one machine.
	Platform sandbox.Platform
	// Home is the operator's home directory. The whole directory is denied.
	Home string
	// HarnessBinary is the executable the stage runs. Preflight checks for
	// it so that a missing harness is reported with install instructions
	// rather than as an exec failure after the stage has started.
	HarnessBinary string
	// StopGrace is how long Stop waits after SIGTERM before it sends SIGKILL
	// to the stage's process group. Zero selects the default. A stage that
	// ignores SIGTERM is killed when the grace period elapses.
	StopGrace time.Duration
	// Look resolves an executable. It defaults to exec.LookPath.
	Look func(string) (string, error)
	// Lookup reads one variable of the process environment; the composition
	// root passes os.LookupEnv. A stage inherits only the variables that
	// sandbox.HostEnvironment admits through it, PATH above all. There is no
	// way to pass this driver the whole environment, because a credential
	// exported in the operator's shell must not reach an agent that runs
	// with its permission prompts bypassed.
	Lookup func(string) (string, bool)
}

// defaultStopGrace is how long Stop waits after SIGTERM before it sends
// SIGKILL to a stage that has not exited. It matches the container driver's
// stop timeout, so both drivers give a stage the same time to shut down
// cleanly.
const defaultStopGrace = 10 * time.Second

// Sandbox is the srt driver.
type Sandbox struct{ config Config }

// New composes the driver.
func New(config Config) *Sandbox {
	if config.Platform == "" {
		config.Platform = sandbox.CurrentPlatform()
	}
	if config.Look == nil {
		config.Look = exec.LookPath
	}
	if config.StopGrace <= 0 {
		config.StopGrace = defaultStopGrace
	}
	return &Sandbox{config: config}
}

// Name is the driver's canonical name.
func (s *Sandbox) Name() sandbox.DriverName { return sandbox.Host }

// dependencies returns the executables srt itself needs on a platform. macOS
// uses Seatbelt, which is part of the operating system; Linux needs bubblewrap
// and a socket relay.
func dependencies(platform sandbox.Platform) []string {
	switch platform {
	case sandbox.Linux:
		return []string{"bubblewrap", "socat", "rg"}
	default:
		return []string{"rg"}
	}
}

// Preflight reports whether this machine can run the driver, and names what
// to install when it cannot. Windows is refused because srt marks its Windows
// support as alpha, which is not an acceptable isolation boundary.
func (s *Sandbox) Preflight(context.Context) error {
	if s.config.Platform == sandbox.Windows {
		return sandbox.NotReady(s.config.Platform, sandbox.Host, sandbox.ConditionUnsupported, "srt")
	}
	if _, err := s.config.Look("srt"); err != nil {
		return sandbox.NotReady(s.config.Platform, sandbox.Host, sandbox.ConditionMissing, "srt")
	}
	var missing []string
	for _, dependency := range dependencies(s.config.Platform) {
		if _, err := s.config.Look(dependency); err != nil {
			missing = append(missing, dependency)
		}
	}
	if len(missing) > 0 {
		return sandbox.NotReady(s.config.Platform, sandbox.Host, sandbox.ConditionMissing, missing...)
	}
	if binary := strings.TrimSpace(s.config.HarnessBinary); binary != "" {
		if _, err := s.config.Look(binary); err != nil {
			return sandbox.NotReady(s.config.Platform, sandbox.Host, sandbox.ConditionMissing, binary)
		}
	}
	return nil
}

// CarriesHarnessLogin reports whether a stage under this driver can reach the
// credential the harness on this machine is already signed in with. It can: a
// stage runs on the operator's own filesystem, and the runtime grants read
// access to exactly the paths the harness names for its login.
func (s *Sandbox) CarriesHarnessLogin() bool { return true }

// Capabilities reports what this driver can enforce and install. It declares
// the allowlist network mode, which the container driver cannot, and omits
// apt, which needs a privileged root this driver does not have.
func (s *Sandbox) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{
		PackageManagers: map[string]bool{"pip": true, "npm": true},
		// There is no open mode. srt is allow-only and refuses a wildcard,
		// so this driver can neither enforce nor express unrestricted
		// egress. The capability declaration exists so that a driver never
		// declares a mode it cannot enforce.
		NetworkModes: map[string]bool{
			sandbox.NetworkNone: true, sandbox.NetworkAllowlist: true,
		},
		MaxAttachmentBytes: 64 << 20,
	}
}

// Launch starts one stage under srt, detached, with its output written to a
// file the stage itself cannot write. The wrapper writes the exit status to a
// second file, because the exit status of a reaped child is lost and this
// process may have exited before anyone asks for it.
func (s *Sandbox) Launch(ctx context.Context, spec sandbox.StageSpec) (sandbox.StageHandle, error) {
	if err := s.Preflight(ctx); err != nil {
		return sandbox.StageHandle{}, err
	}
	if len(spec.Argv) == 0 {
		return sandbox.StageHandle{}, errors.New("a stage needs a command")
	}
	// A stage with no PATH cannot run anything. It fails with exit code 127
	// and an empty log, which is the kind of unexplained failure this driver
	// must not produce. srt is itself a script whose shebang resolves node,
	// so an absolute command path is not enough.
	base := sandbox.HostEnvironment(s.config.Lookup)
	if !hasPath(base) {
		return sandbox.StageHandle{}, fmt.Errorf(
			"%w: the host sandbox was composed with no environment, so a stage would have no PATH",
			sandbox.ErrNotReady)
	}
	settingsPath := filepath.Join(filepath.Dir(spec.LogPath), spec.Name+".srt.json")
	body, err := Render(s.profile(spec)).Encode()
	if err != nil {
		return sandbox.StageHandle{}, err
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		return sandbox.StageHandle{}, err
	}
	if err := os.WriteFile(settingsPath, body, 0o600); err != nil {
		return sandbox.StageHandle{}, err
	}

	// The resolved path is used rather than the bare name. The wrapper runs
	// under a controlled PATH, and a bare name there makes a stage exit 127
	// with nothing in its log to explain why.
	srt, err := s.config.Look("srt")
	if err != nil {
		return sandbox.StageHandle{}, sandbox.NotReady(s.config.Platform, sandbox.Host, sandbox.ConditionMissing, "srt")
	}
	token := spec.Name
	statusPath := s.statusPath(spec.LogPath, spec.Name)
	_ = os.Remove(statusPath)
	_ = os.Remove(statusPath + ".part")
	// The "--" is required. srt parses options anywhere in its arguments
	// unless they are separated, so a harness argv containing -d, -s, -c, -V
	// or -h would have them consumed by srt: `srt --settings f echo -d -s -V`
	// fails with an srt settings error rather than running echo. Current
	// harnesses use long flags only, but that is not a property to rely on.
	command := append([]string{srt, "--settings", settingsPath, "--"}, spec.Argv...)
	// The status is written to a temporary file and renamed, so it appears
	// whole or not at all. Written in place, it could be observed empty, with
	// the process exited and the file present but not yet written, which
	// would read as a stage the driver lost.
	script := wrapperScript(token, command, statusPath)

	// The script is built from a shell-quoted argv. ShellJoin quotes every
	// element, so nothing in a task or a model id is interpreted by the shell.
	launcher := exec.Command("/bin/sh", "-c", script) //nolint:gosec,noctx // a quoted argv, and the stage outlives this process by design
	launcher.Dir = spec.Workdir
	launcher.Env = s.environment(base, spec)
	log, err := os.OpenFile(spec.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return sandbox.StageHandle{}, fmt.Errorf("open stage log: %w", err)
	}
	defer func() { _ = log.Close() }()
	// Stdin stays nil, which gives the stage the null device. A harness that
	// reads an inherited stdin waits on it; Claude Code stalls for three
	// seconds and reports this. A detached stage whose stdin is a pipe that
	// nobody closes waits forever.
	launcher.Stdout, launcher.Stderr = log, log
	launcher.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := launcher.Start(); err != nil {
		return sandbox.StageHandle{}, fmt.Errorf("start stage %s: %w", spec.Name, err)
	}
	pid := launcher.Process.Pid
	// The child is reaped in a goroutine rather than released. A released
	// child is never waited on, so it would remain a zombie for the life of
	// this process, and a long-running server would accumulate one per
	// stage. The wait only reaps; it sends no signal, so the stage stays
	// detached and outlives this process when it must, in which case it is
	// reparented to init and reaped there.
	go func() { _ = launcher.Wait() }()
	return sandbox.StageHandle{Driver: sandbox.Host, ID: fmt.Sprintf("pid:%d:%s:%s", pid, token, spec.LogPath)}, nil
}

// wrapperScript returns the shell script the driver runs: the stage's command
// followed by a record of its exit status.
//
// The status is written to a temporary file and renamed, so it appears whole
// or not at all. Written directly to the path it is read from, it could be
// observed empty, with the process exited and the file present but not yet
// written. That would read as a stage the driver lost and fail a session that
// finished.
func wrapperScript(token string, command []string, statusPath string) string {
	return fmt.Sprintf("# replichai-stage:%s\n%s\ncode=$?\nprintf %%s $code > %s\nmv -f %s %s\n",
		token, sandbox.ShellJoin(command),
		sandbox.ShellQuote(statusPath+".part"), sandbox.ShellQuote(statusPath+".part"), sandbox.ShellQuote(statusPath))
}

// hasPath reports whether the environment a stage inherits has a non-empty
// PATH, without which no command can be resolved.
func hasPath(environ []string) bool {
	for _, entry := range environ {
		if name, value, found := strings.Cut(entry, "="); found && name == "PATH" && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

// profile builds the complete access policy for one stage. Inside the
// operator's home directory it grants only what the StageSpec carries and
// adds nothing of its own. The harness's configuration reaches a run only when
// the runtime has put it in Paths, which it does only for a session that uses
// the operator's own login.
func (s *Sandbox) profile(spec sandbox.StageSpec) Profile {
	profile := Profile{Home: s.config.Home, Network: spec.Network}
	if binary := strings.TrimSpace(s.config.HarnessBinary); binary != "" {
		if resolved, err := s.config.Look(binary); err == nil {
			profile.Reads = append(profile.Reads, filepath.Dir(resolved))
			if real, err := filepath.EvalSymlinks(resolved); err == nil {
				profile.Reads = append(profile.Reads, filepath.Dir(real))
			}
		}
	}
	for _, path := range spec.Paths {
		profile.Reads = append(profile.Reads, path.Host)
		if path.Access == sandbox.ReadWrite {
			profile.Writes = append(profile.Writes, path.Host)
		}
	}
	// srt honours a denial that is more specific than the allowance it falls
	// inside. This is what allows one file to be withheld from a directory
	// that must be readable for another file.
	profile.Denies = append(profile.Denies, spec.Denied...)
	// The evidence directory is never in Paths, so it is already outside
	// every allowWrite entry. It is also denied explicitly, because a future
	// path that grants a writable parent would otherwise include it.
	if evidence := evidenceDir(spec.LogPath); evidence != "" {
		profile.DenyWrites = append(profile.DenyWrites, evidence)
	}
	return profile
}

// environment returns the environment the stage runs with: the allowlisted
// base, the HOME and TERM this driver sets, and the stage's own variables.
// Credentials named in EnvFile are passed through the launcher's environment
// rather than on a command line, so they never appear in the process table.
func (s *Sandbox) environment(base []string, spec sandbox.StageSpec) []string {
	environment := append([]string(nil), base...)
	environment = append(environment, "HOME="+s.config.Home, "TERM=dumb")
	for name, value := range spec.Env {
		environment = append(environment, name+"="+value)
	}
	return environment
}

// Observe reports whether the stage is alive. A stage is finished when its
// wrapper has written a status file. Otherwise it is running only if the pid
// is alive and still belongs to this stage. The command line carries the
// stage's marker, which makes a recycled pid detectable. Without the marker, a
// session could report a live run that is in fact another process.
func (s *Sandbox) Observe(_ context.Context, handle sandbox.StageHandle) (sandbox.StageStatus, error) {
	pid, token, logPath, err := parseHandle(handle)
	if err != nil {
		return sandbox.StageStatus{}, err
	}
	statusPath := s.statusPath(logPath, token)
	if code, ok := readStatus(statusPath); ok {
		return sandbox.StageStatus{ExitCode: code}, nil
	}
	if alive(pid) && belongsTo(pid, token) {
		return sandbox.StageStatus{Running: true}, nil
	}
	// The process is not observably alive and there is no status yet. The
	// wrapper records its status before it exits, so these two observations
	// race: the process can be gone before the rename that records its
	// status has completed, and ps can miss a process that is exiting. Wait
	// a bounded time rather than reporting a finished stage as lost, because
	// an exit read as gone fails a run that succeeded.
	for range 10 {
		time.Sleep(20 * time.Millisecond)
		if code, ok := readStatus(statusPath); ok {
			return sandbox.StageStatus{ExitCode: code}, nil
		}
		if alive(pid) && belongsTo(pid, token) {
			return sandbox.StageStatus{Running: true}, nil
		}
	}
	// No status and no process: the stage exited without recording its
	// status. That is not success, and an unknown exit must not be reported
	// as one.
	return sandbox.StageStatus{Gone: true}, nil
}

// Output streams the stage's log from an offset. This process writes the log
// into a directory no stage may write, so a stage cannot rewrite its own
// transcript.
func (s *Sandbox) Output(_ context.Context, handle sandbox.StageHandle, offset int64) (io.ReadCloser, error) {
	_, _, logPath, err := parseHandle(handle)
	if err != nil {
		return nil, err
	}
	//nolint:gosec // the path comes from this package's own stage handle
	file, err := os.Open(logPath)
	if os.IsNotExist(err) {
		// A stage that has written nothing yet yields an empty read rather
		// than an error, because the caller polls.
		return io.NopCloser(strings.NewReader("")), nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// Stop ends a running stage together with its whole process group. srt starts
// helper processes, and killing only the wrapper would leave the harness
// running.
//
// Stop sends SIGTERM, waits for the grace period, then sends SIGKILL. A
// well-behaved stage exits on SIGTERM and records its status. One that ignores
// it is killed when the grace period elapses. The kill goes to the group, not
// the leader alone: a child that outlived a leader killed by SIGTERM is still
// in the group and is killed with it, so nothing is left running when Stop
// returns.
func (s *Sandbox) Stop(ctx context.Context, handle sandbox.StageHandle) error {
	pid, token, logPath, err := parseHandle(handle)
	if err != nil {
		return err
	}
	statusPath := s.statusPath(logPath, token)
	if _, done := readStatus(statusPath); done {
		return nil
	}
	if !alive(pid) {
		return nil
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	// The status file, not the leader's liveness, is the authoritative signal
	// that the stage has finished, because a leader can exit on SIGTERM while
	// a child it started keeps running. The grace period therefore waits for
	// the status rather than for the pid, and if the status never appears
	// the whole group is killed.
	deadline := time.Now().Add(s.config.StopGrace)
	for time.Now().Before(deadline) {
		if _, done := readStatus(statusPath); done {
			return nil
		}
		select {
		case <-ctx.Done():
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	return nil
}

// Discard stops the stage. There is nothing else to remove: this driver
// creates no image, container or volume, and the session directory belongs to
// the runtime.
func (s *Sandbox) Discard(ctx context.Context, handle sandbox.StageHandle) error {
	return s.Stop(ctx, handle)
}

// Version reports the version of the harness on this machine, so that a run
// records what produced it. It runs outside the sandbox because it is this
// program's query about its own dependency, not the agent's work.
func (s *Sandbox) Version(ctx context.Context, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("no version command")
	}
	//nolint:gosec // the argv is the harness's own version command from its adapter
	output, err := exec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func (s *Sandbox) statusPath(logPath, name string) string {
	return filepath.Join(filepath.Dir(logPath), name+".status")
}

// evidenceDir is the session's evidence directory, derived from the stage log
// that lives inside it.
func evidenceDir(logPath string) string {
	raw := filepath.Dir(logPath)
	if filepath.Base(raw) != "raw" {
		return raw
	}
	return filepath.Dir(raw)
}

func parseHandle(handle sandbox.StageHandle) (int, string, string, error) {
	if err := sandbox.MatchHandle(sandbox.Host, handle); err != nil {
		return 0, "", "", err
	}
	parts := strings.SplitN(handle.ID, ":", 4)
	if len(parts) != 4 || parts[0] != "pid" {
		return 0, "", "", fmt.Errorf("%w: malformed host handle %q", sandbox.ErrNotReady, handle.ID)
	}
	pid, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, "", "", fmt.Errorf("%w: malformed host handle %q", sandbox.ErrNotReady, handle.ID)
	}
	return pid, parts[2], parts[3], nil
}

func readStatus(path string) (int, bool) {
	//nolint:gosec // the path comes from this package's own stage handle
	body, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil {
		return 0, false
	}
	return code, true
}

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// belongsTo reports whether the process running under this pid is the stage
// that was launched. The wrapper's command line carries the marker, so a pid
// the operating system has reassigned to another process fails the test.
func belongsTo(pid int, token string) bool {
	//nolint:gosec,noctx // a fixed command over an integer pid; liveness has no deadline to honour
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "args=").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(output), "replichai-stage:"+token)
}
