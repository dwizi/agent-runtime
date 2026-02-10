package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
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
	RunnerCommand   string
	RunnerArgs      []string
	Timeout         time.Duration
	MaxOutputBytes  int
}

type Plugin struct {
	enabled        bool
	workspaceRoot  string
	allowed        map[string]struct{}
	runnerCommand  string
	runnerArgs     []string
	timeout        time.Duration
	maxOutputBytes int
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
	maxOutputBytes := cfg.MaxOutputBytes
	if maxOutputBytes < 256 {
		maxOutputBytes = 4096
	}
	return &Plugin{
		enabled:        cfg.Enabled,
		workspaceRoot:  filepath.Clean(strings.TrimSpace(cfg.WorkspaceRoot)),
		allowed:        allowed,
		runnerCommand:  strings.TrimSpace(cfg.RunnerCommand),
		runnerArgs:     append([]string{}, cfg.RunnerArgs...),
		timeout:        timeout,
		maxOutputBytes: maxOutputBytes,
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
	execName, execArgs := p.executionSpec(command, args)
	cmd := exec.CommandContext(runCtx, execName, execArgs...)
	cmd.Dir = workdir
	combinedOutput := &limitedBuffer{MaxBytes: p.maxOutputBytes}
	cmd.Stdout = combinedOutput
	cmd.Stderr = combinedOutput
	if err := cmd.Run(); err != nil {
		return executor.Result{}, fmt.Errorf("command failed: %w; output=%s", err, compactOutput(combinedOutput.String(), combinedOutput.Truncated))
	}
	return executor.Result{
		Plugin:  p.PluginKey(),
		Message: summarizeCommandOutcome(command, args, combinedOutput.String(), combinedOutput.Truncated),
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
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("cwd does not exist")
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
	if strings.Contains(command, "/") || strings.Contains(command, "\\") || strings.ContainsAny(command, " \t\r\n") {
		return "", nil, fmt.Errorf("command must be a bare executable name")
	}
	args := []string{}
	if rawArgs, ok := approval.Payload["args"]; ok && rawArgs != nil {
		parsed, err := parseArgs(rawArgs)
		if err != nil {
			return "", nil, err
		}
		args = append(args, parsed...)
	}
	if len(args) > 32 {
		return "", nil, fmt.Errorf("too many arguments")
	}
	for _, arg := range args {
		if len(arg) > 512 {
			return "", nil, fmt.Errorf("argument exceeds limit")
		}
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

func compactOutput(output string, truncated bool) string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		if truncated {
			return "(output truncated)"
		}
		return "(no output)"
	}
	if truncated {
		return trimmed + " ... [truncated]"
	}
	if len(trimmed) > 280 {
		return trimmed[:280] + "..."
	}
	return trimmed
}

func summarizeCommandOutcome(command string, args []string, output string, truncated bool) string {
	snippet := compactOutput(output, truncated)
	if snippet == "(no output)" {
		return "The command ran successfully and produced no output."
	}
	if strings.EqualFold(strings.TrimSpace(command), "curl") && !curlFollowsRedirects(args) && looksLikeRedirectResponse(snippet) {
		return "The command ran successfully, but curl stopped at an HTTP redirect. Use `-L` to follow redirects. Output: " + snippet
	}
	return "The command ran successfully. Output: " + snippet
}

func curlFollowsRedirects(args []string) bool {
	for _, arg := range args {
		switch strings.TrimSpace(arg) {
		case "-L", "--location", "--location-trusted":
			return true
		}
	}
	return false
}

func looksLikeRedirectResponse(output string) bool {
	lower := strings.ToLower(strings.TrimSpace(output))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "redirecting") ||
		strings.Contains(lower, "moved permanently") ||
		strings.Contains(lower, "found. redirecting to") ||
		(strings.Contains(lower, "http-equiv=\"refresh\"") && strings.Contains(lower, "url="))
}

func (p *Plugin) executionSpec(command string, args []string) (string, []string) {
	if strings.TrimSpace(p.runnerCommand) == "" {
		return command, args
	}
	execArgs := append([]string{}, p.runnerArgs...)
	execArgs = append(execArgs, command)
	execArgs = append(execArgs, args...)
	return p.runnerCommand, execArgs
}

type limitedBuffer struct {
	MaxBytes  int
	Truncated bool
	buffer    bytes.Buffer
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.MaxBytes < 1 {
		return l.buffer.Write(p)
	}
	remaining := l.MaxBytes - l.buffer.Len()
	if remaining <= 0 {
		l.Truncated = true
		return len(p), nil
	}
	toWrite := p
	if len(p) > remaining {
		toWrite = p[:remaining]
		l.Truncated = true
	}
	if _, err := l.buffer.Write(toWrite); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (l *limitedBuffer) String() string {
	return l.buffer.String()
}

var _ io.Writer = (*limitedBuffer)(nil)
