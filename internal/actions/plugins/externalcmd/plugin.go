package externalcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/actions/executor"
	"github.com/dwizi/agent-runtime/internal/store"
)

const (
	defaultTimeout = 60 * time.Second
	maxOutputBytes = 128 * 1024
)

type Config struct {
	ID          string
	PluginKey   string
	BaseDir     string
	Command     string
	Args        []string
	Env         map[string]string
	ActionTypes []string
	Timeout     time.Duration
}

type Plugin struct {
	id          string
	pluginKey   string
	baseDir     string
	command     string
	args        []string
	env         map[string]string
	actionTypes []string
	timeout     time.Duration
}

type requestPayload struct {
	Version        string               `json:"version"`
	ActionApproval store.ActionApproval `json:"action_approval"`
}

type responsePayload struct {
	Message string `json:"message"`
	Plugin  string `json:"plugin"`
}

func New(cfg Config) (*Plugin, error) {
	command := strings.TrimSpace(cfg.Command)
	if command == "" {
		return nil, fmt.Errorf("external plugin command is required")
	}
	actionTypes := normalizeActionTypes(cfg.ActionTypes)
	if len(actionTypes) == 0 {
		return nil, fmt.Errorf("external plugin action types are required")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	baseDir := strings.TrimSpace(cfg.BaseDir)
	if baseDir == "" {
		baseDir = "."
	}
	pluginKey := strings.TrimSpace(cfg.PluginKey)
	if pluginKey == "" {
		pluginKey = "external:" + strings.TrimSpace(cfg.ID)
	}
	if pluginKey == "external:" {
		pluginKey = "external:plugin"
	}
	expandedEnv := map[string]string{}
	for key, value := range cfg.Env {
		name := strings.TrimSpace(key)
		if name == "" {
			continue
		}
		expandedEnv[name] = os.ExpandEnv(strings.TrimSpace(value))
	}
	return &Plugin{
		id:          strings.TrimSpace(cfg.ID),
		pluginKey:   pluginKey,
		baseDir:     baseDir,
		command:     command,
		args:        append([]string{}, cfg.Args...),
		env:         expandedEnv,
		actionTypes: actionTypes,
		timeout:     timeout,
	}, nil
}

func (p *Plugin) PluginKey() string {
	return p.pluginKey
}

func (p *Plugin) ActionTypes() []string {
	return append([]string{}, p.actionTypes...)
}

func (p *Plugin) Execute(ctx context.Context, approval store.ActionApproval) (executor.Result, error) {
	if p == nil {
		return executor.Result{}, fmt.Errorf("external plugin is not configured")
	}
	runCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	execCommand := p.command
	if looksLikePath(execCommand) && !filepath.IsAbs(execCommand) {
		execCommand = filepath.Join(p.baseDir, execCommand)
	}
	execCommand = filepath.Clean(execCommand)

	payload := requestPayload{
		Version:        "v1",
		ActionApproval: approval,
	}
	requestBytes, err := json.Marshal(payload)
	if err != nil {
		return executor.Result{}, fmt.Errorf("encode external plugin request: %w", err)
	}

	cmd := exec.CommandContext(runCtx, execCommand, p.args...)
	cmd.Dir = p.baseDir
	cmd.Stdin = bytes.NewReader(requestBytes)
	cmd.Env = append(os.Environ(), mapToEnv(p.env)...)

	stdout := &limitedBuffer{MaxBytes: maxOutputBytes}
	stderr := &limitedBuffer{MaxBytes: maxOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		return executor.Result{}, fmt.Errorf(
			"external plugin %s failed: %w; stderr=%s",
			fallbackLabel(p.id, p.pluginKey),
			err,
			compactOutput(stderr.String()),
		)
	}

	responseText := strings.TrimSpace(stdout.String())
	if responseText == "" {
		return executor.Result{}, fmt.Errorf("external plugin %s returned empty stdout", fallbackLabel(p.id, p.pluginKey))
	}

	var decoded responsePayload
	if err := json.Unmarshal([]byte(responseText), &decoded); err != nil {
		return executor.Result{
			Plugin:  p.pluginKey,
			Message: compactOutput(responseText),
		}, nil
	}
	message := strings.TrimSpace(decoded.Message)
	if message == "" {
		message = compactOutput(responseText)
	}
	return executor.Result{
		Plugin:  strings.TrimSpace(decoded.Plugin),
		Message: message,
	}, nil
}

func normalizeActionTypes(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result
}

func fallbackLabel(id, key string) string {
	id = strings.TrimSpace(id)
	if id != "" {
		return id
	}
	key = strings.TrimSpace(key)
	if key != "" {
		return key
	}
	return "plugin"
}

func looksLikePath(command string) bool {
	return strings.Contains(command, "/") || strings.Contains(command, "\\") || strings.HasPrefix(command, ".")
}

func mapToEnv(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		result = append(result, key+"="+value)
	}
	return result
}

func compactOutput(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "(empty)"
	}
	normalized := strings.Join(strings.Fields(trimmed), " ")
	if len(normalized) <= 300 {
		return normalized
	}
	return normalized[:300] + "..."
}

type limitedBuffer struct {
	MaxBytes  int
	Truncated bool
	buf       bytes.Buffer
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.MaxBytes < 1 {
		return len(p), nil
	}
	remaining := b.MaxBytes - b.buf.Len()
	if remaining <= 0 {
		b.Truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.Truncated = true
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return b.buf.String()
}
