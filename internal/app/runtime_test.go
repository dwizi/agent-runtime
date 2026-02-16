package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dwizi/agent-runtime/internal/orchestrator"
	"github.com/dwizi/agent-runtime/internal/store"
)

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

func TestShouldQueueQMDForPath(t *testing.T) {
	root := "/data/workspaces"

	if !shouldQueueQMDForPath(root, "/data/workspaces/ws-1/docs/notes.md") {
		t.Fatal("expected docs markdown path to queue qmd indexing")
	}
	if shouldQueueQMDForPath(root, "/data/workspaces/ws-1/logs/chats/discord/123.md") {
		t.Fatal("expected chat log markdown path to skip qmd indexing")
	}
	if shouldQueueQMDForPath(root, "/data/workspaces/ws-1/.qmd/agent-runtime/index.md") {
		t.Fatal("expected qmd internal path to skip qmd indexing")
	}
	if shouldQueueQMDForPath(root, "/tmp/outside.md") {
		t.Fatal("expected out-of-root path to skip qmd indexing")
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

func TestHasPendingReindexTask(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "runtime_test.sqlite")
	sqlStore, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })
	if err := sqlStore.AutoMigrate(ctx); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	workspaceID := "ws-1"
	pending, err := hasPendingReindexTask(ctx, sqlStore, workspaceID)
	if err != nil {
		t.Fatalf("pending check failed: %v", err)
	}
	if pending {
		t.Fatal("expected no pending tasks in empty store")
	}

	if err := sqlStore.CreateTask(ctx, store.CreateTaskInput{
		ID:          "task-general-1",
		WorkspaceID: workspaceID,
		ContextID:   "ctx",
		Kind:        string(orchestrator.TaskKindGeneral),
		Title:       "general",
		Prompt:      "prompt",
		Status:      "queued",
	}); err != nil {
		t.Fatalf("create general task: %v", err)
	}
	pending, err = hasPendingReindexTask(ctx, sqlStore, workspaceID)
	if err != nil {
		t.Fatalf("pending check failed: %v", err)
	}
	if pending {
		t.Fatal("expected non-reindex task to be ignored")
	}

	if err := sqlStore.CreateTask(ctx, store.CreateTaskInput{
		ID:          "task-reindex-queued",
		WorkspaceID: workspaceID,
		ContextID:   "system:filewatcher",
		Kind:        string(orchestrator.TaskKindReindex),
		Title:       "Reindex markdown",
		Prompt:      "changed",
		Status:      "queued",
	}); err != nil {
		t.Fatalf("create queued reindex task: %v", err)
	}
	pending, err = hasPendingReindexTask(ctx, sqlStore, workspaceID)
	if err != nil {
		t.Fatalf("pending check failed: %v", err)
	}
	if !pending {
		t.Fatal("expected queued reindex task to be pending")
	}

	if err := sqlStore.MarkTaskCompleted(ctx, "task-reindex-queued", nowUTC(), "done", ""); err != nil {
		t.Fatalf("complete queued reindex task: %v", err)
	}
	if err := sqlStore.CreateTask(ctx, store.CreateTaskInput{
		ID:          "task-reindex-running",
		WorkspaceID: workspaceID,
		ContextID:   "system:filewatcher",
		Kind:        string(orchestrator.TaskKindReindex),
		Title:       "Reindex markdown",
		Prompt:      "changed",
		Status:      "queued",
	}); err != nil {
		t.Fatalf("create running reindex task: %v", err)
	}
	if err := sqlStore.MarkTaskRunning(ctx, "task-reindex-running", 1, nowUTC()); err != nil {
		t.Fatalf("mark running reindex task: %v", err)
	}
	pending, err = hasPendingReindexTask(ctx, sqlStore, workspaceID)
	if err != nil {
		t.Fatalf("pending check failed: %v", err)
	}
	if !pending {
		t.Fatal("expected running reindex task to be pending")
	}

	if err := sqlStore.MarkTaskCompleted(ctx, "task-reindex-running", nowUTC(), "done", ""); err != nil {
		t.Fatalf("complete running reindex task: %v", err)
	}
	pending, err = hasPendingReindexTask(ctx, sqlStore, workspaceID)
	if err != nil {
		t.Fatalf("pending check failed: %v", err)
	}
	if pending {
		t.Fatal("expected no pending reindex tasks after completion")
	}
}

func nowUTC() time.Time {
	return time.Now().UTC()
}
