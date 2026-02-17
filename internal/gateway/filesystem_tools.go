package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dwizi/agent-runtime/internal/store"
)

// WriteFileTool writes content to a file in the workspace scratchpad.
type WriteFileTool struct {
	workspaceRoot string
}

func NewWriteFileTool(workspaceRoot string) *WriteFileTool {
	return &WriteFileTool{workspaceRoot: workspaceRoot}
}

func (t *WriteFileTool) Name() string { return "write_file" }

func (t *WriteFileTool) Description() string {
	return "Write text content to a file in the workspace scratchpad. Overwrites if exists."
}

func (t *WriteFileTool) ParametersSchema() string {
	return `{"path": "string (relative)", "content": "string"}`
}

func (t *WriteFileTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	record, ok := ctx.Value(ContextKeyRecord).(store.ContextRecord)
	if !ok {
		return "", fmt.Errorf("internal error: context record missing from context")
	}

	fullPath, err := resolveScratchPath(t.workspaceRoot, record.WorkspaceID, args.Path)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", fmt.Errorf("create directory: %w", err)
	}

	if err := os.WriteFile(fullPath, []byte(args.Content), 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return fmt.Sprintf("Wrote %d bytes to %s", len(args.Content), args.Path), nil
}

// ReadFileTool reads content from a file in the workspace scratchpad.
type ReadFileTool struct {
	workspaceRoot string
}

func NewReadFileTool(workspaceRoot string) *ReadFileTool {
	return &ReadFileTool{workspaceRoot: workspaceRoot}
}

func (t *ReadFileTool) Name() string { return "read_file" }

func (t *ReadFileTool) Description() string {
	return "Read text content from a file in the workspace scratchpad."
}

func (t *ReadFileTool) ParametersSchema() string {
	return `{"path": "string (relative)"}`
}

func (t *ReadFileTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	record, ok := ctx.Value(ContextKeyRecord).(store.ContextRecord)
	if !ok {
		return "", fmt.Errorf("internal error: context record missing from context")
	}

	fullPath, err := resolveScratchPath(t.workspaceRoot, record.WorkspaceID, args.Path)
	if err != nil {
		return "", err
	}

	content, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found: %s", args.Path)
		}
		return "", fmt.Errorf("read file: %w", err)
	}

	return string(content), nil
}

// ListFilesTool lists files in the workspace scratchpad.
type ListFilesTool struct {
	workspaceRoot string
}

func NewListFilesTool(workspaceRoot string) *ListFilesTool {
	return &ListFilesTool{workspaceRoot: workspaceRoot}
}

func (t *ListFilesTool) Name() string { return "list_files" }

func (t *ListFilesTool) Description() string {
	return "List files in the workspace scratchpad directory."
}

func (t *ListFilesTool) ParametersSchema() string {
	return `{"path": "string (optional relative subdir)"}`
}

func (t *ListFilesTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(rawArgs, &args) // Ignore error, optional args

	record, ok := ctx.Value(ContextKeyRecord).(store.ContextRecord)
	if !ok {
		return "", fmt.Errorf("internal error: context record missing from context")
	}

	targetDir, err := resolveScratchPath(t.workspaceRoot, record.WorkspaceID, args.Path)
	if err != nil {
		return "", err
	}

	entries, err := os.ReadDir(targetDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "Directory not found.", nil
		}
		return "", fmt.Errorf("read dir: %w", err)
	}

	if len(entries) == 0 {
		return "No files found.", nil
	}

	var lines []string
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		suffix := ""
		if entry.IsDir() {
			suffix = "/"
		}
		lines = append(lines, fmt.Sprintf("%s%s (%d bytes)", entry.Name(), suffix, info.Size()))
	}

	return strings.Join(lines, "\n"), nil
}

func resolveScratchPath(root, workspaceID, relPath string) (string, error) {
	if strings.Contains(relPath, "..") {
		return "", fmt.Errorf("invalid path: traversal not allowed")
	}
	cleanRel := filepath.Clean(strings.TrimSpace(relPath))
	if filepath.IsAbs(cleanRel) {
		return "", fmt.Errorf("invalid path: absolute paths not allowed")
	}
	
	scratchDir := filepath.Join(root, workspaceID, "scratch")
	fullPath := filepath.Join(scratchDir, cleanRel)
	
	// Double check we are still inside scratchDir
	if !strings.HasPrefix(fullPath, scratchDir) {
		return "", fmt.Errorf("invalid path: outside scratch directory")
	}
	
	return fullPath, nil
}
