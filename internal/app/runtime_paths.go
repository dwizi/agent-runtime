package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/dwizi/agent-runtime/internal/orchestrator"
	"github.com/dwizi/agent-runtime/internal/store"
)

func workspaceIDFromPath(workspaceRoot, changedPath string) string {
	root := filepath.Clean(strings.TrimSpace(workspaceRoot))
	path := filepath.Clean(strings.TrimSpace(changedPath))
	if root == "" || path == "" {
		return ""
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	if strings.HasPrefix(relative, "..") || relative == "." {
		return ""
	}
	parts := strings.Split(relative, string(os.PathSeparator))
	if len(parts) == 0 {
		return ""
	}
	workspaceID := strings.TrimSpace(parts[0])
	if workspaceID == "" || workspaceID == "." {
		return ""
	}
	return workspaceID
}

func shouldQueueQMDForPath(workspaceRoot, changedPath string) bool {
	workspaceRelative, ok := workspaceRelativeMarkdownPath(workspaceRoot, changedPath)
	if !ok {
		return false
	}
	if strings.HasPrefix(workspaceRelative, ".qmd/") {
		return false
	}
	if workspaceRelative == "logs" || strings.HasPrefix(workspaceRelative, "logs/") {
		return false
	}
	if workspaceRelative == "ops/heartbeat.md" {
		return false
	}
	return true
}

func shouldTriggerObjectiveEventForPath(workspaceRoot, changedPath string) bool {
	workspaceRelative, ok := workspaceRelativeMarkdownPath(workspaceRoot, changedPath)
	if !ok {
		return false
	}
	if strings.HasPrefix(workspaceRelative, ".qmd/") {
		return false
	}
	if workspaceRelative == "logs" || strings.HasPrefix(workspaceRelative, "logs/") {
		return false
	}
	if workspaceRelative == "tasks" || strings.HasPrefix(workspaceRelative, "tasks/") {
		return false
	}
	if workspaceRelative == "ops" || strings.HasPrefix(workspaceRelative, "ops/") {
		return false
	}
	return true
}

func workspaceRelativeMarkdownPath(workspaceRoot, changedPath string) (string, bool) {
	root := filepath.Clean(strings.TrimSpace(workspaceRoot))
	path := filepath.Clean(strings.TrimSpace(changedPath))
	if root == "" || path == "" {
		return "", false
	}
	if strings.ToLower(filepath.Ext(path)) != ".md" {
		return "", false
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return "", false
	}
	if strings.HasPrefix(relative, "..") || relative == "." {
		return "", false
	}
	relative = filepath.ToSlash(relative)
	separator := strings.Index(relative, "/")
	if separator < 0 || separator+1 >= len(relative) {
		return "", false
	}
	workspaceRelative := strings.ToLower(strings.TrimSpace(relative[separator+1:]))
	if workspaceRelative == "" {
		return "", false
	}
	return workspaceRelative, true
}

func hasPendingReindexTask(ctx context.Context, sqlStore *store.Store, workspaceID string) (bool, error) {
	if sqlStore == nil {
		return false, nil
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return false, nil
	}
	kind := string(orchestrator.TaskKindReindex)
	queued, err := sqlStore.ListTasks(ctx, store.ListTasksInput{
		WorkspaceID: workspaceID,
		Kind:        kind,
		Status:      "queued",
		Limit:       1,
	})
	if err != nil {
		return false, err
	}
	if len(queued) > 0 {
		return true, nil
	}
	running, err := sqlStore.ListTasks(ctx, store.ListTasksInput{
		WorkspaceID: workspaceID,
		Kind:        kind,
		Status:      "running",
		Limit:       1,
	})
	if err != nil {
		return false, err
	}
	return len(running) > 0, nil
}
