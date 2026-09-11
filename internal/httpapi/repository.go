// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/latere-ai/replichai/internal/paper"
)

// This file implements 045l: the repository a locally run session pushes to
// comes from the platform rather than from a credential on the operator's
// machine.
//
// The route exists because the order of operations is reversed. Without it, the
// terminal creates the repository with an organisation token it holds, pushes,
// and files a record naming what it made. With platform provisioning, the
// terminal needs the credential before it has anything to file: a session that
// has not finished cannot be imported, and one that has finished has nowhere to
// push. So this is its own route, and it is on the project rather than on a
// session, because the name comes from the paper's identity and the session
// that fills the repository does not exist yet.

// RepoProvisioner allocates a repository for a paper and mints a credential
// that can write to it and to nothing else. It runs with the server's forge
// identity; the caller never sees that identity, only the minted token.
//
// It is nil when the deployment has no GitHub App. A personal access token
// authenticates but cannot mint a sub-token of itself, and returning the
// organisation token instead would put the same credential on the operator's
// machine and in a response body.
type RepoProvisioner interface {
	Provision(ctx context.Context, id paper.Identity) (Provisioned, error)
}

// Provisioned is the response to a terminal: the repository to push to and
// the credential to push with.
type Provisioned struct {
	Repo    string    `json:"repo"`
	URL     string    `json:"url"`
	Token   string    `json:"token"`
	Expires time.Time `json:"expires_at"`
}

// MaxProvisionBytes bounds the request body, which names one paper.
const MaxProvisionBytes = 4 << 10

// provisionRepository allocates the repository a local run will push to.
func (rt *Router) provisionRepository(w http.ResponseWriter, r *http.Request) {
	proj, ok := rt.projectForWork(w, r)
	if !ok {
		return
	}
	if rt.repos == nil {
		writeError(w, http.StatusNotImplemented, "minting_unavailable")
		return
	}
	var body struct {
		PaperKey string `json:"paper_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil { // The body is bounded at registration to MaxProvisionBytes.
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	held, ok := rt.paperHeldBy(w, r, proj, body.PaperKey)
	if !ok {
		return
	}
	// The identity comes from what the project already holds, so a terminal
	// cannot name a repository. It names a paper the project holds, and the
	// platform decides what that paper's repository is called. No lookup runs
	// here, because the project resolved this paper when it filed it.
	provisioned, err := rt.repos.Provision(r.Context(), paper.Identity{
		Key: held.Key, Title: held.Title, Slug: paper.Slug(held.Title),
	})
	switch {
	case errors.Is(err, ErrMintingUnavailable):
		writeError(w, http.StatusNotImplemented, "minting_unavailable")
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, "provision_failed")
		return
	}
	writeJSON(w, http.StatusCreated, provisioned)
}

// ErrMintingUnavailable is returned by a provisioner when the deployment's
// forge credential cannot mint. It is distinct from a forge outage: the
// remedy is a deployment setting, which the operator can change.
var ErrMintingUnavailable = errors.New("minting a scoped token needs a GitHub App")
