// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package local

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"latere.ai/x/pkg/hostsandbox"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/runtimes/local/harness"
)

// The session directory's layout. workspace/ becomes the repository, tmp/ and
// venv/ hold what the agent needs but must not publish, and session/ holds the
// evidence, which the host writes and no stage may write.
const (
	workspaceDir  = "workspace"
	tmpDir        = "tmp"
	harnessTmpDir = "tmp/harness"
	venvDir       = "venv"
	gradeDir      = "grade"
	homeDir       = "home"
	evidenceDir   = "session"
	rawDir        = "session/raw"
)

// The files under session/.
const (
	eventsFile      = "session/events.jsonl"
	statusFile      = "session/status.json"
	locatorFile     = "session/locator.json"
	environmentFile = "session/environment.json"
	releasedFile    = "session/released.json"
	revisionFile    = "session/revision"
	specFile        = "session/spec.json"
)

// LocatorVersion is the shape of the recovery metadata a SessionRef carries.
const LocatorVersion = 1

// Locator is adapter-owned recovery metadata: enough to find a session's
// directory and its stages after the process that started them has gone.
// Nothing in it is a credential and nothing in it is user content.
// SessionRef.Validate rejects a locator key that contains one of the
// secret-bearing words, and these field names are chosen to pass that check
// rather than to be exempted from it.
type Locator struct {
	Version int                    `json:"version"`
	Dir     string                 `json:"dir"`
	Driver  hostsandbox.DriverName `json:"driver"`
	Harness harness.Name           `json:"harness"`
	Stages  []StageRecord          `json:"stages"`
}

// StageRecord is one stage's entry in the journal. Offset is how far the
// transcript pump has read, so a restart resumes rather than re-decoding, and
// Lines is the next event index, which is what keeps event ids stable.
type StageRecord struct {
	Role      harness.Stage           `json:"role"`
	Name      string                  `json:"name"`
	Handle    hostsandbox.StageHandle `json:"handle"`
	StartedAt time.Time               `json:"started_at"`
	Offset    int64                   `json:"offset"`
	Lines     int                     `json:"lines"`
	Exit      *int                    `json:"exit,omitempty"`
	Verdict   *managed.Verdict        `json:"verdict,omitempty"`
}

// Session is one run's directory on disk. Every durable fact about a local
// session lives here rather than in a goroutine, which is what makes a
// detached run recoverable by reference alone.
type Session struct {
	dir string
	id  string
}

// ErrSessionExists reports a session id that already has a directory. It is
// returned rather than ignored, because creating into an existing directory
// would re-initialize another session's workspace as if it were new.
var ErrSessionExists = errors.New("session already exists")

// CreateSession makes a fresh session directory under root, 0700, and the
// subdirectories a run needs. The workspace is initialized as a git repository
// with a committer already configured, so a run cannot stall on git refusing
// an empty user.email, and with no remote, because the host pushes (045g).
// The git that does it runs with the same allowlisted environment a stage
// gets, read through lookup, so a credential in the operator's shell reaches
// neither. An id whose directory already exists is refused with
// ErrSessionExists.
func CreateSession(root, id string, lookup LookupEnv) (*Session, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("a session needs an id")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create session root: %w", err)
	}
	dir := filepath.Join(root, id)
	// Mkdir rather than MkdirAll: Mkdir fails if the directory exists,
	// whereas MkdirAll would adopt whatever is already there.
	if err := os.Mkdir(dir, 0o700); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("%w: %s", ErrSessionExists, id)
		}
		return nil, fmt.Errorf("create session directory: %w", err)
	}
	// 0o700, not 0o600: it is a directory, and a directory without the
	// execute bit cannot be entered.
	//nolint:gosec // a private directory needs the execute bit
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("secure session directory: %w", err)
	}
	session := &Session{dir: dir, id: id}
	// tmp/harness is the temporary root a harness is pointed at, so that
	// whatever it derives from that root lands inside the session rather
	// than in the workspace.
	for _, child := range []string{workspaceDir, tmpDir, harnessTmpDir, venvDir, gradeDir, homeDir, evidenceDir, rawDir} {
		if err := os.MkdirAll(filepath.Join(dir, child), 0o700); err != nil {
			return nil, fmt.Errorf("create %s: %w", child, err)
		}
	}
	if err := initRepository(session.Workspace(), hostsandbox.Environment(lookup)); err != nil {
		return nil, err
	}
	return session, nil
}

// OpenSession attaches to an existing session directory. It is how a fresh
// process picks up a detached run: the locator names the directory, and the
// directory holds everything else.
func OpenSession(dir string) (*Session, error) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil, &managed.NotFoundError{Runtime: Name, ID: filepath.Base(dir)}
	}
	return &Session{dir: dir, id: filepath.Base(dir)}, nil
}

// ID is the session's identifier, which is also its directory's name.
func (s *Session) ID() string { return s.id }

// Dir is the session directory.
func (s *Session) Dir() string { return s.dir }

// Workspace is the agent's work, and the repository.
func (s *Session) Workspace() string { return workspacePath(filepath.Dir(s.dir), s.id) }

// workspacePath is where session id under root keeps its workspace. It is a
// pure function of the two so a workspace can be named before the session
// exists: Capabilities names it and Start creates it.
func workspacePath(root, id string) string { return filepath.Join(root, id, workspaceDir) }

// Tmp is where downloads go, deliberately outside the repository.
func (s *Session) Tmp() string { return filepath.Join(s.dir, tmpDir) }

// HarnessTmp is the temporary root a harness is pointed at. It sits inside the
// session's own temporary directory, so the directories a harness derives from
// it, such as the per-shell siblings Claude Code creates, are writable and are
// removed with the run.
//
// With the root outside the session, pip fell back to the working directory
// and filled a reproduction's repository with unpack caches.
func (s *Session) HarnessTmp() string { return filepath.Join(s.dir, harnessTmpDir) }

// Venv is the session's language environment, outside the repository so one
// `git add -A` cannot commit it.
func (s *Session) Venv() string { return filepath.Join(s.dir, venvDir) }

// Grade is where the judge writes, and the only path it may write.
func (s *Session) Grade() string { return filepath.Join(s.dir, gradeDir) }

// Home is the harness's session-scoped configuration directory.
func (s *Session) Home() string { return filepath.Join(s.dir, homeDir) }

// Evidence is the host-authored record. No stage may write it.
func (s *Session) Evidence() string { return filepath.Join(s.dir, evidenceDir) }

// RawLog is where one stage's own stream is written, byte for byte.
func (s *Session) RawLog(name string) string {
	return filepath.Join(s.dir, rawDir, name+".log")
}

// Paths is the set a harness builds a stage against.
func (s *Session) Paths() harness.Paths {
	return harness.Paths{
		Workspace:  s.Workspace(),
		Tmp:        s.Tmp(),
		HarnessTmp: s.HarnessTmp(),
		Home:       s.Home(),
		Evidence:   s.Workspace(),
		Grade:      s.Grade(),
	}
}

// WriteLocator persists the journal.
func (s *Session) WriteLocator(locator Locator) error {
	locator.Version, locator.Dir = LocatorVersion, s.dir
	return s.writeJSON(locatorFile, locator)
}

// ReadLocator reads the journal back.
func (s *Session) ReadLocator() (Locator, error) {
	var locator Locator
	err := s.readJSON(locatorFile, &locator)
	return locator, err
}

// WriteStatus persists the last observation, so a fresh process answers a poll
// from disk rather than from a runtime that no longer exists.
func (s *Session) WriteStatus(status managed.Status) error { return s.writeJSON(statusFile, status) }

// ReadStatus reads the last observation.
func (s *Session) ReadStatus() (managed.Status, error) {
	var status managed.Status
	err := s.readJSON(statusFile, &status)
	return status, err
}

// WriteEnvironment records what this run ran on. It is system-authored
// evidence, and it is what shows whether two reproductions that disagree
// differ about the paper or about the machine.
func (s *Session) WriteEnvironment(record Environment) error {
	return s.writeJSON(environmentFile, record)
}

// ReadEnvironment reads the environment record back.
func (s *Session) ReadEnvironment() (Environment, error) {
	var record Environment
	err := s.readJSON(environmentFile, &record)
	return record, err
}

// NextRevision is the session's own monotonic checkpoint counter.
//
// It belongs to the session rather than being derived from the reference a
// caller holds. The contract allows a caller to poll twice with the same
// reference. A revision computed from that reference would stay the same while
// the journal underneath it moved, and the controller would correctly treat
// that as a checkpoint that changed without a new revision.
func (s *Session) NextRevision() (uint64, error) {
	current := uint64(0)
	if body, err := os.ReadFile(filepath.Join(s.dir, revisionFile)); err == nil {
		if parsed, err := strconv.ParseUint(strings.TrimSpace(string(body)), 10, 64); err == nil {
			current = parsed
		}
	}
	current++
	if err := writeFileMode(filepath.Join(s.dir, revisionFile),
		[]byte(strconv.FormatUint(current, 10)), 0o600); err != nil {
		return 0, err
	}
	return current, nil
}

// maxEventBytes bounds one event's line on disk. An event's Content is already
// capped when the event is built, so the only field that can grow without
// bound is Raw, which keeps the whole stream line. A line past this is written
// with its Raw replaced by a marker naming how much was dropped; the whole
// value is still in the stage's raw log. Bounding at the write ensures the
// read never meets a line it cannot scan, which would truncate the transcript
// from that line on.
const maxEventBytes = 4 << 20

// AppendEvents adds decoded events to the transcript. The file is append-only
// and lives where no stage can write, so the subject of the audit cannot edit
// its own evidence.
func (s *Session) AppendEvents(events []managed.Event) error {
	if len(events) == 0 {
		return nil
	}
	path := filepath.Join(s.dir, eventsFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // this session's own transcript
	if err != nil {
		return fmt.Errorf("open transcript: %w", err)
	}
	defer func() { _ = file.Close() }()
	writer := bufio.NewWriter(file)
	for _, event := range events {
		line, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("encode event %s: %w", event.ID, err)
		}
		if len(line) > maxEventBytes {
			event.Raw = elidedRaw(len(event.Raw))
			line, err = json.Marshal(event)
			if err != nil {
				return fmt.Errorf("encode event %s: %w", event.ID, err)
			}
		}
		if _, err := writer.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return writer.Flush()
}

// elidedRaw is the marker that replaces an oversize event's Raw. It is a JSON
// string, because Raw must hold valid JSON, and it names the raw log where the
// whole value still is.
func elidedRaw(size int) json.RawMessage {
	encoded, err := json.Marshal(
		fmt.Sprintf("[%d bytes elided; the whole value is in this stage's raw log]", size))
	if err != nil {
		return json.RawMessage(`""`)
	}
	return encoded
}

// Events reads the transcript. A truncated final line, left by a process
// killed mid-write, does not parse and is dropped rather than failing the
// read, since the events that survived are still valid evidence. A scanner
// error is returned rather than ignored, because it means the transcript could
// not be read to the end and returning a prefix would present a partial
// transcript as the whole. Writes are bounded so no line this produces is too
// long to scan.
func (s *Session) Events() ([]managed.Event, error) {
	file, err := os.Open(filepath.Join(s.dir, eventsFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read transcript: %w", err)
	}
	defer func() { _ = file.Close() }()
	var events []managed.Event
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 8<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var event managed.Event
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return events, fmt.Errorf("read transcript: %w", err)
	}
	return events, nil
}

// Artifacts is everything the run produced, as clean relative paths under the
// workspace. Build residue and the repository's own metadata are left out:
// neither is a result, and .git would be an enormous and meaningless artifact.
func (s *Session) Artifacts() ([]managed.Artifact, error) {
	root := s.Workspace()
	var artifacts []managed.Artifact
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if relative == ".git" || strings.HasPrefix(relative, ".git/") || isVendoredDir(path, entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if relative == "." {
			return nil
		}
		// Only a regular file is an artifact. WalkDir reports a symlink as a
		// non-directory whatever it points at, so a link into an installed
		// dependency tree, such as the wheel cache uv unpacks as one, would
		// be listed and then read, and reading a link to a directory fails
		// the whole harvest. Following the link is not the answer either:
		// the target's bytes are not what the run produced.
		if !entry.Type().IsRegular() {
			return nil
		}
		artifact := managed.Artifact{ID: relative, Path: relative}
		if artifact.IsCompiled() {
			return nil
		}
		artifacts = append(artifacts, artifact)
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	slices.SortFunc(artifacts, func(a, b managed.Artifact) int { return strings.Compare(a.Path, b.Path) })
	return artifacts, nil
}

// Fetch reads one artifact, refusing any path that escapes the workspace.
func (s *Session) Fetch(artifact managed.Artifact) ([]byte, error) {
	target, err := containedPath(s.Workspace(), artifact.Path)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(target) //nolint:gosec // containedPath refused anything outside the workspace
	if os.IsNotExist(err) {
		return nil, &managed.NotFoundError{Runtime: Name, ID: artifact.Path}
	}
	return body, err
}

// Remove deletes the whole session directory. It is only ever reached through
// a retention policy that has already decided the work is safe to lose.
func (s *Session) Remove() error { return os.RemoveAll(s.dir) }

// containedPath resolves a relative artifact path inside root and refuses one
// that climbs out of it.
func containedPath(root, relative string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(relative))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: path %q escapes the workspace", managed.ErrInvalidSpec, relative)
	}
	return filepath.Join(root, clean), nil
}

func (s *Session) writeJSON(name string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeFileMode(filepath.Join(s.dir, name), append(body, '\n'), 0o600)
}

func (s *Session) readJSON(name string, into any) error {
	body, err := os.ReadFile(filepath.Join(s.dir, name)) //nolint:gosec // a fixed name under this session's own directory
	if os.IsNotExist(err) {
		return &managed.NotFoundError{Runtime: Name, ID: s.id}
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(body, into)
}

// initRepository makes the workspace a git repository with an identity already
// set and no remote. The identity is set so a run cannot stall on git refusing
// to commit; the absence of a remote is the containment argument of 045g.
// environ is the allowlisted host environment, never the process's own.
func initRepository(workspace string, environ []string) error {
	steps := [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.name", "replichai"},
		{"config", "user.email", "reproduction@replichai.invalid"},
		{"config", "commit.gpgsign", "false"},
	}
	for _, args := range steps {
		//nolint:gosec,noctx // a fixed git verb over this package's own arguments
		command := exec.Command("git", append([]string{"-C", workspace}, args...)...)
		command.Env = append(append([]string(nil), environ...), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if output, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
		}
	}
	return writeExcludes(workspace)
}

// vendoredDirs are the directory names that always hold installed
// dependencies. The agent builds in the same directory that becomes the
// repository, so an installed dependency tree of a few hundred megabytes sits
// in the repository root from the moment anything is installed. This list is
// what keeps such a tree both uncommitted and unharvested.
var vendoredDirs = []string{"venv", ".venv", "node_modules", "__pycache__"}

// isVendoredDir reports whether a directory holds installed dependencies
// rather than the reproduction. A virtualenv is recognized by the marker
// Python itself writes, so one under any name is caught; the other trees have
// no marker and are recognized by name.
func isVendoredDir(path, name string) bool {
	if slices.Contains(vendoredDirs, name) {
		return true
	}
	_, err := os.Stat(filepath.Join(path, "pyvenv.cfg"))
	return err == nil
}

// writeExcludes keeps those trees out of the repository by a rule the agent
// does not control. It writes .git/info/exclude rather than .gitignore,
// because .gitignore belongs to the reproduction and an agent that writes its
// own replaces whatever was seeded there. info/exclude belongs to the
// repository, is never committed, and survives anything the agent writes. git
// can only match by name, so a virtualenv under an unexpected name is caught
// instead by the harvest above and by publish.VerifyTree, which both inspect
// the tree's contents rather than its name.
func writeExcludes(workspace string) error {
	dir := filepath.Join(workspace, ".git", "info")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("git excludes: %w", err)
	}
	var body strings.Builder
	body.WriteString("# Written by replichai. A dependency tree installed into the\n" +
		"# workspace is not part of the reproduction.\n")
	for _, name := range vendoredDirs {
		body.WriteString(name + "/\n")
	}
	body.WriteString("*.pyc\n.DS_Store\n")
	return writeFileMode(filepath.Join(dir, "exclude"), []byte(body.String()), 0o600)
}
