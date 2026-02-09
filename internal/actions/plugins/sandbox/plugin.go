package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/carlos/spinner/internal/actions/executor"
	"github.com/carlos/spinner/internal/store"
)

type Config struct {
	Enabled         bool
	WorkspaceRoot   string
	AllowedCommands []string
	Timeout         time.Duration
}

type Plugin struct {
	enabled       bool
	workspaceRoot string
	allowed       map[string]struct{}
	timeout       time.Duration
}

func New(cfg Config) *Plugin {
	allowed := map[string]struct{}{}
	for _, item := range cfg.AllowedCommands {
		key := strings.ToLower(strings.TrimSpace(item))
		if key == "" {
			continue
		}
		allowed[key] = struct{}{}
	}
	timeout := cfg.Timeout
	if timeout < time.Second {
		timeout = 20 * time.Second
	}
	return &Plugin{
		enabled:       cfg.Enabled,
		workspaceRoot: filepath.Clean(strings.TrimSpace(cfg.WorkspaceRoot)),
		allowed:       allowed,
		timeout:       timeout,
	}
}

func (p *Plugin) PluginKey() string {
	return "sandbox_command"
}

func (p *Plugin) ActionTypes() []string {
	return []string{"run_command", "shell_command", "cli_command"}
}

func (p *Plugin) Execute(ctx context.Context, approval store.ActionApproval) (executor.Result, error) {
	if p == nil || !p.enabled {
		return executor.Result{}, fmt.Errorf("sandbox command execution is disabled")
	}
	command, args, err := parseCommand(approval)
	if err != nil {
		return executor.Result{}, err
	}
	if !p.isAllowed(command) {
		return executor.Result{}, fmt.Errorf("command %q is not allowed", command)
	}
	workdir, err := p.resolveWorkingDir(approval)
	if err != nil {
		return executor.Result{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, command, args...)
	cmd.Dir = workdir
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return executor.Result{}, fmt.Errorf("command failed: %w; output=%s", err, compactOutput(output.String()))
	}
	return executor.Result{
		Plugin:  p.PluginKey(),
		Message: "command succeeded: " + compactOutput(output.String()),
	}, nil
}

func (p *Plugin) isAllowed(command string) bool {
	key := strings.ToLower(strings.TrimSpace(filepath.Base(command)))
	_, ok := p.allowed[key]
	return ok
}

func (p *Plugin) resolveWorkingDir(approval store.ActionApproval) (string, error) {
	workspaceRoot := filepath.Clean(filepath.Join(p.workspaceRoot, approval.WorkspaceID))
	if strings.TrimSpace(approval.WorkspaceID) == "" {
		return "", fmt.Errorf("workspace id is required for sandbox command")
	}
	cwd := getString(approval.Payload, "cwd")
	if strings.TrimSpace(cwd) == "" {
		return workspaceRoot, nil
	}
	var resolved string
	if filepath.IsAbs(cwd) {
		resolved = filepath.Clean(cwd)
	} else {
		resolved = filepath.Clean(filepath.Join(workspaceRoot, cwd))
	}
	if !isWithin(resolved, workspaceRoot) {
		return "", fmt.Errorf("cwd escapes workspace boundary")
	}
	return resolved, nil
}

func parseCommand(approval store.ActionApproval) (string, []string, error) {
	command := strings.TrimSpace(approval.ActionTarget)
	if command == "" {
		command = getString(approval.Payload, "command")
	}
	if command == "" {
		return "", nil, fmt.Errorf("command action requires target or payload.command")
	}
	args := []string{}
	if rawArgs, ok := approval.Payload["args"]; ok && rawArgs != nil {
		parsed, err := parseArgs(rawArgs)
		if err != nil {
			return "", nil, err
		}
		args = append(args, parsed...)
	}
	return command, args, nil
}

func parseArgs(value any) ([]string, error) {
	switch casted := value.(type) {
	case []string:
		return casted, nil
	case []any:
		args := make([]string, 0, len(casted))
		for _, raw := range casted {
			args = append(args, strings.TrimSpace(fmt.Sprintf("%v", raw)))
		}
		return args, nil
	case string:
		trimmed := strings.TrimSpace(casted)
		if trimmed == "" {
			return nil, nil
		}
		return strings.Fields(trimmed), nil
	default:
		return nil, fmt.Errorf("unsupported args payload")
	}
}

func getString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	switch casted := value.(type) {
	case string:
		return strings.TrimSpace(casted)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", value))
	}
}

func isWithin(path, base string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !strings.Contains(rel, ".."+string(filepath.Separator)))
}

func compactOutput(output string) string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return "(no output)"
	}
	if len(trimmed) > 280 {
		return trimmed[:280] + "..."
	}
	return trimmed
}
