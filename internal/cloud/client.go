package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultAPIURL is the deployment a terminal talks to.
const DefaultAPIURL = "https://replichai.latere.ai"

// Client is the terminal's view of the platform's API. Every call presents a
// bearer minted for this service, so a credential captured in transit is
// bounded to minutes and to one audience.
type Client struct {
	BaseURL string
	Session *Session
	HTTP    *http.Client
}

// NewClient builds a client over a signed-in session.
func NewClient(session *Session, baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultAPIURL
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Session: session, HTTP: http.DefaultClient}
}

// Project is a container of papers, as the API reports it.
type Project struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// ImportRecord is what a terminal says about a session it ran. The locator is
// deliberately absent: it holds a host path and a process handle, which mean
// nothing on the server and leak the operator's home directory.
type ImportRecord struct {
	PaperKey   string      `json:"paper_key"`
	Ref        string      `json:"ref"`
	Runtime    string      `json:"runtime"`
	SessionID  string      `json:"session_id"`
	Harness    string      `json:"harness,omitempty"`
	Sandbox    string      `json:"sandbox,omitempty"`
	Phase      string      `json:"phase"`
	Grade      string      `json:"grade,omitempty"`
	Model      ModelRef    `json:"model"`
	Usage      UsageRecord `json:"usage"`
	Repo       string      `json:"repo,omitempty"`
	Error      string      `json:"error,omitempty"`
	StartedAt  time.Time   `json:"started_at,omitzero"`
	FinishedAt time.Time   `json:"finished_at,omitzero"`
}

// ModelRef is the exact model a session ran on.
type ModelRef struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

// UsageRecord is what it consumed. It is recorded and not priced: the compute
// was bought from the vendor by whoever ran it.
type UsageRecord struct {
	InputFresh   int64 `json:"input_fresh"`
	InputCached  int64 `json:"input_cached"`
	CacheWrite   int64 `json:"cache_write"`
	CacheWrite5m int64 `json:"cache_write_5m"`
	CacheWrite1h int64 `json:"cache_write_1h"`
	Output       int64 `json:"output"`
}

// CreateProject opens a project to file work under. The create answers with
// the paper results and the seeded members beside the project, and the terminal
// wants neither: what it needs is the id to file against.
func (c *Client) CreateProject(ctx context.Context, title string) (Project, error) {
	var out struct {
		ID    string `json:"project_id"`
		Title string `json:"title"`
	}
	err := c.call(ctx, http.MethodPost, "/projects", map[string]any{"title": title}, &out)
	return Project{ID: out.ID, Title: out.Title}, err
}

// ListProjects is what this principal owns. The list answers with a bare array
// of the same view the web app renders, which carries far more than two fields;
// everything past id and title is a screen's business, not a terminal's.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var out []Project
	if err := c.call(ctx, http.MethodGet, "/projects", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// AddPaper files a paper under a project. A paper already there is not an
// error: filing is idempotent, and a batch pushed twice must not fail on it.
//
// The refusal is recognised by the reason the app writes, not by an English
// word in it. A message is prose and changes; paper_exists is the contract.
func (c *Client) AddPaper(ctx context.Context, projectID, reference string) error {
	err := c.call(ctx, http.MethodPost, "/projects/"+projectID+"/papers",
		map[string]any{"url": reference}, nil)
	if err != nil && strings.Contains(err.Error(), "paper_exists") {
		return nil
	}
	return err
}

// ImportSession files a session executed on this machine. Re-pushing the same
// session updates its row rather than opening a second, so an interrupted
// upload is resumed by running it again.
func (c *Client) ImportSession(ctx context.Context, projectID string, record ImportRecord) (string, error) {
	var out struct {
		JobID string `json:"job_id"`
	}
	err := c.call(ctx, http.MethodPost, "/projects/"+projectID+"/sessions/import", record, &out)
	return out.JobID, err
}

// call presents the credential and reads the answer. A refusal carries the
// server's own machine-readable reason rather than only a status line.
func (c *Client) call(ctx context.Context, method, path string, body, into any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	bearer, _, err := c.Session.Bearer(ctx, 5*time.Minute)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+bearer)

	response, err := c.HTTP.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: %s: %s", method, path, response.Status, strings.TrimSpace(string(payload)))
	}
	if into == nil {
		return nil
	}
	return json.Unmarshal(payload, into)
}

// Repository is the repository the platform allocated for a paper, and the
// credential to push it with (045l).
type Repository struct {
	Repo    string    `json:"repo"`
	URL     string    `json:"url"`
	Token   string    `json:"token"`
	Expires time.Time `json:"expires_at"`
}

// ErrMintingUnavailable reports that the deployment cannot mint a scoped
// credential, so the terminal has to supply its own. It is a deployment
// setting, not a failure, which is why it is a named condition rather than a
// status line the caller has to parse.
var ErrMintingUnavailable = errors.New("this deployment cannot provision a repository")

// ProvisionRepository asks the platform for the repository a local run will
// push to. The terminal does not name it: it names a paper the project holds,
// and the platform allocates from the same registry the hosted path uses.
func (c *Client) ProvisionRepository(ctx context.Context, projectID, paperKey string) (Repository, error) {
	var out Repository
	err := c.call(ctx, http.MethodPost, "/projects/"+projectID+"/repository",
		map[string]any{"paper_key": paperKey}, &out)
	if err != nil && strings.Contains(err.Error(), "minting_unavailable") {
		return Repository{}, ErrMintingUnavailable
	}
	return out, err
}
