package executor

import (
	"context"
	"errors"
	"testing"

	"github.com/carlos/spinner/internal/store"
)

type fakePlugin struct {
	key    string
	types  []string
	result Result
	err    error
}

func (f *fakePlugin) PluginKey() string {
	return f.key
}

func (f *fakePlugin) ActionTypes() []string {
	return f.types
}

func (f *fakePlugin) Execute(ctx context.Context, approval store.ActionApproval) (Result, error) {
	if f.err != nil {
		return Result{}, f.err
	}
	return f.result, nil
}

func TestRegistryExecutesPlugin(t *testing.T) {
	registry := NewRegistry(&fakePlugin{
		key:   "fake",
		types: []string{"send_email"},
		result: Result{
			Message: "ok",
		},
	})
	result, err := registry.Execute(context.Background(), store.ActionApproval{
		ActionType: "send_email",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if result.Plugin != "fake" {
		t.Fatalf("expected plugin fake, got %s", result.Plugin)
	}
}

func TestRegistryReturnsNotFound(t *testing.T) {
	registry := NewRegistry()
	_, err := registry.Execute(context.Background(), store.ActionApproval{
		ActionType: "unknown",
	})
	if !errors.Is(err, ErrPluginNotFound) {
		t.Fatalf("expected not found error, got %v", err)
	}
}
