package app

import (
	"context"
	"io"
	"log/slog"
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

func TestShouldTriggerObjectiveEventForPath(t *testing.T) {
	root := "/data/workspaces"

	if !shouldTriggerObjectiveEventForPath(root, "/data/workspaces/ws-1/docs/notes.md") {
		t.Fatal("expected docs markdown path to trigger objective events")
	}
	if shouldTriggerObjectiveEventForPath(root, "/data/workspaces/ws-1/logs/chats/codex/session.md") {
		t.Fatal("expected chat log markdown path to skip objective event trigger")
	}
	if shouldTriggerObjectiveEventForPath(root, "/data/workspaces/ws-1/tasks/2026/02/17/task.md") {
		t.Fatal("expected task artifact markdown path to skip objective event trigger")
	}
	if shouldTriggerObjectiveEventForPath(root, "/data/workspaces/ws-1/ops/heartbeat.md") {
		t.Fatal("expected ops markdown path to skip objective event trigger")
	}
	if shouldTriggerObjectiveEventForPath(root, "/data/workspaces/ws-1/.qmd/cache/index.md") {
		t.Fatal("expected qmd internal markdown path to skip objective event trigger")
	}
	if shouldTriggerObjectiveEventForPath(root, "/tmp/outside.md") {
		t.Fatal("expected out-of-root path to skip objective event trigger")
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

type recoveryEngineStub struct {
	tasks []orchestrator.Task
}

func (s *recoveryEngineStub) Enqueue(task orchestrator.Task) (orchestrator.Task, error) {
	s.tasks = append(s.tasks, task)
	return task, nil
}

func TestRecoverPendingTasksEnqueuesQueuedAndStaleRunning(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "runtime_recovery_test.sqlite")
	sqlStore, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })
	if err := sqlStore.AutoMigrate(ctx); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	now := time.Now().UTC()
	insertTask := func(id, status string) {
		t.Helper()
		if err := sqlStore.CreateTask(ctx, store.CreateTaskInput{
			ID:          id,
			WorkspaceID: "ws-1",
			ContextID:   "ctx-1",
			Kind:        string(orchestrator.TaskKindGeneral),
			Title:       id,
			Prompt:      "run",
			Status:      status,
		}); err != nil {
			t.Fatalf("create task %s: %v", id, err)
		}
	}
	insertTask("task-queued", "queued")
	insertTask("task-running-stale", "queued")
	insertTask("task-running-fresh", "queued")
	if err := sqlStore.MarkTaskRunning(ctx, "task-running-stale", 1, now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("mark stale running: %v", err)
	}
	if err := sqlStore.MarkTaskRunning(ctx, "task-running-fresh", 1, now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("mark fresh running: %v", err)
	}

	engine := &recoveryEngineStub{}
	if err := recoverPendingTasks(ctx, sqlStore, engine, 10*time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("recover pending tasks: %v", err)
	}
	if len(engine.tasks) != 2 {
		t.Fatalf("expected 2 recovered tasks enqueued, got %d", len(engine.tasks))
	}

	stale, err := sqlStore.LookupTask(ctx, "task-running-stale")
	if err != nil {
		t.Fatalf("lookup stale task: %v", err)
	}
	if stale.Status != "queued" {
		t.Fatalf("expected stale running task to be requeued, got %s", stale.Status)
	}

	fresh, err := sqlStore.LookupTask(ctx, "task-running-fresh")
	if err != nil {
		t.Fatalf("lookup fresh task: %v", err)
	}
	if fresh.Status != "running" {
		t.Fatalf("expected fresh running task to remain running, got %s", fresh.Status)
	}
}

func TestRecoverStaleRunningTasksRequeuesOnlyStale(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "runtime_stale_recovery_test.sqlite")
	sqlStore, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })
	if err := sqlStore.AutoMigrate(ctx); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	now := time.Now().UTC()
	insertTask := func(id string) {
		t.Helper()
		if err := sqlStore.CreateTask(ctx, store.CreateTaskInput{
			ID:          id,
			WorkspaceID: "ws-1",
			ContextID:   "ctx-1",
			Kind:        string(orchestrator.TaskKindGeneral),
			Title:       id,
			Prompt:      "run",
			Status:      "queued",
		}); err != nil {
			t.Fatalf("create task %s: %v", id, err)
		}
	}
	insertTask("task-running-stale-loop")
	insertTask("task-running-fresh-loop")
	if err := sqlStore.MarkTaskRunning(ctx, "task-running-stale-loop", 11, now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("mark stale running: %v", err)
	}
	if err := sqlStore.MarkTaskRunning(ctx, "task-running-fresh-loop", 12, now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("mark fresh running: %v", err)
	}

	engine := &recoveryEngineStub{}
	requeued, err := recoverStaleRunningTasks(ctx, sqlStore, engine, 10*time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("recover stale running tasks: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("expected 1 stale task requeued, got %d", requeued)
	}
	if len(engine.tasks) != 1 || engine.tasks[0].ID != "task-running-stale-loop" {
		t.Fatalf("expected stale task enqueued once, got %+v", engine.tasks)
	}

	stale, err := sqlStore.LookupTask(ctx, "task-running-stale-loop")
	if err != nil {
		t.Fatalf("lookup stale task: %v", err)
	}
	if stale.Status != "queued" {
		t.Fatalf("expected stale task queued after recovery, got %s", stale.Status)
	}

	fresh, err := sqlStore.LookupTask(ctx, "task-running-fresh-loop")
	if err != nil {
		t.Fatalf("lookup fresh task: %v", err)
	}
	if fresh.Status != "running" {
		t.Fatalf("expected fresh task still running, got %s", fresh.Status)
	}
}

func TestStaleRecoveryLoopIntervalBounds(t *testing.T) {
	if got := staleRecoveryLoopInterval(0); got != 5*time.Minute {
		t.Fatalf("expected default interval 5m, got %s", got)
	}
	if got := staleRecoveryLoopInterval(20 * time.Second); got != 30*time.Second {
		t.Fatalf("expected minimum interval 30s, got %s", got)
	}
	if got := staleRecoveryLoopInterval(2 * time.Hour); got != 10*time.Minute {
		t.Fatalf("expected capped interval 10m, got %s", got)
	}
	if got := staleRecoveryLoopInterval(8 * time.Minute); got != 4*time.Minute {
		t.Fatalf("expected half stale interval 4m, got %s", got)
	}
}

func nowUTC() time.Time {
	return time.Now().UTC()
}
