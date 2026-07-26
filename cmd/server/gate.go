package main

import (
	"net/http"

	"github.com/latere-ai/replichai/internal/httpapi"
)

// devPrincipal is the synthetic identity every request is attributed to under
// the dev gate. It is not a grant and confers nothing in a real deployment —
// the fail-closed gate and 006c's access model replace it.
const devPrincipal = "dev-user"

// devGate admits every request. It exists only so the web app can be exercised
// end to end locally before 006c's identity and access gate are built. It must
// never run anywhere reachable by an untrusted client.
var devGate = httpapi.GateFunc(func(*http.Request) (bool, string) { return true, "" })

// withDevPrincipal attributes every request to the synthetic dev principal, so
// submitted jobs carry a submitter the way a real signed-in request would.
func withDevPrincipal(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(httpapi.WithPrincipal(r.Context(), devPrincipal)))
	})
}
