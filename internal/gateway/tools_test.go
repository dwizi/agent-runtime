package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dwizi/agent-runtime/internal/orchestrator"
	"github.com/dwizi/agent-runtime/internal/qmd"
	"github.com/dwizi/agent-runtime/internal/store"
)

// MockStore for testing tools
type MockStore struct {
	Store
	CreateTaskFunc      func(ctx context.Context, input store.CreateTaskInput) error
	CreateObjectiveFunc func(ctx context.Context, input store.CreateObjectiveInput) (store.Objective, error)
	UpdateObjectiveFunc func(ctx context.Context, input store.UpdateObjectiveInput) (store.Objective, error)
	LookupTaskFunc      func(ctx context.Context, id string) (store.TaskRecord, error)
	UpdateTaskRoutingFn func(ctx context.Context, input store.UpdateTaskRoutingInput) (store.TaskRecord, error)
	MarkTaskCompletedFn func(ctx context.Context, id string, finishedAt time.Time, summary, resultPath string) error
}

func (m *MockStore) CreateTask(ctx context.Context, input store.CreateTaskInput) error {
	if m.CreateTaskFunc != nil {
		return m.CreateTaskFunc(ctx, input)
	}
	return nil
}

func (m *MockStore) CreateObjective(ctx context.Context, input store.CreateObjectiveInput) (store.Objective, error) {
	if m.CreateObjectiveFunc != nil {
		return m.CreateObjectiveFunc(ctx, input)
	}
	return store.Objective{ID: "obj-1", Active: input.Active}, nil
}

func (m *MockStore) UpdateObjective(ctx context.Context, input store.UpdateObjectiveInput) (store.Objective, error) {
	if m.UpdateObjectiveFunc != nil {
		return m.UpdateObjectiveFunc(ctx, input)
	}
	return store.Objective{ID: input.ID, Active: true}, nil
}

func (m *MockStore) LookupTask(ctx context.Context, id string) (store.TaskRecord, error) {
	if m.LookupTaskFunc != nil {
		return m.LookupTaskFunc(ctx, id)
	}
	return store.TaskRecord{
		ID:           id,
		WorkspaceID:  "ws-1",
		ContextID:    "ctx-1",
		RouteClass:   "task",
		Priority:     "p3",
		AssignedLane: "operations",
	}, nil
}

func (m *MockStore) UpdateTaskRouting(ctx context.Context, input store.UpdateTaskRoutingInput) (store.TaskRecord, error) {
	if m.UpdateTaskRoutingFn != nil {
		return m.UpdateTaskRoutingFn(ctx, input)
	}
	return store.TaskRecord{
		ID:           input.ID,
		RouteClass:   input.RouteClass,
		Priority:     input.Priority,
		AssignedLane: input.AssignedLane,
		DueAt:        input.DueAt,
	}, nil
}

func (m *MockStore) MarkTaskCompleted(ctx context.Context, id string, finishedAt time.Time, summary, resultPath string) error {
	if m.MarkTaskCompletedFn != nil {
		return m.MarkTaskCompletedFn(ctx, id, finishedAt, summary, resultPath)
	}
	return nil
}

// MockEngine for testing tools
type MockEngine struct {
	Engine
	EnqueueFunc func(task orchestrator.Task) (orchestrator.Task, error)
}

func (m *MockEngine) Enqueue(task orchestrator.Task) (orchestrator.Task, error) {
	if m.EnqueueFunc != nil {
		return m.EnqueueFunc(task)
	}
	task.ID = "mock-task-id"
	return task, nil
}

// MockRetriever for testing tools
type MockRetriever struct {
	Retriever
	SearchFunc func(ctx context.Context, workspaceID, query string, limit int) ([]qmd.SearchResult, error)
}

func (m *MockRetriever) Search(ctx context.Context, workspaceID, query string, limit int) ([]qmd.SearchResult, error) {
	if m.SearchFunc != nil {
		return m.SearchFunc(ctx, workspaceID, query, limit)
	}
	return nil, nil
}

func TestSearchTool_Execute(t *testing.T) {
	mockRetriever := &MockRetriever{
		SearchFunc: func(ctx context.Context, workspaceID, query string, limit int) ([]qmd.SearchResult, error) {
			if query != "test query" {
				t.Errorf("expected query 'test query', got '%s'", query)
			}
			return []qmd.SearchResult{{Path: "doc.md", Snippet: "found it"}}, nil
		},
	}

	tool := NewSearchTool(mockRetriever)
	ctx := context.WithValue(context.Background(), contextKeyRecord, store.ContextRecord{WorkspaceID: "ws-1", ID: "ctx-1"})
	ctx = context.WithValue(ctx, contextKeyInput, MessageInput{Text: "question"})

	out, err := tool.Execute(ctx, json.RawMessage(`{"query": "test query"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Error("expected output, got empty string")
	}
}

func TestCreateTaskTool_Execute(t *testing.T) {
	mockStore := &MockStore{
		CreateTaskFunc: func(ctx context.Context, input store.CreateTaskInput) error {
			if input.Title != "Fix Bug" {
				t.Errorf("expected title 'Fix Bug', got '%s'", input.Title)
			}
			return nil
		},
	}
	mockEngine := &MockEngine{}

	tool := NewCreateTaskTool(mockStore, mockEngine)
	ctx := context.WithValue(context.Background(), contextKeyRecord, store.ContextRecord{WorkspaceID: "ws-1"})
	ctx = context.WithValue(ctx, contextKeyInput, MessageInput{Text: "original message"})

	out, err := tool.Execute(ctx, json.RawMessage(`{"title": "Fix Bug", "description": "It is broken", "priority": "p1"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Error("expected output")
	}
}

func TestSearchTool_ValidateRejectsUnknownField(t *testing.T) {
	tool := NewSearchTool(&MockRetriever{})
	err := tool.ValidateArgs(json.RawMessage(`{"query":"hello","extra":1}`))
	if err == nil {
		t.Fatal("expected strict schema validation error")
	}
}

func TestModerationTriageTool_Execute(t *testing.T) {
	tool := NewModerationTriageTool()
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"message":"this user made a bomb threat","channel":"general"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "critical") {
		t.Fatalf("expected critical severity, got %q", out)
	}
}

func TestCreateObjectiveTool_Execute(t *testing.T) {
	created := false
	mockStore := &MockStore{
		CreateObjectiveFunc: func(ctx context.Context, input store.CreateObjectiveInput) (store.Objective, error) {
			created = true
			if input.IntervalSeconds < 60 {
				t.Fatalf("expected sensible interval, got %d", input.IntervalSeconds)
			}
			return store.Objective{ID: "obj-1", Active: input.Active}, nil
		},
	}
	tool := NewCreateObjectiveTool(mockStore)
	ctx := context.WithValue(context.Background(), contextKeyRecord, store.ContextRecord{WorkspaceID: "ws-1", ID: "ctx-1"})
	ctx = context.WithValue(ctx, contextKeyInput, MessageInput{Text: "monitor this"})

	out, err := tool.Execute(ctx, json.RawMessage(`{"title":"Watch spam","prompt":"Track repeated spam","active":true}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !created {
		t.Fatal("expected objective creation")
	}
	if !strings.Contains(strings.ToLower(out), "objective created") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestUpdateObjectiveTool_Execute(t *testing.T) {
	updated := false
	mockStore := &MockStore{
		UpdateObjectiveFunc: func(ctx context.Context, input store.UpdateObjectiveInput) (store.Objective, error) {
			updated = true
			if strings.TrimSpace(input.ID) != "obj-1" {
				t.Fatalf("expected objective id obj-1, got %q", input.ID)
			}
			return store.Objective{ID: "obj-1", Active: false}, nil
		},
	}
	tool := NewUpdateObjectiveTool(mockStore)

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"objective_id":"obj-1","active":false}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !updated {
		t.Fatal("expected objective update")
	}
	if !strings.Contains(strings.ToLower(out), "objective updated") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestUpdateTaskTool_Close(t *testing.T) {
	closed := false
	mockStore := &MockStore{
		MarkTaskCompletedFn: func(ctx context.Context, id string, finishedAt time.Time, summary, resultPath string) error {
			closed = true
			if id != "task-1" {
				t.Fatalf("expected task id task-1, got %q", id)
			}
			return nil
		},
	}
	tool := NewUpdateTaskTool(mockStore)

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"task_id":"task-1","status":"closed","summary":"resolved"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !closed {
		t.Fatal("expected task closure")
	}
	if !strings.Contains(strings.ToLower(out), "task closed") {
		t.Fatalf("unexpected output: %q", out)
	}
}
