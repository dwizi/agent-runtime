package app

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/carlos/spinner/internal/connectors"
	"github.com/carlos/spinner/internal/orchestrator"
	"github.com/carlos/spinner/internal/store"
)

type publishedMessage struct {
	externalID string
	text       string
}

type fakePublisher struct {
	mu       sync.Mutex
	messages []publishedMessage
}

func (f *fakePublisher) Publish(ctx context.Context, externalID, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, publishedMessage{
		externalID: externalID,
		text:       text,
	})
	return nil
}

func TestTaskCompletionNotificationToTaskContext(t *testing.T) {
	sqlStore := openAppTestStore(t)
	ctx := context.Background()
	contextRecord, err := sqlStore.EnsureContextForExternalChannel(ctx, "telegram", "100", "community")
	if err != nil {
		t.Fatalf("ensure context: %v", err)
	}

	if err := sqlStore.CreateTask(ctx, store.CreateTaskInput{
		ID:          "task-n1",
		WorkspaceID: contextRecord.WorkspaceID,
		ContextID:   contextRecord.ID,
		Kind:        "general",
		Title:       "Notify target",
		Prompt:      "Write summary",
		Status:      "queued",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	publisher := &fakePublisher{}
	notifier := newTaskCompletionNotifier(sqlStore, map[string]connectors.Publisher{"telegram": publisher}, "both", "", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	observer := newTaskObserver(sqlStore, notifier, slog.New(slog.NewTextHandler(io.Discard, nil)))
	task := orchestrator.Task{
		ID:          "task-n1",
		WorkspaceID: contextRecord.WorkspaceID,
		ContextID:   contextRecord.ID,
		Kind:        orchestrator.TaskKindGeneral,
		Title:       "Notify target",
		Prompt:      "Write summary",
		CreatedAt:   time.Now().UTC(),
	}
	observer.OnTaskStarted(task, 1)
	observer.OnTaskCompleted(task, 1, orchestrator.TaskResult{
		Summary:      "done",
		ArtifactPath: "tasks/task-n1.md",
	})

	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	if len(publisher.messages) != 1 {
		t.Fatalf("expected one published message, got %d", len(publisher.messages))
	}
	if publisher.messages[0].externalID != "100" {
		t.Fatalf("expected publish to external id 100, got %s", publisher.messages[0].externalID)
	}
}

func TestTaskCompletionNotificationToAdminContextForSystemTasks(t *testing.T) {
	sqlStore := openAppTestStore(t)
	ctx := context.Background()
	adminContext, err := sqlStore.SetContextAdminByExternal(ctx, "telegram", "200", true)
	if err != nil {
		t.Fatalf("set admin context: %v", err)
	}

	if err := sqlStore.CreateTask(ctx, store.CreateTaskInput{
		ID:          "task-n2",
		WorkspaceID: adminContext.WorkspaceID,
		ContextID:   "system:filewatcher",
		Kind:        "reindex_markdown",
		Title:       "Reindex markdown",
		Prompt:      "file changed",
		Status:      "queued",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	publisher := &fakePublisher{}
	notifier := newTaskCompletionNotifier(sqlStore, map[string]connectors.Publisher{"telegram": publisher}, "both", "", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	observer := newTaskObserver(sqlStore, notifier, slog.New(slog.NewTextHandler(io.Discard, nil)))
	task := orchestrator.Task{
		ID:          "task-n2",
		WorkspaceID: adminContext.WorkspaceID,
		ContextID:   "system:filewatcher",
		Kind:        orchestrator.TaskKindReindex,
		Title:       "Reindex markdown",
		Prompt:      "file changed",
		CreatedAt:   time.Now().UTC(),
	}
	observer.OnTaskStarted(task, 1)
	observer.OnTaskCompleted(task, 1, orchestrator.TaskResult{
		Summary: "indexed",
	})

	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	if len(publisher.messages) != 1 {
		t.Fatalf("expected one published message, got %d", len(publisher.messages))
	}
	if publisher.messages[0].externalID != "200" {
		t.Fatalf("expected publish to external id 200, got %s", publisher.messages[0].externalID)
	}
}

func TestTaskCompletionNotificationPolicyOriginSkipsAdminContexts(t *testing.T) {
	sqlStore := openAppTestStore(t)
	ctx := context.Background()
	adminContext, err := sqlStore.SetContextAdminByExternal(ctx, "telegram", "300", true)
	if err != nil {
		t.Fatalf("set admin context: %v", err)
	}

	if err := sqlStore.CreateTask(ctx, store.CreateTaskInput{
		ID:          "task-n3",
		WorkspaceID: adminContext.WorkspaceID,
		ContextID:   "system:filewatcher",
		Kind:        "reindex_markdown",
		Title:       "Reindex markdown",
		Prompt:      "file changed",
		Status:      "queued",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	publisher := &fakePublisher{}
	notifier := newTaskCompletionNotifier(sqlStore, map[string]connectors.Publisher{"telegram": publisher}, "origin", "", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	observer := newTaskObserver(sqlStore, notifier, slog.New(slog.NewTextHandler(io.Discard, nil)))
	task := orchestrator.Task{
		ID:          "task-n3",
		WorkspaceID: adminContext.WorkspaceID,
		ContextID:   "system:filewatcher",
		Kind:        orchestrator.TaskKindReindex,
		Title:       "Reindex markdown",
		Prompt:      "file changed",
		CreatedAt:   time.Now().UTC(),
	}
	observer.OnTaskStarted(task, 1)
	observer.OnTaskCompleted(task, 1, orchestrator.TaskResult{
		Summary: "indexed",
	})

	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	if len(publisher.messages) != 0 {
		t.Fatalf("expected no published messages for origin-only policy, got %d", len(publisher.messages))
	}
}

func TestTaskCompletionNotificationPolicyAdminSkipsOriginContext(t *testing.T) {
	sqlStore := openAppTestStore(t)
	ctx := context.Background()
	contextRecord, err := sqlStore.EnsureContextForExternalChannel(ctx, "telegram", "400", "community")
	if err != nil {
		t.Fatalf("ensure context: %v", err)
	}

	if err := sqlStore.CreateTask(ctx, store.CreateTaskInput{
		ID:          "task-n4",
		WorkspaceID: contextRecord.WorkspaceID,
		ContextID:   contextRecord.ID,
		Kind:        "general",
		Title:       "Notify target",
		Prompt:      "Write summary",
		Status:      "queued",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	publisher := &fakePublisher{}
	notifier := newTaskCompletionNotifier(sqlStore, map[string]connectors.Publisher{"telegram": publisher}, "admin", "", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	observer := newTaskObserver(sqlStore, notifier, slog.New(slog.NewTextHandler(io.Discard, nil)))
	task := orchestrator.Task{
		ID:          "task-n4",
		WorkspaceID: contextRecord.WorkspaceID,
		ContextID:   contextRecord.ID,
		Kind:        orchestrator.TaskKindGeneral,
		Title:       "Notify target",
		Prompt:      "Write summary",
		CreatedAt:   time.Now().UTC(),
	}
	observer.OnTaskStarted(task, 1)
	observer.OnTaskCompleted(task, 1, orchestrator.TaskResult{
		Summary: "done",
	})

	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	if len(publisher.messages) != 0 {
		t.Fatalf("expected no published messages for admin-only policy without admin contexts, got %d", len(publisher.messages))
	}
}

func TestTaskCompletionNotificationPolicyOverridesByOutcome(t *testing.T) {
	sqlStore := openAppTestStore(t)
	ctx := context.Background()
	adminContext, err := sqlStore.SetContextAdminByExternal(ctx, "telegram", "500", true)
	if err != nil {
		t.Fatalf("set admin context: %v", err)
	}

	if err := sqlStore.CreateTask(ctx, store.CreateTaskInput{
		ID:          "task-n5",
		WorkspaceID: adminContext.WorkspaceID,
		ContextID:   "system:filewatcher",
		Kind:        "reindex_markdown",
		Title:       "Reindex markdown",
		Prompt:      "file changed",
		Status:      "queued",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := sqlStore.CreateTask(ctx, store.CreateTaskInput{
		ID:          "task-n6",
		WorkspaceID: adminContext.WorkspaceID,
		ContextID:   "system:filewatcher",
		Kind:        "reindex_markdown",
		Title:       "Reindex markdown",
		Prompt:      "file changed",
		Status:      "queued",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	publisher := &fakePublisher{}
	notifier := newTaskCompletionNotifier(
		sqlStore,
		map[string]connectors.Publisher{"telegram": publisher},
		"both",
		"origin",
		"admin",
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	observer := newTaskObserver(sqlStore, notifier, slog.New(slog.NewTextHandler(io.Discard, nil)))

	successTask := orchestrator.Task{
		ID:          "task-n5",
		WorkspaceID: adminContext.WorkspaceID,
		ContextID:   "system:filewatcher",
		Kind:        orchestrator.TaskKindReindex,
		Title:       "Reindex markdown",
		Prompt:      "file changed",
		CreatedAt:   time.Now().UTC(),
	}
	observer.OnTaskStarted(successTask, 1)
	observer.OnTaskCompleted(successTask, 1, orchestrator.TaskResult{Summary: "indexed"})

	failedTask := orchestrator.Task{
		ID:          "task-n6",
		WorkspaceID: adminContext.WorkspaceID,
		ContextID:   "system:filewatcher",
		Kind:        orchestrator.TaskKindReindex,
		Title:       "Reindex markdown",
		Prompt:      "file changed",
		CreatedAt:   time.Now().UTC(),
	}
	observer.OnTaskStarted(failedTask, 1)
	observer.OnTaskFailed(failedTask, 1, context.DeadlineExceeded)

	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	if len(publisher.messages) != 1 {
		t.Fatalf("expected one published failure notification, got %d", len(publisher.messages))
	}
	if publisher.messages[0].externalID != "500" {
		t.Fatalf("expected admin publish to external id 500, got %s", publisher.messages[0].externalID)
	}
}

func openAppTestStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "spinner_app.sqlite")
	sqlStore, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })
	if err := sqlStore.AutoMigrate(context.Background()); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	return sqlStore
}
