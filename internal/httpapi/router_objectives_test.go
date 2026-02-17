package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dwizi/agent-runtime/internal/config"
	"github.com/dwizi/agent-runtime/internal/orchestrator"
	"github.com/dwizi/agent-runtime/internal/store"
)

func TestObjectivesListUsesNullUnixForUnsetRuns(t *testing.T) {
	sqlStore := newRouterTestStore(t)
	ctx := context.Background()
	active := true
	_, err := sqlStore.CreateObjective(ctx, store.CreateObjectiveInput{
		WorkspaceID: "ws-1",
		ContextID:   "ctx-1",
		Title:       "React to markdown",
		Prompt:      "Inspect markdown changes",
		TriggerType: store.ObjectiveTriggerEvent,
		EventKey:    "markdown.updated",
		Active:      &active,
	})
	if err != nil {
		t.Fatalf("create objective: %v", err)
	}

	handler := NewRouter(Dependencies{
		Config: config.Config{},
		Store:  sqlStore,
		Engine: orchestrator.New(1, slog.New(slog.NewTextHandler(io.Discard, nil))),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/objectives?workspace_id=ws-1&active_only=false", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", res.Code)
	}

	var payload struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("expected one objective, got %d", len(payload.Items))
	}
	item := payload.Items[0]
	if item["next_run_unix"] != nil {
		t.Fatalf("expected next_run_unix to be null, got %#v", item["next_run_unix"])
	}
	if item["last_run_unix"] != nil {
		t.Fatalf("expected last_run_unix to be null, got %#v", item["last_run_unix"])
	}
	if item["timezone"] != "UTC" {
		t.Fatalf("expected default timezone UTC, got %#v", item["timezone"])
	}
	if _, ok := item["run_count"]; !ok {
		t.Fatal("expected run_count field in objective response")
	}
}
