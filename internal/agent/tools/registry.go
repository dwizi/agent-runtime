package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// Registry manages a collection of tools.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry. It overwrites any existing tool with the same name.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// Get retrieves a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns a list of all registered tools, sorted by name.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		list = append(list, t)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Name() < list[j].Name()
	})
	return list
}

// ExecuteTool finds a tool by name and executes it with the provided raw JSON arguments.
func (r *Registry) ExecuteTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	tool, exists := r.Get(name)
	if !exists {
		return "", fmt.Errorf("tool not found: %s", name)
	}
	if validator, ok := tool.(ArgumentValidator); ok {
		if err := validator.ValidateArgs(args); err != nil {
			return "", fmt.Errorf("invalid args for %s: %w", name, err)
		}
	}
	return tool.Execute(ctx, args)
}

// DescribeAll returns a formatted string describing all available tools for the LLM system prompt.
func (r *Registry) DescribeAll() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Sort for deterministic output
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	var output string
	for _, name := range names {
		tool := r.tools[name]
		output += fmt.Sprintf("- %s: %s\n  Schema: %s\n", tool.Name(), tool.Description(), tool.ParametersSchema())
	}
	return output
}
