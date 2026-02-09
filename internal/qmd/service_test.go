package qmd

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeRunner struct {
	mu       sync.Mutex
	calls    []string
	resolver func(cmd *exec.Cmd) ([]byte, error)
}

func (f *fakeRunner) Run(cmd *exec.Cmd) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, strings.Join(cmd.Args, " "))
	f.mu.Unlock()
	if f.resolver == nil {
		return nil, nil
	}
	return f.resolver(cmd)
}

func (f *fakeRunner) callsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]string, len(f.calls))
	copy(items, f.calls)
	return items
}

func TestSearchReturnsParsedResults(t *testing.T) {
	root := t.TempDir()
	workspaceID := "ws-test"
	workspacePath := filepath.Join(root, workspaceID)
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	runner := &fakeRunner{
		resolver: func(cmd *exec.Cmd) ([]byte, error) {
			args := strings.Join(cmd.Args, " ")
			switch {
			case strings.Contains(args, " query "):
				return []byte(`[{"path":"notes/today.md","docid":"#abc123","score":0.91,"snippet":"release prep notes"}]`), nil
			default:
				return []byte("ok"), nil
			}
		},
	}
	service := newService(
		Config{
			WorkspaceRoot: root,
			AutoEmbed:     false,
		},
		slog.Default(),
		runner,
	)

	results, err := service.Search(context.Background(), workspaceID, "release prep", 0)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	if results[0].Path != "notes/today.md" {
		t.Fatalf("unexpected path: %s", results[0].Path)
	}
	if results[0].DocID != "#abc123" {
		t.Fatalf("unexpected docid: %s", results[0].DocID)
	}
}

func TestOpenMarkdownFromWorkspacePath(t *testing.T) {
	root := t.TempDir()
	workspaceID := "ws-open"
	workspacePath := filepath.Join(root, workspaceID)
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	target := filepath.Join(workspacePath, "memory.md")
	if err := os.WriteFile(target, []byte("line one\nline two"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	service := newService(
		Config{
			WorkspaceRoot: root,
			OpenMaxBytes:  2048,
		},
		slog.Default(),
		&fakeRunner{},
	)

	result, err := service.OpenMarkdown(context.Background(), workspaceID, "memory.md")
	if err != nil {
		t.Fatalf("open markdown failed: %v", err)
	}
	if result.Path != "memory.md" {
		t.Fatalf("unexpected path: %s", result.Path)
	}
	if !strings.Contains(result.Content, "line two") {
		t.Fatalf("unexpected content: %s", result.Content)
	}
}

func TestOpenMarkdownRejectsTraversal(t *testing.T) {
	service := newService(
		Config{
			WorkspaceRoot: t.TempDir(),
		},
		slog.Default(),
		&fakeRunner{},
	)

	_, err := service.OpenMarkdown(context.Background(), "ws-traversal", "../secrets.md")
	if !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("expected invalid target, got %v", err)
	}
}

func TestSearchReturnsUnavailableWhenBinaryMissing(t *testing.T) {
	root := t.TempDir()
	workspaceID := "ws-unavailable"
	workspacePath := filepath.Join(root, workspaceID)
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	runner := &fakeRunner{
		resolver: func(cmd *exec.Cmd) ([]byte, error) {
			return nil, exec.ErrNotFound
		},
	}
	service := newService(
		Config{
			WorkspaceRoot: root,
			AutoEmbed:     false,
		},
		slog.Default(),
		runner,
	)

	_, err := service.Search(context.Background(), workspaceID, "anything", 1)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected unavailable error, got %v", err)
	}
}

func TestQueueWorkspaceIndexDebounces(t *testing.T) {
	root := t.TempDir()
	workspaceID := "ws-debounce"
	workspacePath := filepath.Join(root, workspaceID)
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	runner := &fakeRunner{
		resolver: func(cmd *exec.Cmd) ([]byte, error) {
			return []byte("ok"), nil
		},
	}
	service := newService(
		Config{
			WorkspaceRoot: root,
			Debounce:      25 * time.Millisecond,
			IndexTimeout:  2 * time.Second,
			AutoEmbed:     false,
		},
		slog.Default(),
		runner,
	)
	defer service.Close()

	service.QueueWorkspaceIndex(workspaceID)
	time.Sleep(8 * time.Millisecond)
	service.QueueWorkspaceIndex(workspaceID)
	time.Sleep(90 * time.Millisecond)

	calls := runner.callsSnapshot()
	updateCalls := 0
	for _, call := range calls {
		if strings.Contains(call, " update") {
			updateCalls++
		}
	}
	if updateCalls != 1 {
		t.Fatalf("expected one update call, got %d (all calls=%v)", updateCalls, calls)
	}
}

func TestStatusReturnsWorkspaceMetadata(t *testing.T) {
	root := t.TempDir()
	workspaceID := "ws-status"
	workspacePath := filepath.Join(root, workspaceID)
	if err := os.MkdirAll(filepath.Join(workspacePath, ".qmd", "cache", "qmd"), 0o755); err != nil {
		t.Fatalf("create qmd dirs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspacePath, ".qmd", "cache", "qmd", "index.sqlite"), []byte("db"), 0o644); err != nil {
		t.Fatalf("write index file: %v", err)
	}

	runner := &fakeRunner{
		resolver: func(cmd *exec.Cmd) ([]byte, error) {
			return []byte("collections: 1"), nil
		},
	}
	service := newService(
		Config{
			WorkspaceRoot: root,
		},
		slog.Default(),
		runner,
	)

	status, err := service.Status(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !status.WorkspaceExist {
		t.Fatal("expected workspace to exist")
	}
	if !status.IndexExists {
		t.Fatal("expected index file to exist")
	}
	if !strings.Contains(status.Summary, "collections") {
		t.Fatalf("expected qmd summary, got %q", status.Summary)
	}
}
