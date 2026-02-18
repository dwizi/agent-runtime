package mcp

import "testing"

func TestBuildRegisteredToolName(t *testing.T) {
	name := BuildRegisteredToolName("GitHub Cloud", "List Repos")
	if name != "mcp_github_cloud__list_repos" {
		t.Fatalf("unexpected registered name: %s", name)
	}
}

func TestEnsureUniqueRegisteredNames(t *testing.T) {
	names := EnsureUniqueRegisteredNames("github", []string{"tool-a", "tool a"})
	first := names["tool-a"]
	second := names["tool a"]
	if first == second {
		t.Fatalf("expected unique names, got %s", first)
	}
}

func TestBuildRegisteredToolNameTruncatesAndHashes(t *testing.T) {
	long := "super_long_tool_name_with_many_many_many_segments_that_should_force_a_hash_suffix_because_it_is_huge"
	name := BuildRegisteredToolName("github", long+long+long)
	if len(name) > 128 {
		t.Fatalf("expected <=128 chars, got %d", len(name))
	}
}
