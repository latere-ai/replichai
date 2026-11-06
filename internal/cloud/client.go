// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package cloud

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	xotel "latere.ai/x/pkg/otel"

	"github.com/latere-ai/replichai/internal/wirejson"
)

// httpClient is this package's outbound client. Its transport is
// instrumented, so every request starts a client span and carries the W3C
// trace context to the server. http.DefaultClient is deliberately left
// unchanged, because replacing its transport would affect every package in
// the process.
var httpClient = xotel.HTTPClient()

// DefaultAPIURL is the deployment a terminal talks to.
const DefaultAPIURL = "https://replichai.latere.ai"

// Client is the CLI's client for the platform's API. Every call presents a
// bearer minted for this service, so a credential captured in transit is
// valid for minutes and for one audience only.
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
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Session: session, HTTP: httpClient}
}

// Project is a container of papers, as the API reports it.
type Project struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// ImportRecord describes a session the CLI ran locally. The locator is
// deliberately omitted: it holds a host path and a process handle, which mean
// nothing on the server and would leak the operator's home directory.
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

// UsageRecord is the tokens a session consumed. It is recorded but not
// priced, because whoever ran the session bought the compute from the vendor
// directly.
type UsageRecord struct {
	InputFresh   int64 `json:"input_fresh"`
	InputCached  int64 `json:"input_cached"`
	CacheWrite   int64 `json:"cache_write"`
	CacheWrite5m int64 `json:"cache_write_5m"`
	CacheWrite1h int64 `json:"cache_write_1h"`
	Output       int64 `json:"output"`
}

// CreateProject creates a project to file work under. The response also
// carries the paper results and the seeded members, which the CLI does not
// need; it keeps only the id and title.
func (c *Client) CreateProject(ctx context.Context, title string) (Project, error) {
	var out struct {
		ID    string `json:"project_id"`
		Title string `json:"title"`
	}
	err := c.call(ctx, http.MethodPost, "/projects", map[string]any{"title": title}, &out)
	return Project{ID: out.ID, Title: out.Title}, err
}

// ListProjects returns the projects this principal owns. The response is a
// bare array of the same view the web app renders, which carries many more
// fields; the CLI keeps only id and title.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var out []Project
	if err := c.call(ctx, http.MethodGet, "/projects", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ProjectSession is one session as the CLI needs it: which paper it ran, how
// it ended, where its reproduction is, and the id a delete names. The app's
// own view of a session carries many more fields, which the CLI does not
// need.
type ProjectSession struct {
	ID       string `json:"id"`
	Project  string `json:"project_id"`
	Ref      string `json:"ref"`
	PaperKey string `json:"paper_key"`
	Phase    string `json:"phase"`
	Origin   string `json:"origin"`
	Repo     string `json:"repo_name"`
}

// ListSessions returns every session this principal holds, filtered to one
// project when projectID is given. The filter is applied client-side because
// /jobs returns everything the principal may see and offers no project
// filter; the client does not invent a route the server does not serve.
func (c *Client) ListSessions(ctx context.Context, projectID string) ([]ProjectSession, error) {
	var all []ProjectSession
	if err := c.call(ctx, http.MethodGet, "/jobs", nil, &all); err != nil {
		return nil, err
	}
	if strings.TrimSpace(projectID) == "" {
		return all, nil
	}
	kept := make([]ProjectSession, 0, len(all))
	for _, s := range all {
		if s.Project == projectID {
			kept = append(kept, s)
		}
	}
	return kept, nil
}

// DeleteSession deletes one session (032). The repository is closed, not
// destroyed; only the project's record of the run is removed.
//
// Only the owner may delete, and only a session that has ended. The server
// enforces both rules and the client does not check them itself, because a
// client that duplicates the server's rules disagrees with it after the next
// release.
func (c *Client) DeleteSession(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodDelete, "/jobs/"+id, nil, nil)
}

// AddPaper files a paper under a project. A paper already present is not an
// error: filing is idempotent, so a batch pushed twice does not fail.
//
// The conflict is recognized by the machine-readable reason paper_exists,
// not by a word in the message. The message is prose and may change; the
// reason is the contract.
func (c *Client) AddPaper(ctx context.Context, projectID, reference string) error {
	err := c.call(ctx, http.MethodPost, "/projects/"+projectID+"/papers",
		map[string]any{"url": reference}, nil)
	if err != nil && strings.Contains(err.Error(), "paper_exists") {
		return nil
	}
	return err
}

// ImportSession files a session executed on this machine. Pushing the same
// session again updates its row rather than creating a second one, so an
// interrupted upload is resumed by running it again.
func (c *Client) ImportSession(ctx context.Context, projectID string, record ImportRecord) (string, error) {
	var out struct {
		JobID string `json:"job_id"`
	}
	err := c.call(ctx, http.MethodPost, "/projects/"+projectID+"/sessions/import", record, &out)
	return out.JobID, err
}

// call presents the credential and reads the response. An error carries the
// server's own machine-readable reason as well as the status line.
func (c *Client) call(ctx context.Context, method, path string, body, into any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body, wirejson.Text)
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
	bearer, _, err := c.Session.Bearer(ctx, ActorTTL)
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
	return json.Unmarshal(payload, into, wirejson.Foreign)
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
// credential, so the CLI has to supply its own. It is a deployment setting,
// not a failure, so it is a named error rather than a status line the caller
// has to parse.
var ErrMintingUnavailable = errors.New("this deployment cannot provision a repository")

// ProvisionRepository asks the platform for the repository a local run will
// push to. The CLI does not choose the repository name. It names a paper the
// project holds, and the platform allocates from the same registry the hosted
// path uses.
func (c *Client) ProvisionRepository(ctx context.Context, projectID, paperKey string) (Repository, error) {
	var out Repository
	err := c.call(ctx, http.MethodPost, "/projects/"+projectID+"/repository",
		map[string]any{"paper_key": paperKey}, &out)
	if err != nil && strings.Contains(err.Error(), "minting_unavailable") {
		return Repository{}, ErrMintingUnavailable
	}
	return out, err
}
