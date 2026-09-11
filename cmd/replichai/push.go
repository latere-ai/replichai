// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/latere-ai/replichai/internal/batch"
	"github.com/latere-ai/replichai/internal/cloud"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/paper"
)

// runProject creates and lists the containers a locally-run session is filed
// under (045k).
func runProject(ctx context.Context, args []string, stdout, stderr *os.File) error {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: replichai project create <title> | replichai project ls")
		return errExitQuiet
	}
	client, err := apiClient()
	if err != nil {
		return err
	}
	switch args[0] {
	case "create":
		title := strings.TrimSpace(strings.Join(args[1:], " "))
		if title == "" {
			_, _ = fmt.Fprintln(stderr, "project create takes a title")
			return errExitQuiet
		}
		created, err := client.CreateProject(ctx, title)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stdout, "%s\t%s\n", created.ID, created.Title)
		return nil
	case "ls":
		projects, err := client.ListProjects(ctx)
		if err != nil {
			return err
		}
		for _, p := range projects {
			_, _ = fmt.Fprintf(stdout, "%s\t%s\n", p.ID, p.Title)
		}
		return nil
	case "sessions":
		if len(args) < 2 {
			_, _ = fmt.Fprintln(stderr, "usage: replichai project sessions <project-id>")
			return errExitQuiet
		}
		sessions, err := client.ListSessions(ctx, args[1])
		if err != nil {
			return err
		}
		for _, s := range sessions {
			_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", s.ID, s.Phase, s.Repo, s.Ref)
		}
		return nil
	case "rm-session":
		if len(args) < 2 {
			_, _ = fmt.Fprintln(stderr, "usage: replichai project rm-session <session-id>...")
			return errExitQuiet
		}
		// Each id is its own act: one refusal — a session still running, a
		// session somebody else owns — must not strand the rest, because the
		// reason to delete several at once is that a run went wrong in a way
		// that produced several.
		failed := 0
		for _, id := range args[1:] {
			if err := client.DeleteSession(ctx, id); err != nil {
				_, _ = fmt.Fprintf(stderr, "  ! %s: %v\n", id, err)
				failed++
				continue
			}
			_, _ = fmt.Fprintf(stdout, "%s\tdeleted\n", id)
		}
		if failed > 0 {
			return errExitQuiet
		}
		return nil
	default:
		_, _ = fmt.Fprintf(stderr, "unknown project command %q\n", args[0])
		return errExitQuiet
	}
}

// runPush files sessions this machine ran under a project in the app. The
// evidence itself is already in each reproduction's repository; what travels
// here is the record.
//
// A push failure never fails a run: the work is on disk and in the repository,
// and running push again finishes the job. That is why the whole command is
// resumable and idempotent rather than transactional.
func runPush(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := fs.String("project", "", "the project to file the sessions under")
	// The documented form puts the flag after the file, and flag stops at the
	// first argument that is not one. Parsing round the positionals is what
	// audit and run already do, and it is what makes the usage line true.
	var positional []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			return errExitQuiet
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(positional) != 1 {
		_, _ = fmt.Fprintln(stderr, "push takes one project file or batch state")
		return errExitQuiet
	}
	if strings.TrimSpace(*projectID) == "" {
		_, _ = fmt.Fprintln(stderr, "push needs --project; `replichai project ls` lists them")
		return errExitQuiet
	}
	statePath := positional[0]
	if filepath.Base(statePath) != "batch.json" {
		statePath = filepath.Join(batch.Root(statePath), "batch.json")
	}
	state, err := batch.LoadState(statePath)
	if err != nil {
		return fmt.Errorf("read %s: %w", statePath, err)
	}
	client, err := apiClient()
	if err != nil {
		return err
	}

	pushed, skipped, failed := 0, 0, 0
	for _, entry := range state.Entries {
		if !entry.Phase.Terminal() {
			skipped++
			continue
		}
		record, err := recordOf(entry)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "  ! %s: %v\n", entry.Paper, err)
			failed++
			continue
		}
		// The paper is filed first: a session may only be filed against a
		// paper the project holds, for the same reason it may only run one.
		if err := client.AddPaper(ctx, *projectID, entry.Paper); err != nil {
			_, _ = fmt.Fprintf(stderr, "  ! %s: %v\n", entry.Paper, err)
			failed++
			continue
		}
		id, err := client.ImportSession(ctx, *projectID, record)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "  ! %s: %v\n", entry.Paper, err)
			failed++
			continue
		}
		_, _ = fmt.Fprintf(stdout, "%s\t%s\n", id, entry.Paper)
		pushed++
	}
	_, _ = fmt.Fprintf(stderr, "\npushed %d, skipped %d still running, %d failed\n", pushed, skipped, failed)
	if failed > 0 {
		return errExitQuiet
	}
	return nil
}

// recordOf turns one batch entry into the record the app files. The paper key
// is the one the run already resolved where it has one, and is derived offline
// where it does not: a push must not depend on a paper source being reachable.
func recordOf(entry batch.EntryState) (cloud.ImportRecord, error) {
	key := entry.PaperKey
	if key == "" {
		derived, err := paper.Key(entry.Paper)
		if err != nil {
			return cloud.ImportRecord{}, fmt.Errorf("no paper identity: %w", err)
		}
		key = derived
	}
	session := entry.Session
	if session == "" {
		return cloud.ImportRecord{}, errors.New("the entry records no session id")
	}
	phase := job.PhaseDone
	if entry.Phase == batch.Failed {
		phase = job.PhaseFailed
	}
	return cloud.ImportRecord{
		PaperKey: key, Ref: entry.Paper,
		Runtime: runtimeOf(entry), SessionID: session,
		Phase: string(phase), Grade: entry.Grade,
		Model: cloud.ModelRef{Provider: entry.Model.Provider, ID: entry.Model.ID},
		Usage: cloud.UsageRecord{
			InputFresh: entry.Usage.InputFresh, InputCached: entry.Usage.InputCached,
			CacheWrite: entry.Usage.CacheWrite, CacheWrite5m: entry.Usage.CacheWrite5m,
			CacheWrite1h: entry.Usage.CacheWrite1h, Output: entry.Usage.Output,
		},
		Repo: entry.Repo, Error: entry.Error,
		StartedAt: entry.Started, FinishedAt: entry.Finished,
	}, nil
}

// runtimeOf is which runtime executed the session, from the reference the run
// recorded. An entry with no reference is attributed to the local runtime,
// which is what a batch state file describes.
func runtimeOf(entry batch.EntryState) string {
	if entry.Ref.Runtime != "" {
		return string(entry.Ref.Runtime)
	}
	return "local"
}

// apiClient builds the platform client over the saved credential, refusing
// with an actionable message where there is none.
func apiClient() (*cloud.Client, error) {
	session := cloud.New(homeDir(), envOr("REPLICHAI_AUTH_URL", cloud.DefaultAuthURL))
	if _, err := session.Store.Load(); err != nil {
		if errors.Is(err, cloud.ErrSignedOut) {
			return nil, errors.New("not signed in; run `replichai login`")
		}
		return nil, err
	}
	return cloud.NewClient(session, envOr("REPLICHAI_API_URL", cloud.DefaultAPIURL)), nil
}
