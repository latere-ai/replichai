package publish

import (
	"context"
	"reflect"
	"testing"
)

func outputs(paths ...string) map[string][]byte {
	files := make(map[string][]byte, len(paths))
	for _, p := range paths {
		files[p] = []byte("content of " + p)
	}
	return files
}

// A run that pushed nothing but copied its work to the output directory is
// recovered whole. This is the incident 044 records: the repository held only
// the system's own identity file, and verdict.json existed only as a session
// file the server used to throw away.
func TestCommitOutputsRecoversARunThatNeverPushed(t *testing.T) {
	forge := newFakeForge([]string{"README.md", ".replichai/paper.json"})

	rep, err := CommitOutputs(context.Background(), forge, "open-hai", "a-paper-xyz",
		outputs("REPRODUCIBILITY.md", "verdict.json", "instrument.json", "src/run.py"))
	if err != nil {
		t.Fatalf("CommitOutputs: %v", err)
	}
	want := []string{"REPRODUCIBILITY.md", "instrument.json", "src/run.py", "verdict.json"}
	if !reflect.DeepEqual(rep.Committed, want) {
		t.Errorf("committed = %v, want %v", rep.Committed, want)
	}
	if !rep.Recovered() {
		t.Error("a harvest that committed four files recovered something")
	}
	for _, p := range want {
		if _, ok := forge.files[p]; !ok {
			t.Errorf("%s is not in the repository", p)
		}
	}
	// One commit, named so a reader can tell it from the agent's own history.
	if !reflect.DeepEqual(forge.msgs, []string{outputsMessage}) {
		t.Errorf("commit messages = %v, want one %q", forge.msgs, outputsMessage)
	}
}

// The redistribution boundary holds on the way in. A harvested file has never
// passed the mount's gate, so it passes VerifyTree here or it is not committed.
func TestCommitOutputsRefusesThirdPartyData(t *testing.T) {
	forge := newFakeForge([]string{"README.md"})

	rep, err := CommitOutputs(context.Background(), forge, "open-hai", "a-paper-xyz",
		outputs("paper.pdf", "data/corpus.zip", ".env", "verdict.json"))
	if err != nil {
		t.Fatalf("CommitOutputs: %v", err)
	}
	if !reflect.DeepEqual(rep.Committed, []string{"verdict.json"}) {
		t.Errorf("committed = %v, want only verdict.json", rep.Committed)
	}
	if !reflect.DeepEqual(rep.Refused, []string{".env", "data/corpus.zip", "paper.pdf"}) {
		t.Errorf("refused = %v", rep.Refused)
	}
	for _, p := range []string{"paper.pdf", "data/corpus.zip", ".env"} {
		if _, ok := forge.files[p]; ok {
			t.Errorf("%s was committed and must not have been", p)
		}
	}
}

// A path that climbs out of the tree, names an absolute location, or reaches
// into .git is refused rather than cleaned into something that looks harmless.
func TestCommitOutputsRefusesUnsafePaths(t *testing.T) {
	forge := newFakeForge(nil)

	rep, err := CommitOutputs(context.Background(), forge, "open-hai", "a-paper-xyz",
		outputs("../escape.md", "/etc/passwd.md", ".git/config", "ok.md"))
	if err != nil {
		t.Fatalf("CommitOutputs: %v", err)
	}
	if !reflect.DeepEqual(rep.Committed, []string{"ok.md"}) {
		t.Errorf("committed = %v, want only ok.md", rep.Committed)
	}
	if len(rep.Refused) != 3 {
		t.Errorf("refused = %v, want three", rep.Refused)
	}
}

// What the agent pushed wins. A copy in the output directory may predate the
// agent's last revision, so it never overwrites the committed tree.
func TestCommitOutputsNeverOverwritesWhatTheAgentPushed(t *testing.T) {
	forge := newFakeForge([]string{"REPRODUCIBILITY.md", "verdict.json"})
	forge.files["verdict.json"] = []byte("the revised verdict")

	rep, err := CommitOutputs(context.Background(), forge, "open-hai", "a-paper-xyz",
		outputs("verdict.json", "REPRODUCIBILITY.md", "SOURCES.md"))
	if err != nil {
		t.Fatalf("CommitOutputs: %v", err)
	}
	if !reflect.DeepEqual(rep.Skipped, []string{"REPRODUCIBILITY.md", "verdict.json"}) {
		t.Errorf("skipped = %v", rep.Skipped)
	}
	if !reflect.DeepEqual(rep.Committed, []string{"SOURCES.md"}) {
		t.Errorf("committed = %v, want only SOURCES.md", rep.Committed)
	}
	if got := string(forge.files["verdict.json"]); got != "the revised verdict" {
		t.Errorf("the pushed verdict was overwritten: %q", got)
	}
}

// Nothing to harvest touches the repository at all — no head read, no commit.
func TestCommitOutputsIsANoOpWithNothingToHarvest(t *testing.T) {
	forge := newFakeForge([]string{"README.md"})

	rep, err := CommitOutputs(context.Background(), forge, "open-hai", "a-paper-xyz", nil)
	if err != nil {
		t.Fatalf("CommitOutputs: %v", err)
	}
	if rep.Recovered() || len(forge.msgs) != 0 {
		t.Errorf("an empty harvest committed something: %+v %v", rep, forge.msgs)
	}
}
