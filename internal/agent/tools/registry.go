package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Registry manages a collection of tools.
type Registry struct {
	mu             sync.RWMutex
	tools          map[string]Tool
	toolNamespaces map[string]string
	namespaces     map[string]map[string]struct{}
}

func NewRegistry() *Registry {
	return &Registry{
		tools:          make(map[string]Tool),
		toolNamespaces: make(map[string]string),
		namespaces:     make(map[string]map[string]struct{}),
	}
}

// Register adds a tool to the registry. It overwrites any existing tool with the same name.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := t.Name()
	r.detachNameLocked(name)
	r.tools[name] = t
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

// ReplaceNamespace atomically replaces all tools registered under namespace.
func (r *Registry) ReplaceNamespace(namespace string, entries []Tool) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeNamespaceLocked(namespace)
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if name == "" {
			continue
		}
		r.detachNameLocked(name)
		r.tools[name] = entry
		r.toolNamespaces[name] = namespace
		if _, exists := r.namespaces[namespace]; !exists {
			r.namespaces[namespace] = map[string]struct{}{}
		}
		r.namespaces[namespace][name] = struct{}{}
	}
}

// RemoveNamespace removes every tool currently tracked under namespace.
func (r *Registry) RemoveNamespace(namespace string) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeNamespaceLocked(namespace)
}

func (r *Registry) detachNameLocked(name string) {
	if namespace, exists := r.toolNamespaces[name]; exists {
		delete(r.toolNamespaces, name)
		if names, ok := r.namespaces[namespace]; ok {
			delete(names, name)
			if len(names) == 0 {
				delete(r.namespaces, namespace)
			}
		}
	}
}

func (r *Registry) removeNamespaceLocked(namespace string) {
	names, exists := r.namespaces[namespace]
	if !exists {
		return
	}
	for name := range names {
		delete(r.tools, name)
		delete(r.toolNamespaces, name)
	}
	delete(r.namespaces, namespace)
}
