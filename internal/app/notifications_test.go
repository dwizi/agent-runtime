package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
	notifier := newTaskCompletionNotifier("", sqlStore, map[string]connectors.Publisher{"telegram": publisher}, "both", "", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
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

func TestTaskCompletionNotificationAppendsOutboundChatLog(t *testing.T) {
	workspaceRoot := t.TempDir()
	sqlStore := openAppTestStore(t)
	ctx := context.Background()
	contextRecord, err := sqlStore.EnsureContextForExternalChannel(ctx, "telegram", "101", "community")
	if err != nil {
		t.Fatalf("ensure context: %v", err)
	}

	if err := sqlStore.CreateTask(ctx, store.CreateTaskInput{
		ID:          "task-log-1",
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
	notifier := newTaskCompletionNotifier(workspaceRoot, sqlStore, map[string]connectors.Publisher{"telegram": publisher}, "both", "", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	observer := newTaskObserver(sqlStore, notifier, slog.New(slog.NewTextHandler(io.Discard, nil)))
	task := orchestrator.Task{
		ID:          "task-log-1",
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

	logPath := filepath.Join(workspaceRoot, contextRecord.WorkspaceID, "logs", "chats", "telegram", "101.md")
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, "`OUTBOUND`") {
		t.Fatalf("expected outbound log entry, got %s", text)
	}
	if !strings.Contains(text, "Notify target") {
		t.Fatalf("expected task completion text in outbound log, got %s", text)
	}
}

func TestRoutedTaskSuccessNotificationUsesNaturalReply(t *testing.T) {
	sqlStore := openAppTestStore(t)
	ctx := context.Background()
	contextRecord, err := sqlStore.EnsureContextForExternalChannel(ctx, "telegram", "110", "community")
	if err != nil {
		t.Fatalf("ensure context: %v", err)
	}

	if err := sqlStore.CreateTask(ctx, store.CreateTaskInput{
		ID:               "task-routed-success",
		WorkspaceID:      contextRecord.WorkspaceID,
		ContextID:        contextRecord.ID,
		Kind:             "general",
		Title:            "Routed question",
		Prompt:           "Who am I?",
		Status:           "queued",
		RouteClass:       "question",
		Priority:         "p3",
		AssignedLane:     "support",
		SourceConnector:  "telegram",
		SourceExternalID: "110",
		SourceUserID:     "u1",
		SourceText:       "Who am I?",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	publisher := &fakePublisher{}
	notifier := newTaskCompletionNotifier("", sqlStore, map[string]connectors.Publisher{"telegram": publisher}, "both", "", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	observer := newTaskObserver(sqlStore, notifier, slog.New(slog.NewTextHandler(io.Discard, nil)))
	task := orchestrator.Task{
		ID:          "task-routed-success",
		WorkspaceID: contextRecord.WorkspaceID,
		ContextID:   contextRecord.ID,
		Kind:        orchestrator.TaskKindGeneral,
		Title:       "Routed question",
		Prompt:      "Who am I?",
		CreatedAt:   time.Now().UTC(),
	}
	observer.OnTaskStarted(task, 1)
	observer.OnTaskCompleted(task, 1, orchestrator.TaskResult{
		Summary: "You're Carlos.",
	})

	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	if len(publisher.messages) != 2 {
		t.Fatalf("expected two published messages, got %d", len(publisher.messages))
	}
	if publisher.messages[0].externalID != "110" {
		t.Fatalf("expected in-progress publish to external id 110, got %s", publisher.messages[0].externalID)
	}
	if !strings.Contains(strings.ToLower(publisher.messages[0].text), "still working") {
		t.Fatalf("expected in-progress lifecycle update, got %q", publisher.messages[0].text)
	}
	if publisher.messages[1].externalID != "110" {
		t.Fatalf("expected completion publish to external id 110, got %s", publisher.messages[1].externalID)
	}
	if publisher.messages[1].text != "You're Carlos." {
		t.Fatalf("expected natural routed reply, got %q", publisher.messages[1].text)
	}
}

func TestRoutedTaskFailureSkipsNonAdminChannels(t *testing.T) {
	sqlStore := openAppTestStore(t)
	ctx := context.Background()
	contextRecord, err := sqlStore.EnsureContextForExternalChannel(ctx, "telegram", "120", "community")
	if err != nil {
		t.Fatalf("ensure context: %v", err)
	}

	if err := sqlStore.CreateTask(ctx, store.CreateTaskInput{
		ID:               "task-routed-failure",
		WorkspaceID:      contextRecord.WorkspaceID,
		ContextID:        contextRecord.ID,
		Kind:             "general",
		Title:            "Routed failure",
		Prompt:           "help",
		Status:           "queued",
		RouteClass:       "question",
		Priority:         "p3",
		AssignedLane:     "support",
		SourceConnector:  "telegram",
		SourceExternalID: "120",
		SourceUserID:     "u2",
		SourceText:       "help",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	publisher := &fakePublisher{}
	notifier := newTaskCompletionNotifier("", sqlStore, map[string]connectors.Publisher{"telegram": publisher}, "both", "", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	observer := newTaskObserver(sqlStore, notifier, slog.New(slog.NewTextHandler(io.Discard, nil)))
	task := orchestrator.Task{
		ID:          "task-routed-failure",
		WorkspaceID: contextRecord.WorkspaceID,
		ContextID:   contextRecord.ID,
		Kind:        orchestrator.TaskKindGeneral,
		Title:       "Routed failure",
		Prompt:      "help",
		CreatedAt:   time.Now().UTC(),
	}
	observer.OnTaskStarted(task, 1)
	observer.OnTaskFailed(task, 1, context.DeadlineExceeded)

	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	if len(publisher.messages) != 1 {
		t.Fatalf("expected one in-progress routed message in non-admin channels, got %d", len(publisher.messages))
	}
	if publisher.messages[0].externalID != "120" {
		t.Fatalf("expected in-progress publish to origin channel 120, got %s", publisher.messages[0].externalID)
	}
	if !strings.Contains(strings.ToLower(publisher.messages[0].text), "still working") {
		t.Fatalf("expected in-progress lifecycle text, got %q", publisher.messages[0].text)
	}
}

func TestRoutedTaskFailureNotifiesAdminChannelOnly(t *testing.T) {
	sqlStore := openAppTestStore(t)
	ctx := context.Background()
	originContext, err := sqlStore.EnsureContextForExternalChannel(ctx, "telegram", "chan-a", "origin")
	if err != nil {
		t.Fatalf("ensure origin context: %v", err)
	}
	adminContext, err := sqlStore.SetContextAdminByExternal(ctx, "telegram", "CHAN-A", true)
	if err != nil {
		t.Fatalf("set admin context: %v", err)
	}
	if originContext.WorkspaceID != adminContext.WorkspaceID {
		t.Fatalf("expected shared workspace for test setup, got %s and %s", originContext.WorkspaceID, adminContext.WorkspaceID)
	}

	if err := sqlStore.CreateTask(ctx, store.CreateTaskInput{
		ID:               "task-routed-admin-failure",
		WorkspaceID:      originContext.WorkspaceID,
		ContextID:        originContext.ID,
		Kind:             "general",
		Title:            "Routed admin failure",
		Prompt:           "help",
		Status:           "queued",
		RouteClass:       "issue",
		Priority:         "p2",
		AssignedLane:     "operations",
		SourceConnector:  "telegram",
		SourceExternalID: "chan-a",
		SourceUserID:     "u3",
		SourceText:       "help",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	publisher := &fakePublisher{}
	notifier := newTaskCompletionNotifier("", sqlStore, map[string]connectors.Publisher{"telegram": publisher}, "both", "", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	observer := newTaskObserver(sqlStore, notifier, slog.New(slog.NewTextHandler(io.Discard, nil)))
	task := orchestrator.Task{
		ID:          "task-routed-admin-failure",
		WorkspaceID: originContext.WorkspaceID,
		ContextID:   originContext.ID,
		Kind:        orchestrator.TaskKindGeneral,
		Title:       "Routed admin failure",
		Prompt:      "help",
		CreatedAt:   time.Now().UTC(),
	}
	observer.OnTaskStarted(task, 1)
	observer.OnTaskFailed(task, 1, context.DeadlineExceeded)

	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	if len(publisher.messages) != 2 {
		t.Fatalf("expected one in-progress message plus one admin failure message, got %d", len(publisher.messages))
	}
	if publisher.messages[0].externalID != "chan-a" {
		t.Fatalf("expected in-progress publish to origin channel chan-a, got %s", publisher.messages[0].externalID)
	}
	if publisher.messages[1].externalID != "CHAN-A" {
		t.Fatalf("expected publish to admin channel CHAN-A, got %s", publisher.messages[1].externalID)
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
	notifier := newTaskCompletionNotifier("", sqlStore, map[string]connectors.Publisher{"telegram": publisher}, "both", "", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	notifier := newTaskCompletionNotifier("", sqlStore, map[string]connectors.Publisher{"telegram": publisher}, "origin", "", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	notifier := newTaskCompletionNotifier("", sqlStore, map[string]connectors.Publisher{"telegram": publisher}, "admin", "", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
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
		"",
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
