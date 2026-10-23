package transfer

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestPullPlan(t *testing.T) {
	known := State{
		"same":     {10, 100, "b1"},
		"touched":  {10, 100, "b2"},
		"resized":  {10, 100, "b3"},
		"remerged": {10, 100, "b4"}, // main moved on since the last sync
		"gone":     {10, 100, "b5"},
	}
	main := Tree{
		"same": {ID: "b1"}, "touched": {ID: "b2"}, "resized": {ID: "b3"},
		"remerged": {ID: "b4-new"}, "gone": {ID: "b5"}, "local-only": {ID: "b6"},
	}
	server := Manifest{
		"same": {10, 100}, "touched": {10, 200}, "resized": {11, 100},
		"remerged": {10, 100}, "fresh": {1, 1},
	}
	fetch, gone := PullPlan(known, main, server)
	if want := []string{"fresh", "remerged", "resized", "touched"}; !slices.Equal(fetch, want) {
		t.Errorf("fetch = %v, want %v", fetch, want)
	}
	if want := []string{"gone", "local-only"}; !slices.Equal(gone, want) {
		t.Errorf("gone = %v, want %v", gone, want)
	}
}

func TestPullPlanWithoutState(t *testing.T) {
	fetch, _ := PullPlan(State{}, Tree{"a": {ID: "x"}}, Manifest{"a": {1, 1}})
	if !slices.Equal(fetch, []string{"a"}) {
		t.Errorf("a machine with no state must fetch everything, got %v", fetch)
	}
}

func TestDeployPlan(t *testing.T) {
	base := Tree{
		"edited": {ID: "a"}, "removed": {ID: "b"}, "both-edited": {ID: "c"},
		"removed-but-edited-live": {ID: "d"}, "already-live": {ID: "e"},
		"deleted-live": {ID: "f"}, "untouched": {ID: "g"},
	}
	main := Tree{
		"edited": {ID: "a2"}, "both-edited": {ID: "c2"}, "already-live": {ID: "e2"},
		"deleted-live": {ID: "f2"}, "untouched": {ID: "g"},
		"new": {ID: "h"}, "new-but-exists-live": {ID: "i"},
	}
	// What the server holds now, for the paths that differ between base and main.
	server := map[string]string{
		"edited": "a", "removed": "b", "both-edited": "c-live",
		"removed-but-edited-live": "d-live", "already-live": "e2",
		"new-but-exists-live": "someone-elses",
	}
	up, del, drift := DeployPlan(base, main, server)
	if want := []string{"edited", "new"}; !slices.Equal(up, want) {
		t.Errorf("up = %v, want %v", up, want)
	}
	if want := []string{"removed"}; !slices.Equal(del, want) {
		t.Errorf("del = %v, want %v", del, want)
	}
	if want := []string{"both-edited", "deleted-live", "new-but-exists-live", "removed-but-edited-live"}; !slices.Equal(drift, want) {
		t.Errorf("drift = %v, want %v", drift, want)
	}
}

func TestDeployPlanFromNothing(t *testing.T) {
	// No sync and no deploy yet: everything in main is new, nothing is deleted,
	// and a file already on the server is never overwritten unseen.
	up, del, drift := DeployPlan(Tree{}, Tree{"a": {ID: "1"}, "b": {ID: "2"}}, map[string]string{"b": "other"})
	if !slices.Equal(up, []string{"a"}) || len(del) != 0 || !slices.Equal(drift, []string{"b"}) {
		t.Errorf("up %v, del %v, drift %v", up, del, drift)
	}
}

func TestBlobIDMatchesGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	content := "line one\r\nline two\n\x00binary"
	cmd := exec.Command("git", "hash-object", "--no-filters", "--stdin")
	cmd.Stdin = strings.NewReader(content)
	want, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	got, err := BlobID(strings.NewReader(content), int64(len(content)))
	if err != nil || got != strings.TrimSpace(string(want)) {
		t.Errorf("BlobID = %s, git says %s", got, want)
	}
}
