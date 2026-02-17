package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dwizi/agent-runtime/internal/store"
)

func TestWriteFileTool(t *testing.T) {
	tempDir := t.TempDir()
	tool := NewWriteFileTool(tempDir)

	ctx := context.WithValue(context.Background(), ContextKeyRecord, store.ContextRecord{
		WorkspaceID: "ws1",
	})

	t.Run("writes file successfully", func(t *testing.T) {
		args := json.RawMessage(`{"path": "test.txt", "content": "hello world"}`)
		res, err := tool.Execute(ctx, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(res, "Wrote 11 bytes to test.txt") {
			t.Errorf("unexpected response: %s", res)
		}

		content, err := os.ReadFile(filepath.Join(tempDir, "ws1", "scratch", "test.txt"))
		if err != nil {
			t.Fatalf("unexpected error reading file: %v", err)
		}
		if string(content) != "hello world" {
			t.Errorf("expected 'hello world', got '%s'", string(content))
		}
	})

	t.Run("overwrites file", func(t *testing.T) {
		args := json.RawMessage(`{"path": "test.txt", "content": "new content"}`)
		res, err := tool.Execute(ctx, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(res, "Wrote 11 bytes to test.txt") {
			t.Errorf("unexpected response: %s", res)
		}

		content, err := os.ReadFile(filepath.Join(tempDir, "ws1", "scratch", "test.txt"))
		if err != nil {
			t.Fatalf("unexpected error reading file: %v", err)
		}
		if string(content) != "new content" {
			t.Errorf("expected 'new content', got '%s'", string(content))
		}
	})

	t.Run("creates subdirectories", func(t *testing.T) {
		args := json.RawMessage(`{"path": "subdir/nested.txt", "content": "deep"}`)
		res, err := tool.Execute(ctx, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(res, "Wrote 4 bytes to subdir/nested.txt") {
			t.Errorf("unexpected response: %s", res)
		}

		content, err := os.ReadFile(filepath.Join(tempDir, "ws1", "scratch", "subdir", "nested.txt"))
		if err != nil {
			t.Fatalf("unexpected error reading file: %v", err)
		}
		if string(content) != "deep" {
			t.Errorf("expected 'deep', got '%s'", string(content))
		}
	})

	t.Run("prevents path traversal", func(t *testing.T) {
		args := json.RawMessage(`{"path": "../outside.txt", "content": "bad"}`)
		_, err := tool.Execute(ctx, args)
		if err == nil {
			t.Fatal("expected error for path traversal")
		}
		if !strings.Contains(err.Error(), "traversal not allowed") {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}

func TestReadFileTool(t *testing.T) {
	tempDir := t.TempDir()
	tool := NewReadFileTool(tempDir)
	
	// Setup a file
	wsDir := filepath.Join(tempDir, "ws1", "scratch")
	os.MkdirAll(wsDir, 0o755)
	os.WriteFile(filepath.Join(wsDir, "read.txt"), []byte("read me"), 0o644)

	ctx := context.WithValue(context.Background(), ContextKeyRecord, store.ContextRecord{
		WorkspaceID: "ws1",
	})

	t.Run("reads file successfully", func(t *testing.T) {
		args := json.RawMessage(`{"path": "read.txt"}`)
		res, err := tool.Execute(ctx, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res != "read me" {
			t.Errorf("expected 'read me', got '%s'", res)
		}
	})

	t.Run("returns error for missing file", func(t *testing.T) {
		args := json.RawMessage(`{"path": "missing.txt"}`)
		_, err := tool.Execute(ctx, args)
		if err == nil {
			t.Fatal("expected error for missing file")
		}
		if !strings.Contains(err.Error(), "file not found") {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("prevents path traversal", func(t *testing.T) {
		args := json.RawMessage(`{"path": "../secret.txt"}`)
		_, err := tool.Execute(ctx, args)
		if err == nil {
			t.Fatal("expected error for path traversal")
		}
		if !strings.Contains(err.Error(), "traversal not allowed") {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}

func TestListFilesTool(t *testing.T) {
	tempDir := t.TempDir()
	tool := NewListFilesTool(tempDir)

	wsDir := filepath.Join(tempDir, "ws1", "scratch")
	os.MkdirAll(wsDir, 0o755)
	os.WriteFile(filepath.Join(wsDir, "file1.txt"), []byte("1"), 0o644)
	os.Mkdir(filepath.Join(wsDir, "subdir"), 0o755)

	ctx := context.WithValue(context.Background(), ContextKeyRecord, store.ContextRecord{
		WorkspaceID: "ws1",
	})

	t.Run("lists files", func(t *testing.T) {
		args := json.RawMessage(`{}`)
		res, err := tool.Execute(ctx, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(res, "file1.txt") {
			t.Errorf("expected output to contain 'file1.txt', got: %s", res)
		}
		if !strings.Contains(res, "subdir") {
			t.Errorf("expected output to contain 'subdir', got: %s", res)
		}
	})
}
