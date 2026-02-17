package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/actions/executor"
	"github.com/dwizi/agent-runtime/internal/agenterr"
	"github.com/dwizi/agent-runtime/internal/store"
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
		return executor.Result{}, fmt.Errorf("%w: %v", agenterr.ErrToolInvalidArgs, err)
	}
	if !p.isAllowed(command) {
		return executor.Result{}, fmt.Errorf("%w: command %q", agenterr.ErrToolNotAllowed, command)
	}
	execCommand, execArgs, fallbackUsed := p.resolveExecutionCommand(command, args)
	workdir, err := p.resolveWorkingDir(approval)
	if err != nil {
		return executor.Result{}, fmt.Errorf("%w: %v", agenterr.ErrToolPreflight, err)
	}
	runCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	execName, execSpecArgs := p.executionSpec(execCommand, execArgs)
	cmd := exec.CommandContext(runCtx, execName, execSpecArgs...)
	cmd.Dir = workdir
	combinedOutput := &limitedBuffer{MaxBytes: p.maxOutputBytes}
	cmd.Stdout = combinedOutput
	cmd.Stderr = combinedOutput
	if err := cmd.Run(); err != nil {
		if retryArgs, retryFallback, ok := retryGitDiffNoIndex(execCommand, execArgs, err, combinedOutput.String()); ok {
			runCtxRetry, cancelRetry := context.WithTimeout(ctx, p.timeout)
			defer cancelRetry()
			execNameRetry, execSpecArgsRetry := p.executionSpec(execCommand, retryArgs)
			retryCmd := exec.CommandContext(runCtxRetry, execNameRetry, execSpecArgsRetry...)
			retryCmd.Dir = workdir
			retryOutput := &limitedBuffer{MaxBytes: p.maxOutputBytes}
			retryCmd.Stdout = retryOutput
			retryCmd.Stderr = retryOutput
			if retryErr := retryCmd.Run(); retryErr == nil || isExpectedNonZeroExit(execCommand, retryArgs, retryErr) {
				message := summarizeCommandOutcome(command, args, retryOutput.String(), retryOutput.Truncated)
				fallbackUsed = mergeFallbackHint(fallbackUsed, retryFallback)
				if strings.TrimSpace(fallbackUsed) != "" {
					message = message + " (fallback: " + fallbackUsed + ")"
				}
				return executor.Result{
					Plugin:  p.PluginKey(),
					Message: message,
				}, nil
			}
		}
		if isExpectedNonZeroExit(execCommand, execArgs, err) {
			message := summarizeCommandOutcome(command, args, combinedOutput.String(), combinedOutput.Truncated)
			if strings.TrimSpace(fallbackUsed) != "" {
				message = message + " (fallback: " + fallbackUsed + ")"
			}
			return executor.Result{
				Plugin:  p.PluginKey(),
				Message: message,
			}, nil
		}
		return executor.Result{}, fmt.Errorf("command failed: %w; output=%s", err, compactOutput(combinedOutput.String(), combinedOutput.Truncated))
	}
	message := summarizeCommandOutcome(command, args, combinedOutput.String(), combinedOutput.Truncated)
	if strings.TrimSpace(fallbackUsed) != "" {
		message = message + " (fallback: " + fallbackUsed + ")"
	}
	return executor.Result{
		Plugin:  p.PluginKey(),
		Message: message,
	}, nil
}

func (p *Plugin) resolveExecutionCommand(command string, args []string) (string, []string, string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return command, args, ""
	}
	if strings.TrimSpace(p.runnerCommand) != "" {
		return command, args, ""
	}
	if _, err := exec.LookPath(command); err == nil {
		return command, args, ""
	}
	fallbackCommand, fallbackArgs, ok := commandFallback(command, args)
	if !ok {
		return command, args, ""
	}
	if _, err := exec.LookPath(fallbackCommand); err != nil {
		return command, args, ""
	}
	return fallbackCommand, fallbackArgs, command + " -> " + fallbackCommand
}

func commandFallback(command string, args []string) (string, []string, bool) {
	switch strings.ToLower(strings.TrimSpace(command)) {
	case "rg":
		fallbackArgs, ok := translateRGToGrep(args)
		if !ok {
			return "", nil, false
		}
		return "grep", fallbackArgs, true
	case "curl":
		fallbackArgs, ok := translateCurlToWget(args)
		if !ok {
			return "", nil, false
		}
		return "wget", fallbackArgs, true
	default:
		return "", nil, false
	}
}

func mergeFallbackHint(base, extra string) string {
	base = strings.TrimSpace(base)
	extra = strings.TrimSpace(extra)
	if base == "" {
		return extra
	}
	if extra == "" {
		return base
	}
	return base + "; " + extra
}

func retryGitDiffNoIndex(command string, args []string, runErr error, output string) ([]string, string, bool) {
	if !strings.EqualFold(strings.TrimSpace(command), "git") {
		return nil, "", false
	}
	diffIndex, ok := gitDiffSubcommandIndex(args)
	if !ok {
		return nil, "", false
	}
	if hasGitNoIndex(args) {
		return nil, "", false
	}
	if !isGitNotRepositoryFailure(runErr, output) {
		return nil, "", false
	}
	retryArgs := append([]string{}, args[:diffIndex+1]...)
	retryArgs = append(retryArgs, "--no-index")
	retryArgs = append(retryArgs, args[diffIndex+1:]...)
	return retryArgs, "git diff -> git diff --no-index", true
}

func gitDiffSubcommandIndex(args []string) (int, bool) {
	for index, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "-") {
			continue
		}
		return index, strings.EqualFold(trimmed, "diff")
	}
	return 0, false
}

func hasGitNoIndex(args []string) bool {
	for _, arg := range args {
		if strings.EqualFold(strings.TrimSpace(arg), "--no-index") {
			return true
		}
	}
	return false
}

func isGitNotRepositoryFailure(runErr error, output string) bool {
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(output))
	if strings.Contains(lower, "not a git repository") {
		return true
	}
	if strings.Contains(lower, "use --no-index") {
		return true
	}
	return strings.Contains(lower, "git diff --no-index")
}

func isExpectedNonZeroExit(command string, args []string, runErr error) bool {
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(command), "git") {
		return false
	}
	_, isDiff := gitDiffSubcommandIndex(args)
	if !isDiff {
		return false
	}
	return exitErr.ExitCode() == 1
}

func translateRGToGrep(args []string) ([]string, bool) {
	pattern := ""
	searchPath := "."
	grepFlags := []string{"-R", "-n"}

	for index := 0; index < len(args); index++ {
		arg := strings.TrimSpace(args[index])
		if arg == "" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			switch arg {
			case "-n", "--line-number":
			case "-i", "--ignore-case":
				grepFlags = append(grepFlags, "-i")
			case "-F", "--fixed-strings":
				grepFlags = append(grepFlags, "-F")
			case "-w", "--word-regexp":
				grepFlags = append(grepFlags, "-w")
			case "-v", "--invert-match":
				grepFlags = append(grepFlags, "-v")
			case "-m", "--max-count":
				if index+1 >= len(args) {
					return nil, false
				}
				grepFlags = append(grepFlags, "-m", strings.TrimSpace(args[index+1]))
				index++
			default:
				return nil, false
			}
			continue
		}
		if pattern == "" {
			pattern = arg
			continue
		}
		if searchPath == "." {
			searchPath = arg
			continue
		}
		return nil, false
	}

	if pattern == "" {
		return nil, false
	}
	fallbackArgs := append([]string{}, grepFlags...)
	fallbackArgs = append(fallbackArgs, "--", pattern, searchPath)
	return fallbackArgs, true
}

func translateCurlToWget(args []string) ([]string, bool) {
	url := ""
	output := "-"
	insecure := false
	headers := make([]string, 0, 2)

	for index := 0; index < len(args); index++ {
		arg := strings.TrimSpace(args[index])
		if arg == "" {
			continue
		}
		switch arg {
		case "-s", "-S", "-sS", "-Ss", "-f", "-L", "--location", "--location-trusted", "-fsSL", "-fsS", "-fsL", "-sSL", "-sL":
			continue
		case "-k", "--insecure":
			insecure = true
			continue
		case "-o", "--output":
			if index+1 >= len(args) {
				return nil, false
			}
			output = strings.TrimSpace(args[index+1])
			index++
			continue
		case "-H", "--header":
			if index+1 >= len(args) {
				return nil, false
			}
			headers = append(headers, strings.TrimSpace(args[index+1]))
			index++
			continue
		}

		if strings.HasPrefix(arg, "-") {
			return nil, false
		}
		if url != "" {
			return nil, false
		}
		url = arg
	}

	if url == "" {
		return nil, false
	}

	fallbackArgs := []string{"-q"}
	if insecure {
		fallbackArgs = append(fallbackArgs, "--no-check-certificate")
	}
	for _, header := range headers {
		if header == "" {
			continue
		}
		fallbackArgs = append(fallbackArgs, "--header", header)
	}
	if strings.TrimSpace(output) == "" {
		output = "-"
	}
	fallbackArgs = append(fallbackArgs, "-O", output, url)
	return fallbackArgs, true
}

func (p *Plugin) isAllowed(command string) bool {
	key := strings.ToLower(strings.TrimSpace(filepath.Base(command)))
	_, ok := p.allowed[key]
	return ok
}

func (p *Plugin) resolveWorkingDir(approval store.ActionApproval) (string, error) {
	workspaceRoot := filepath.Clean(filepath.Join(p.workspaceRoot, approval.WorkspaceID))
	if strings.TrimSpace(approval.WorkspaceID) == "" {
		return "", fmt.Errorf("%w: workspace id is required for sandbox command", agenterr.ErrToolInvalidArgs)
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
		return "", fmt.Errorf("%w: cwd escapes workspace boundary", agenterr.ErrToolPreflight)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("%w: cwd does not exist", agenterr.ErrToolPreflight)
	}
	return resolved, nil
}

func parseCommand(approval store.ActionApproval) (string, []string, error) {
	command := strings.TrimSpace(approval.ActionTarget)
	commandFromPayload := getString(approval.Payload, "command")
	args := []string{}
	if rawArgs, ok := getPayloadValue(approval.Payload, "args"); ok && rawArgs != nil {
		parsed, err := parseArgs(rawArgs)
		if err != nil {
			return "", nil, err
		}
		args = append(args, parsed...)
	}
	payloadCommand, payloadArgs := splitCommandString(commandFromPayload)
	if command == "" {
		command = payloadCommand
	} else if payloadCommand != "" && !strings.EqualFold(command, payloadCommand) {
		return "", nil, fmt.Errorf("payload.command executable must match target")
	}
	if len(args) == 0 && len(payloadArgs) > 0 {
		args = append(args, payloadArgs...)
	}
	if command == "" {
		return "", nil, fmt.Errorf("%w: command action requires target or payload.command", agenterr.ErrToolInvalidArgs)
	}
	if strings.Contains(command, "/") || strings.Contains(command, "\\") || strings.ContainsAny(command, " \t\r\n") {
		return "", nil, fmt.Errorf("%w: command must be a bare executable name", agenterr.ErrToolInvalidArgs)
	}
	if len(args) > 32 {
		return "", nil, fmt.Errorf("%w: too many arguments", agenterr.ErrToolInvalidArgs)
	}
	for _, arg := range args {
		if len(arg) > 512 {
			return "", nil, fmt.Errorf("%w: argument exceeds limit", agenterr.ErrToolInvalidArgs)
		}
	}
	return command, args, nil
}

func splitCommandString(command string) (string, []string) {
	parts := strings.Fields(strings.TrimSpace(command))
	if len(parts) == 0 {
		return "", nil
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	return parts[0], parts[1:]
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
	value, ok := getPayloadValue(payload, key)
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

func getPayloadValue(payload map[string]any, key string) (any, bool) {
	if payload == nil {
		return nil, false
	}
	if value, ok := payload[key]; ok {
		return value, true
	}
	nestedRaw, ok := payload["payload"]
	if !ok || nestedRaw == nil {
		return nil, false
	}
	nested, ok := nestedRaw.(map[string]any)
	if !ok {
		return nil, false
	}
	value, ok := nested[key]
	return value, ok
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
	if len(trimmed) > 1600 {
		return trimmed[:1600] + "..."
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
