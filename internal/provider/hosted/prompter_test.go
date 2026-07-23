package hosted

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
)

func TestPrompterReturnsModelText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant",` +
			`"model":"claude-haiku-4-5","content":[{"type":"text","text":"10.1145/3772318.3791265"}],` +
			`"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`))
	}))
	t.Cleanup(server.Close)

	p, err := NewPrompter(WithAPIKey("test-credential"), WithAPIBase(server.URL))
	if err != nil {
		t.Fatalf("NewPrompter: %v", err)
	}
	got, err := p.Complete(context.Background(), "identify this paper")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "10.1145/3772318.3791265" {
		t.Errorf("Complete = %q, want the model's text block", got)
	}
}

func TestPrompterPropagatesAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	p, err := NewPrompter(WithAPIKey("test-credential"), WithAPIBase(server.URL),
		WithRequestOptions(option.WithMaxRetries(0)))
	if err != nil {
		t.Fatalf("NewPrompter: %v", err)
	}
	if _, err := p.Complete(context.Background(), "prompt"); err == nil {
		t.Error("Complete should surface a backend error")
	}
}
