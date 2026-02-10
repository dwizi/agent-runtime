package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/carlos/spinner/internal/orchestrator"
	"github.com/carlos/spinner/internal/qmd"
	"github.com/carlos/spinner/internal/store"
)

// MockStore for testing tools
type MockStore struct {
	Store
	CreateTaskFunc func(ctx context.Context, input store.CreateTaskInput) error
}

func (m *MockStore) CreateTask(ctx context.Context, input store.CreateTaskInput) error {
	if m.CreateTaskFunc != nil {
		return m.CreateTaskFunc(ctx, input)
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
	ctx := context.WithValue(context.Background(), contextKeyRecord, store.ContextRecord{WorkspaceID: "ws-1"})
	
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
