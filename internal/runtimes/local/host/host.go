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

	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
)

// Config is what a composition root hands this driver.
type Config struct {
	// Platform is the operating system, a parameter rather than a call to
	// runtime.GOOS so every refusal is testable on one machine.
	Platform sandbox.Platform
	// Home is the operator's home directory, denied wholesale.
	Home string
	// HarnessBinary is the executable the stage runs, checked by the
	// preflight so a missing harness is a refusal with instructions rather
	// than an exec failure a minute in.
	HarnessBinary string
	// ConfigDirs are the harness's own configuration directories, read-only
	// and the one allowance inside the home directory. This is the hole in
	// the boundary, and it is disclosed in the environment record.
	ConfigDirs []string
	// Look resolves an executable, defaulting to exec.LookPath.
	Look func(string) (string, error)
}

// Sandbox is the srt driver.
type Sandbox struct{ config Config }

// New composes the driver.
func New(config Config) *Sandbox {
	if config.Platform == "" {
		config.Platform = sandbox.CurrentPlatform()
	}
	if config.Home == "" {
		config.Home, _ = os.UserHomeDir()
	}
	if config.Look == nil {
		config.Look = exec.LookPath
	}
	return &Sandbox{config: config}
}

// Name is the driver's canonical name.
func (s *Sandbox) Name() sandbox.DriverName { return sandbox.Host }

// dependencies are what srt itself needs, per platform. macOS uses Seatbelt,
// which is in the operating system; Linux needs bubblewrap and a socket relay.
func dependencies(platform sandbox.Platform) []string {
	switch platform {
	case sandbox.Linux:
		return []string{"bubblewrap", "socat", "rg"}
	default:
		return []string{"rg"}
	}
}

// Preflight reports whether this machine can run the driver, and says what to
// install when it cannot. Windows is refused outright: srt marks it alpha, and
// a boundary that is alpha is not a boundary.
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

// Capabilities is what this driver can enforce and install. It declares the
// allowlist mode, which the container driver cannot, and refuses apt, which it
// has no privileged root to run.
func (s *Sandbox) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{
		PackageManagers: map[string]bool{"pip": true, "npm": true},
		NetworkModes: map[string]bool{
			sandbox.NetworkNone: true, sandbox.NetworkAllowlist: true, sandbox.NetworkOpen: true,
		},
		MaxAttachmentBytes: 64 << 20,
	}
}

// Launch starts one stage under srt, detached, with its output going to a file
// the stage itself has no write access to. The wrapper writes the exit status
// to a second file, because a reaped child tells nobody anything and this
// process may be gone before anyone asks.
func (s *Sandbox) Launch(ctx context.Context, spec sandbox.StageSpec) (sandbox.StageHandle, error) {
	if err := s.Preflight(ctx); err != nil {
		return sandbox.StageHandle{}, err
	}
	if len(spec.Argv) == 0 {
		return sandbox.StageHandle{}, errors.New("a stage needs a command")
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

	// The resolved path, not the bare name: the wrapper runs under a
	// controlled PATH, and a bare name there is how a stage exits 127 with
	// nothing in its log to say why.
	srt, err := s.config.Look("srt")
	if err != nil {
		return sandbox.StageHandle{}, sandbox.NotReady(s.config.Platform, sandbox.Host, sandbox.ConditionMissing, "srt")
	}
	token := spec.Name
	statusPath := s.statusPath(spec.LogPath, spec.Name)
	_ = os.Remove(statusPath)
	command := append([]string{srt, "--settings", settingsPath}, spec.Argv...)
	script := fmt.Sprintf("# replichai-stage:%s\n%s\nprintf %%s $? > %s\n",
		token, shellJoin(command), shellQuote(statusPath))

	launcher := exec.Command("/bin/sh", "-c", script)
	launcher.Dir = spec.Workdir
	launcher.Env = s.environment(spec)
	log, err := os.OpenFile(spec.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return sandbox.StageHandle{}, fmt.Errorf("open stage log: %w", err)
	}
	defer func() { _ = log.Close() }()
	launcher.Stdout, launcher.Stderr = log, log
	launcher.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := launcher.Start(); err != nil {
		return sandbox.StageHandle{}, fmt.Errorf("start stage %s: %w", spec.Name, err)
	}
	pid := launcher.Process.Pid
	// Released rather than waited on: the stage outlives this process, which
	// is what makes a detached run and a batch that survives a closed lid
	// work at all.
	_ = launcher.Process.Release()
	return sandbox.StageHandle{Driver: sandbox.Host, ID: fmt.Sprintf("pid:%d:%s:%s", pid, token, spec.LogPath)}, nil
}

// profile is the whole access decision for one stage.
func (s *Sandbox) profile(spec sandbox.StageSpec) Profile {
	profile := Profile{Home: s.config.Home, Network: spec.Network}
	profile.Reads = append(profile.Reads, s.config.ConfigDirs...)
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
	// The evidence directory is never in Paths, so it is already outside
	// every allowWrite entry. It is denied explicitly as well, because a
	// future path that grants a writable parent would otherwise carry it.
	if evidence := evidenceDir(spec.LogPath); evidence != "" {
		profile.DenyWrites = append(profile.DenyWrites, evidence)
	}
	return profile
}

// environment is what the stage runs with. Credentials named in EnvFile are
// passed through the environment of the launcher rather than on a command
// line, so they never appear in the process table.
func (s *Sandbox) environment(spec sandbox.StageSpec) []string {
	base := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + s.config.Home,
		"LANG=" + os.Getenv("LANG"),
		"TERM=dumb",
	}
	for name, value := range spec.Env {
		base = append(base, name+"="+value)
	}
	return base
}

// Observe reports whether the stage is alive. A stage is finished when its
// wrapper has written a status file; otherwise it is running only if the pid
// is alive and still belongs to this stage. The command line carries the
// stage's own marker, which is what makes a recycled pid detectable — without
// it a session would report a live run that is somebody else's process.
func (s *Sandbox) Observe(_ context.Context, handle sandbox.StageHandle) (sandbox.StageStatus, error) {
	pid, token, logPath, err := parseHandle(handle)
	if err != nil {
		return sandbox.StageStatus{}, err
	}
	if code, ok := readStatus(s.statusPath(logPath, token)); ok {
		return sandbox.StageStatus{ExitCode: code}, nil
	}
	if !alive(pid) || !belongsTo(pid, token) {
		// No status file and no process: the stage went away without
		// recording why. That is not success, and the runtime treats an
		// unknowable exit as a failure rather than reporting one.
		return sandbox.StageStatus{Gone: true}, nil
	}
	return sandbox.StageStatus{Running: true}, nil
}

// Output streams the stage's log from an offset. The log is written by this
// process, into a directory no stage may write, so a stage cannot rewrite its
// own transcript.
func (s *Sandbox) Output(_ context.Context, handle sandbox.StageHandle, offset int64) (io.ReadCloser, error) {
	_, _, logPath, err := parseHandle(handle)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(logPath)
	if os.IsNotExist(err) {
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

// Stop ends a running stage, and its whole process group with it: srt starts
// helpers, and killing only the wrapper would leave the harness running.
func (s *Sandbox) Stop(_ context.Context, handle sandbox.StageHandle) error {
	pid, token, logPath, err := parseHandle(handle)
	if err != nil {
		return err
	}
	if _, done := readStatus(s.statusPath(logPath, token)); done {
		return nil
	}
	if !alive(pid) {
		return nil
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	return nil
}

// Discard has nothing to remove: this driver creates no image, no container
// and no volume, and the session directory belongs to the runtime.
func (s *Sandbox) Discard(ctx context.Context, handle sandbox.StageHandle) error {
	return s.Stop(ctx, handle)
}

// Version reports what the harness on this machine actually is, so a run
// records what produced it. It runs outside the sandbox: it is this program's
// own question about its own dependency, not the agent's work.
func (s *Sandbox) Version(ctx context.Context, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("no version command")
	}
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

// belongsTo reports whether the process still running under this pid is the
// stage that was launched. The wrapper's own command line carries the marker,
// so a pid the operating system handed to somebody else fails the test.
func belongsTo(pid int, token string) bool {
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "args=").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(output), "replichai-stage:"+token)
}

// shellJoin renders an argv as a single quoted command line. Every element is
// quoted, so nothing in a task or a model id is ever interpreted by the shell.
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
