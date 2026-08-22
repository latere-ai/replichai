package webauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWriteJSONReportsAnUnencodableBody covers the guard writeJSON gained when
// the discarded encoder error was replaced by a checked marshal: a body that
// cannot become JSON must not be reported as a successful empty response.
func TestWriteJSONReportsAnUnencodableBody(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, map[string]any{"unencodable": make(chan int)})

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

// TestWriteJSONWritesTheBody keeps the happy path honest: the encoded value
// reaches the client as a 200 under the JSON content type.
func TestWriteJSONWritesTheBody(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, map[string]any{"tier": "admin"})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal body %q: %v", w.Body.String(), err)
	}
	if out["tier"] != "admin" {
		t.Errorf("body = %v", out)
	}
}
