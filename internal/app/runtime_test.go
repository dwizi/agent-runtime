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

func TestParseCSVList(t *testing.T) {
	list := parseCSVList(" curl,git , ,RG,curl ")
	if len(list) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(list))
	}
	if list[0] != "curl" || list[1] != "git" || list[2] != "rg" {
		t.Fatalf("unexpected list: %+v", list)
	}
}

func TestParseShellArgs(t *testing.T) {
	args := parseShellArgs(" --network=off   --readonly ")
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(args))
	}
	if args[0] != "--network=off" || args[1] != "--readonly" {
		t.Fatalf("unexpected shell args: %+v", args)
	}
}
