package spa

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesIndexFallback(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	// A deep link that is not a real asset falls back to index.html.
	for _, path := range []string{"/", "/jobs/abc", "/dashboard"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Errorf("%s: status %d, want 200", path, w.Code)
		}
		if !strings.Contains(w.Body.String(), "replichai") {
			t.Errorf("%s: body did not contain the app shell", path)
		}
		if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Errorf("%s: content-type %q, want html", path, ct)
		}
	}
}
