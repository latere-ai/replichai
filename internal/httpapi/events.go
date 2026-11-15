// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/project"
)

// streamEndEvent is the name of the last frame a stream sends when it ends for a
// reason other than the session finishing. Its data is {"reason": r}, where r is
// streamGone when the session is no longer readable by this viewer (deleted,
// moved out of reach, or the viewer lost access) and streamUnavailable when the
// server could not read it. A viewer that sees it closes the stream; on
// streamUnavailable it may reconnect.
const streamEndEvent = "end"

// The reasons a stream ends.
const (
	streamGone        = "not_found"
	streamUnavailable = "unavailable"
)

// streamRecheckEvery is how often an open stream checks again that its viewer
// may still read the session. Access is decided when the stream opens, and a
// member removed or a project withdrawn afterwards would otherwise keep
// receiving frames until the session ended.
const streamRecheckEvery = 30 * time.Second

// stillViewable re-runs the access decision of viewableSession for a stream
// that is already open. ok is false when the viewer may no longer read the
// session. A read that fails for another reason is returned, so the caller can
// leave the stream open and look again later.
func (rt *Router) stillViewable(r *http.Request, id string) (ok bool, err error) {
	j, err := rt.jobs.Get(r.Context(), id)
	if errors.Is(err, job.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	p, err := rt.projects.Get(r.Context(), j.ProjectID)
	if errors.Is(err, project.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return rt.mayViewProject(r, p), nil
}

// jobEvents streams a job's progress as Server-Sent Events. It sends a snapshot
// of the current state first, then each subsequent change, so a viewer who
// connects late sees where the run is rather than nothing until the next
// transition. The stream reads the job's own state and does not depend on
// anyone watching: a slow or disconnected client ends its own goroutine and
// never stalls the worker, which writes to the row regardless.
func (rt *Router) jobEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_unsupported")
		return
	}
	id := r.PathValue("id")
	j, ok := rt.viewableSession(w, r, id)
	if !ok {
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Event ids continue from Last-Event-ID so a reconnect resumes the sequence.
	eventID := lastEventID(r)

	// send reports whether the client is still connected. A write error means
	// the connection is gone; the stream ends then rather than on the next tick,
	// so a disconnected viewer stops costing a database read per interval.
	send := func(j job.Job) bool {
		eventID++
		if _, err := fmt.Fprintf(w, "id: %d\nevent: status\ndata: %s\n\n", eventID, mustJSON(j)); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	// end sends the final frame, so the browser can tell a stream that was cut
	// off from one that was closed on purpose.
	end := func(reason string) {
		eventID++
		if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: {\"reason\":%q}\n\n", eventID, streamEndEvent, reason); err != nil {
			return
		}
		flusher.Flush()
	}

	// The snapshot is sent first: the current state, whatever phase the run is in.
	if !send(j) {
		return
	}
	if j.Phase.Terminal() {
		return
	}
	lastUpdate := j.UpdatedAt

	ticker := time.NewTicker(rt.sseInterval)
	defer ticker.Stop()
	idle := time.NewTimer(rt.sseKeepAlive)
	defer idle.Stop()
	recheck := time.NewTicker(rt.sseRecheck)
	defer recheck.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-recheck.C:
			ok, err := rt.stillViewable(r, id)
			if err != nil {
				if r.Context().Err() == nil {
					slog.WarnContext(r.Context(), "events: checking access to an open stream", "session", id, "error", err)
				}
				continue
			}
			if !ok {
				end(streamGone)
				return
			}
		case <-idle.C:
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
			idle.Reset(rt.sseKeepAlive)
		case <-ticker.C:
			next, err := rt.jobs.Get(r.Context(), id)
			if errors.Is(err, job.ErrNotFound) {
				end(streamGone)
				return
			}
			if err != nil {
				if r.Context().Err() == nil {
					end(streamUnavailable)
				}
				return
			}
			if next.UpdatedAt.After(lastUpdate) {
				if !send(next) {
					return
				}
				lastUpdate = next.UpdatedAt
				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				idle.Reset(rt.sseKeepAlive)
			}
			if next.Phase.Terminal() {
				return
			}
		}
	}
}

// lastEventID reads the SSE Last-Event-ID header, defaulting to 0.
func lastEventID(r *http.Request) int {
	n, err := strconv.Atoi(r.Header.Get("Last-Event-ID"))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
