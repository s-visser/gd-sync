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
	known := State{
		"unchanged":     {1, 1, "a"},
		"edited":        {1, 1, "b"},
		"edited-both":   {1, 1, "c"}, // main and server both changed it
		"removed":       {1, 1, "d"},
		"removed-moved": {1, 1, "e"}, // main removed it, server changed it
		"server-gone":   {1, 1, "f"}, // main changed it, server deleted it
		"already-gone":  {1, 1, "g"}, // main removed it, so did the server
	}
	server := Manifest{
		"unchanged": {1, 1}, "edited": {1, 1}, "edited-both": {2, 2},
		"removed": {1, 1}, "removed-moved": {1, 2}, "unknown": {5, 5}, "stranger": {9, 9},
	}
	main := Tree{
		"unchanged": {ID: "a"}, "edited": {ID: "b2"}, "edited-both": {ID: "c2"},
		"server-gone": {ID: "f2"}, "new": {ID: "h"}, "unknown": {ID: "i"},
	}
	up, del, drift := DeployPlan(known, server, main)
	if want := []string{"edited", "edited-both", "new", "server-gone", "unknown"}; !slices.Equal(up, want) {
		t.Errorf("up = %v, want %v", up, want)
	}
	if want := []string{"removed", "removed-moved"}; !slices.Equal(del, want) {
		t.Errorf("del = %v, want %v (never a file gd-sync didn't know)", del, want)
	}
	if want := []string{"edited-both", "removed-moved", "server-gone", "unknown"}; !slices.Equal(drift, want) {
		t.Errorf("drift = %v, want %v", drift, want)
	}
}

func TestAfterDeploy(t *testing.T) {
	known := State{"kept": {1, 1, "a"}, "moved": {1, 1, "b"}, "up": {1, 1, "c"}}
	server := Manifest{"kept": {1, 1}, "moved": {1, 9}, "up": {3, 7}}
	main := Tree{"kept": {ID: "a"}, "moved": {ID: "b"}, "up": {ID: "c2"}}
	s := AfterDeploy(known, server, main, []string{"up"})
	if s["up"] != (Known{3, 7, "c2"}) || s["kept"] != (Known{1, 1, "a"}) {
		t.Errorf("unexpected state %v", s)
	}
	if _, ok := s["moved"]; ok {
		t.Error("a file the server changed must drop out of the state")
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
