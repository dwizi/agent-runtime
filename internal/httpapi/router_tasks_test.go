package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/carlos/spinner/internal/config"
	"github.com/carlos/spinner/internal/heartbeat"
	"github.com/carlos/spinner/internal/orchestrator"
	"github.com/carlos/spinner/internal/store"
)

func TestTasksListAndRetry(t *testing.T) {
	sqlStore := newRouterTestStore(t)
	ctx := context.Background()

	if err := sqlStore.CreateTask(ctx, store.CreateTaskInput{
		ID:          "task-failed",
		WorkspaceID: "ws-1",
		ContextID:   "ctx-1",
		Kind:        "general",
		Title:       "Failed task",
		Prompt:      "do thing",
		Status:      "queued",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := sqlStore.MarkTaskRunning(ctx, "task-failed", 1, time.Now().UTC()); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	if err := sqlStore.MarkTaskFailed(ctx, "task-failed", time.Now().UTC(), "boom"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}

	handler := NewRouter(Dependencies{
		Config: config.Config{},
		Store:  sqlStore,
		Engine: orchestrator.New(1, slog.New(slog.NewTextHandler(io.Discard, nil))),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?workspace_id=ws-1&status=failed", nil)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("expected status 200 for list, got %d", listRes.Code)
	}
	var listPayload struct {
		Items []map[string]any `json:"items"`
		Count int              `json:"count"`
	}
	if err := json.Unmarshal(listRes.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("decode list payload: %v", err)
	}
	if listPayload.Count != 1 {
		t.Fatalf("expected list count 1, got %d", listPayload.Count)
	}

	retryBody, _ := json.Marshal(map[string]string{"task_id": "task-failed"})
	retryReq := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/retry", bytes.NewReader(retryBody))
	retryReq.Header.Set("Content-Type", "application/json")
	retryRes := httptest.NewRecorder()
	handler.ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusAccepted {
		t.Fatalf("expected status 202 for retry, got %d, body=%s", retryRes.Code, retryRes.Body.String())
	}
	var retryPayload struct {
		TaskID string `json:"task_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(retryRes.Body.Bytes(), &retryPayload); err != nil {
		t.Fatalf("decode retry payload: %v", err)
	}
	if retryPayload.TaskID == "" || retryPayload.Status != "queued" {
		t.Fatalf("unexpected retry payload: %+v", retryPayload)
	}
}

func TestTaskRetryRejectsNonFailedTask(t *testing.T) {
	sqlStore := newRouterTestStore(t)
	ctx := context.Background()

	if err := sqlStore.CreateTask(ctx, store.CreateTaskInput{
		ID:          "task-ok",
		WorkspaceID: "ws-1",
		ContextID:   "ctx-1",
		Kind:        "general",
		Title:       "Queued task",
		Prompt:      "do thing",
		Status:      "queued",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	handler := NewRouter(Dependencies{
		Config: config.Config{},
		Store:  sqlStore,
		Engine: orchestrator.New(1, slog.New(slog.NewTextHandler(io.Discard, nil))),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	retryBody, _ := json.Marshal(map[string]string{"task_id": "task-ok"})
	retryReq := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/retry", bytes.NewReader(retryBody))
	retryReq.Header.Set("Content-Type", "application/json")
	retryRes := httptest.NewRecorder()
	handler.ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for non-failed retry, got %d", retryRes.Code)
	}
}

func TestHeartbeatEndpoint(t *testing.T) {
	sqlStore := newRouterTestStore(t)
	registry := heartbeat.NewRegistry()
	registry.Beat("scheduler", "ok")

	handler := NewRouter(Dependencies{
		Config:              config.Config{},
		Store:               sqlStore,
		Engine:              orchestrator.New(1, slog.New(slog.NewTextHandler(io.Discard, nil))),
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		Heartbeat:           registry,
		HeartbeatStaleAfter: 90 * time.Second,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/heartbeat", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", res.Code)
	}

	var payload struct {
		Overall    string `json:"overall"`
		Components []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"components"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode heartbeat payload: %v", err)
	}
	if payload.Overall == "" {
		t.Fatal("expected overall state")
	}
	if len(payload.Components) != 1 {
		t.Fatalf("expected one component, got %d", len(payload.Components))
	}
	if payload.Components[0].Name != "scheduler" {
		t.Fatalf("expected scheduler component, got %s", payload.Components[0].Name)
	}
}

func newRouterTestStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "router.sqlite")
	sqlStore, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })
	if err := sqlStore.AutoMigrate(context.Background()); err != nil {
		t.Fatalf("migrate test store: %v", err)
	}
	return sqlStore
}
