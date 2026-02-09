package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/carlos/spinner/internal/store"
)

var ErrPluginNotFound = errors.New("action plugin not found")

type Result struct {
	Plugin  string
	Message string
}

type Plugin interface {
	PluginKey() string
	ActionTypes() []string
	Execute(ctx context.Context, approval store.ActionApproval) (Result, error)
}

type Registry struct {
	plugins map[string]Plugin
}

func NewRegistry(plugins ...Plugin) *Registry {
	indexed := map[string]Plugin{}
	for _, plugin := range plugins {
		if plugin == nil {
			continue
		}
		for _, actionType := range plugin.ActionTypes() {
			key := normalizeActionType(actionType)
			if key == "" {
				continue
			}
			indexed[key] = plugin
		}
	}
	return &Registry{
		plugins: indexed,
	}
}

func (r *Registry) Execute(ctx context.Context, approval store.ActionApproval) (Result, error) {
	if r == nil {
		return Result{}, fmt.Errorf("%w: no registry configured", ErrPluginNotFound)
	}
	actionType := normalizeActionType(approval.ActionType)
	if actionType == "" {
		return Result{}, fmt.Errorf("%w: empty action type", ErrPluginNotFound)
	}
	plugin, ok := r.plugins[actionType]
	if !ok {
		return Result{}, fmt.Errorf("%w: %s", ErrPluginNotFound, actionType)
	}
	result, err := plugin.Execute(ctx, approval)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(result.Plugin) == "" {
		result.Plugin = plugin.PluginKey()
	}
	return result, nil
}

func normalizeActionType(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
