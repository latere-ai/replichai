package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/paper"
)

// fakeProvisioner records what it was asked for and answers with a credential
// no wider than one repository.
type fakeProvisioner struct {
	asked []paper.Identity
	fail  error
}

func (f *fakeProvisioner) Provision(_ context.Context, id paper.Identity) (Provisioned, error) {
	f.asked = append(f.asked, id)
	if f.fail != nil {
		return Provisioned{}, f.fail
	}
	return Provisioned{
		Repo: "open-hai/a-paper-a3f9", URL: "https://github.com/open-hai/a-paper-a3f9.git",
		Token: "ghs_scoped", Expires: time.Date(2026, 8, 26, 13, 0, 0, 0, time.UTC),
	}, nil
}

// 045l AC5–AC6: a member gets a repository and a credential to push it, and the
// terminal never names the repository — it names a paper the project holds.
func TestProvisioningNamesTheRepositoryFromThePaperTheProjectHolds(t *testing.T) {
	d := testDeps(allowAll)
	repos := &fakeProvisioner{}
	d.Repos = repos
	h := NewRouter(d).Handler()
	id := filed(t, d, "me", "doi:10.1145/3654777.3676417")

	response := do(t, h, http.MethodPost, "/projects/"+id+"/repository",
		`{"paper_key":"doi:10.1145/3654777.3676417","repo":"open-hai/anything-i-like"}`, "me")
	if response.Code != http.StatusCreated {
		t.Fatalf("provision = %d: %s", response.Code, response.Body)
	}
	var body Provisioned
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Repo != "open-hai/a-paper-a3f9" || body.Token != "ghs_scoped" {
		t.Fatalf("provisioned = %+v", body)
	}
	if body.Expires.IsZero() {
		t.Error("a credential handed out with no end is not a short-lived one")
	}
	if len(repos.asked) != 1 || repos.asked[0].Key != "doi:10.1145/3654777.3676417" {
		t.Fatalf("provisioner was asked for %+v", repos.asked)
	}
	// The repo name in the request body reached nothing: the platform decides.
	if strings.Contains(response.Body.String(), "anything-i-like") {
		t.Error("the terminal named its own repository")
	}
}

// A paper the project does not hold gets nothing, on the same terms as every
// other route that works against a project's papers.
func TestProvisioningRefusesAPaperTheProjectDoesNotHold(t *testing.T) {
	d := testDeps(allowAll)
	d.Repos = &fakeProvisioner{}
	h := NewRouter(d).Handler()
	id := filed(t, d, "me", "doi:10.1145/3654777.3676417")

	response := do(t, h, http.MethodPost, "/projects/"+id+"/repository",
		`{"paper_key":"doi:10.9999/not-here"}`, "me")
	if response.Code != http.StatusNotFound {
		t.Fatalf("provision = %d, want 404: %s", response.Code, response.Body)
	}
}

// 045l AC5: a non-member is refused by the same gate every project write uses.
func TestProvisioningIsAProjectWrite(t *testing.T) {
	d := testDeps(allowAll)
	d.Repos = &fakeProvisioner{}
	h := NewRouter(d).Handler()
	id := filed(t, d, "me", "doi:10.1145/3654777.3676417")

	response := do(t, h, http.MethodPost, "/projects/"+id+"/repository",
		`{"paper_key":"doi:10.1145/3654777.3676417"}`, "somebody-else")
	if response.Code != http.StatusForbidden {
		t.Fatalf("provision by a non-member = %d, want 403: %s", response.Code, response.Body)
	}
}

// 045l AC4: a deployment whose forge credential cannot mint says so, and never
// answers with a credential of its own. This test fails if the handler ever
// starts returning the server's token as a consolation.
func TestADeploymentThatCannotMintSaysSo(t *testing.T) {
	for name, deps := range map[string]func(Deps) Deps{
		"no provisioner wired": func(d Deps) Deps { return d },
		"provisioner refuses": func(d Deps) Deps {
			d.Repos = &fakeProvisioner{fail: ErrMintingUnavailable}
			return d
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := deps(testDeps(allowAll))
			h := NewRouter(d).Handler()
			id := filed(t, d, "me", "doi:10.1145/3654777.3676417")

			response := do(t, h, http.MethodPost, "/projects/"+id+"/repository",
				`{"paper_key":"doi:10.1145/3654777.3676417"}`, "me")
			if response.Code != http.StatusNotImplemented {
				t.Fatalf("provision = %d, want 501: %s", response.Code, response.Body)
			}
			if !strings.Contains(response.Body.String(), "minting_unavailable") {
				t.Errorf("body = %s, want the reason an operator can act on", response.Body)
			}
			if strings.Contains(response.Body.String(), "token") {
				t.Errorf("a refusal carried a credential: %s", response.Body)
			}
		})
	}
}
