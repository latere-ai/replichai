// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/latere-ai/replichai/internal/job"
)

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
	for {
		select {
		case <-r.Context().Done():
			return
		case <-idle.C:
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
			idle.Reset(rt.sseKeepAlive)
		case <-ticker.C:
			next, err := rt.jobs.Get(r.Context(), id)
			if err != nil {
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
