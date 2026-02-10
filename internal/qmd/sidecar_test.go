package qmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateWorkspacePath(t *testing.T) {
	root := "/data/workspaces"
	path, err := validateWorkspacePath(root, "/data/workspaces/ws-1")
	if err != nil {
		t.Fatalf("validate path failed: %v", err)
	}
	if path != "/data/workspaces/ws-1" {
		t.Fatalf("unexpected path: %s", path)
	}

	if _, err := validateWorkspacePath(root, "/tmp/ws-1"); err == nil {
		t.Fatal("expected out-of-root path to fail validation")
	}
}

func TestRunQMDViaSidecarSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", req.Method)
		}
		var payload map[string]any
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		output := base64.StdEncoding.EncodeToString([]byte("ok"))
		_ = json.NewEncoder(w).Encode(map[string]string{"output": output})
	}))
	defer server.Close()

	service := newService(
		Config{
			WorkspaceRoot: "/data/workspaces",
			SidecarURL:    server.URL,
		},
		nil,
		nil,
	)

	output, err := service.runQMD(context.Background(), "/data/workspaces/ws-1", "status")
	if err != nil {
		t.Fatalf("run qmd sidecar failed: %v", err)
	}
	if string(output) != "ok" {
		t.Fatalf("unexpected output: %s", output)
	}
}

func TestRunQMDViaSidecarError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "sidecar failed"})
	}))
	defer server.Close()

	service := newService(
		Config{
			WorkspaceRoot: "/data/workspaces",
			SidecarURL:    server.URL,
		},
		nil,
		nil,
	)

	if _, err := service.runQMD(context.Background(), "/data/workspaces/ws-1", "status"); err == nil {
		t.Fatal("expected sidecar error")
	}
}
