package webauth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/latere-ai/replichai/internal/httpapi"
)

// AC6: the dev bypass refuses a non-loopback bind unless the insecure override
// is set, so it cannot ship the back door to a reachable deployment.
func TestCheckDevBypass(t *testing.T) {
	loopback := []string{"127.0.0.1:8080", "localhost:8080", "[::1]:8080", "127.0.0.1:0"}
	for _, addr := range loopback {
		if err := CheckDevBypass(addr, false); err != nil {
			t.Errorf("loopback %q should be allowed: %v", addr, err)
		}
	}
	reachable := []string{":8080", "0.0.0.0:8080", "replichai.latere.ai:443", "192.168.1.5:8080"}
	for _, addr := range reachable {
		if err := CheckDevBypass(addr, false); err == nil {
			t.Errorf("non-loopback %q must be refused without the override", addr)
		}
		// The explicit override permits it (a containerised local run).
		if err := CheckDevBypass(addr, true); err != nil {
			t.Errorf("override should permit %q: %v", addr, err)
		}
	}
}

func TestDevMiddlewareSetsIdentity(t *testing.T) {
	var got httpapi.Identity
	var ok bool
	h := DevMiddleware("dev-user", "dev@local")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok = httpapi.IdentityFrom(r)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/jobs/x", nil))
	if !ok || got.Sub != "dev-user" || got.Email != "dev@local" {
		t.Fatalf("dev identity = %+v ok=%v", got, ok)
	}
}
