package app

import "testing"

func TestWorkspaceIDFromPath(t *testing.T) {
	root := "/data/workspaces"

	got := workspaceIDFromPath(root, "/data/workspaces/ws-1/docs/notes.md")
	if got != "ws-1" {
		t.Fatalf("expected workspace ws-1, got %q", got)
	}

	got = workspaceIDFromPath(root, "/tmp/other.md")
	if got != "" {
		t.Fatalf("expected empty workspace for out-of-root path, got %q", got)
	}
}

func TestParseCSVSet(t *testing.T) {
	set := parseCSVSet(" admin,overlord , ,Member ")
	if len(set) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(set))
	}
	if _, ok := set["admin"]; !ok {
		t.Fatal("expected admin in set")
	}
	if _, ok := set["overlord"]; !ok {
		t.Fatal("expected overlord in set")
	}
	if _, ok := set["member"]; !ok {
		t.Fatal("expected member in set")
	}
}
