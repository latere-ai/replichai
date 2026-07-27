package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/latere-ai/replichai/internal/job"
)

// jobEvents streams a job's progress as Server-Sent Events. It snapshots the
// current state first, then sends each subsequent change — so a viewer who
// connects late still sees where the run is, not a blank screen until the next
// transition. The run streams from the job's own state and does not depend on
// anyone watching: a slow or vanishing client drops its own goroutine and never
// stalls the worker, which writes to the row regardless.
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

	send := func(j job.Job) {
		eventID++
		fmt.Fprintf(w, "id: %d\nevent: status\ndata: %s\n\n", eventID, mustJSON(j))
		flusher.Flush()
	}

	// Snapshot first: the current state, whatever phase the run is already in.
	send(j)
	if j.Phase.Terminal() {
		return
	}
	lastUpdate := j.UpdatedAt

	ticker := time.NewTicker(rt.sseInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			next, err := rt.jobs.Get(r.Context(), id)
			if err != nil {
				return
			}
			if next.UpdatedAt.After(lastUpdate) {
				send(next)
				lastUpdate = next.UpdatedAt
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
