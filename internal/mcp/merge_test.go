package mcp

import "testing"

func TestMergeServer(t *testing.T) {
	base := ServerConfig{
		ID:      "github",
		Enabled: true,
		Transport: TransportConfig{
			Type:     TransportStreamableHTTP,
			Endpoint: "https://base.example/mcp",
		},
		HTTP: HTTPConfig{
			Headers:        map[string]string{"Authorization": "Bearer base", "X-One": "1"},
			TimeoutSeconds: 30,
		},
		RefreshSeconds: 120,
		Policy: PolicyConfig{
			DefaultToolClass:        "general",
			DefaultRequiresApproval: false,
			ToolOverrides: map[string]ToolPolicy{
				"danger": {ToolClass: "sensitive", RequiresApproval: true},
			},
		},
	}
	enabled := false
	refresh := 60
	override := ServerOverride{
		ID:             "github",
		Enabled:        &enabled,
		HTTP:           &HTTPConfig{Headers: map[string]string{"Authorization": "Bearer override"}},
		RefreshSeconds: &refresh,
		Policy: &PolicyOverride{
			DefaultToolClass: strPtr("knowledge"),
			ToolOverrides: map[string]ToolOverride{
				"danger": {RequiresApproval: boolPtr(false)},
			},
		},
	}
	merged := MergeServer(base, override)
	if merged.Enabled {
		t.Fatal("expected merged enabled=false")
	}
	if merged.HTTP.Headers["Authorization"] != "Bearer override" {
		t.Fatalf("expected overridden authorization header, got %q", merged.HTTP.Headers["Authorization"])
	}
	if merged.HTTP.Headers["X-One"] != "1" {
		t.Fatalf("expected preserved X-One header, got %q", merged.HTTP.Headers["X-One"])
	}
	if merged.RefreshSeconds != 60 {
		t.Fatalf("expected refresh 60, got %d", merged.RefreshSeconds)
	}
	if merged.Policy.DefaultToolClass != "knowledge" {
		t.Fatalf("expected default tool class knowledge, got %s", merged.Policy.DefaultToolClass)
	}
	if merged.Policy.ToolOverrides["danger"].RequiresApproval {
		t.Fatal("expected danger override to disable requires approval")
	}
}

func TestFindOverride(t *testing.T) {
	enabled := true
	catalog := WorkspaceCatalog{Servers: []ServerOverride{{ID: "github", Enabled: &enabled}}}
	_, ok := FindOverride(catalog, "missing")
	if ok {
		t.Fatal("expected missing override")
	}
	item, ok := FindOverride(catalog, "github")
	if !ok {
		t.Fatal("expected github override")
	}
	if item.Enabled == nil || !*item.Enabled {
		t.Fatal("expected enabled override")
	}
}

func boolPtr(value bool) *bool    { return &value }
func strPtr(value string) *string { return &value }
