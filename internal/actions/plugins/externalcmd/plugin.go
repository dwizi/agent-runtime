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
	"sync"
	"time"

	"github.com/dwizi/agent-runtime/internal/actions/executor"
	"github.com/dwizi/agent-runtime/internal/store"
)

const (
	defaultTimeout = 60 * time.Second
	maxOutputBytes = 128 * 1024
)

type Config struct {
	ID            string
	PluginKey     string
	BaseDir       string
	Command       string
	Args          []string
	Env           map[string]string
	ActionTypes   []string
	Timeout       time.Duration
	RunnerCommand string
	RunnerArgs    []string
	UV            *UVConfig
}

type UVConfig struct {
	ProjectDir      string
	CacheDir        string
	VenvDir         string
	WarmOnBootstrap bool
	Locked          bool
}

type Plugin struct {
	id            string
	pluginKey     string
	baseDir       string
	command       string
	args          []string
	env           map[string]string
	actionTypes   []string
	timeout       time.Duration
	runnerCommand string
	runnerArgs    []string
	uv            *uvRuntime
}

type uvRuntime struct {
	projectDir      string
	cacheDir        string
	venvDir         string
	warmOnBootstrap bool
	locked          bool
	ready           bool
	mu              sync.Mutex
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

	var uvRuntimeConfig *uvRuntime
	if cfg.UV != nil {
		projectDir := strings.TrimSpace(cfg.UV.ProjectDir)
		cacheDir := strings.TrimSpace(cfg.UV.CacheDir)
		venvDir := strings.TrimSpace(cfg.UV.VenvDir)
		if projectDir == "" || cacheDir == "" || venvDir == "" {
			return nil, fmt.Errorf("external plugin uv configuration requires project_dir, cache_dir, and venv_dir")
		}
		uvRuntimeConfig = &uvRuntime{
			projectDir:      projectDir,
			cacheDir:        cacheDir,
			venvDir:         venvDir,
			warmOnBootstrap: cfg.UV.WarmOnBootstrap,
			locked:          cfg.UV.Locked,
		}
	}

	return &Plugin{
		id:            strings.TrimSpace(cfg.ID),
		pluginKey:     pluginKey,
		baseDir:       baseDir,
		command:       command,
		args:          append([]string{}, cfg.Args...),
		env:           expandedEnv,
		actionTypes:   actionTypes,
		timeout:       timeout,
		runnerCommand: strings.TrimSpace(cfg.RunnerCommand),
		runnerArgs:    append([]string{}, cfg.RunnerArgs...),
		uv:            uvRuntimeConfig,
	}, nil
}

func (p *Plugin) PluginKey() string {
	return p.pluginKey
}

func (p *Plugin) ActionTypes() []string {
	return append([]string{}, p.actionTypes...)
}

func (p *Plugin) Warmup(ctx context.Context) error {
	if p == nil || p.uv == nil || !p.uv.warmOnBootstrap {
		return nil
	}
	return p.ensureUVReady(ctx)
}

func (p *Plugin) Execute(ctx context.Context, approval store.ActionApproval) (executor.Result, error) {
	if p == nil {
		return executor.Result{}, fmt.Errorf("external plugin is not configured")
	}
	if p.uv != nil {
		if err := p.ensureUVReady(ctx); err != nil {
			return executor.Result{}, err
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	execCommand := p.command
	if looksLikePath(execCommand) && !filepath.IsAbs(execCommand) {
		execCommand = filepath.Join(p.baseDir, execCommand)
	}
	execCommand = filepath.Clean(execCommand)
	execArgs := append([]string{}, p.args...)

	payload := requestPayload{
		Version:        "v1",
		ActionApproval: approval,
	}
	requestBytes, err := json.Marshal(payload)
	if err != nil {
		return executor.Result{}, fmt.Errorf("encode external plugin request: %w", err)
	}

	commandEnv := p.runtimeEnv()
	if p.uv != nil {
		execCommand, execArgs = p.uvRunSpec(execCommand, execArgs)
	}
	execName, execSpecArgs := p.executionSpec(execCommand, execArgs)
	cmd := exec.CommandContext(runCtx, execName, execSpecArgs...)
	cmd.Dir = p.baseDir
	cmd.Stdin = bytes.NewReader(requestBytes)
	cmd.Env = commandEnv

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

func (p *Plugin) ensureUVReady(ctx context.Context) error {
	if p == nil || p.uv == nil {
		return nil
	}
	p.uv.mu.Lock()
	defer p.uv.mu.Unlock()
	if p.uv.ready {
		return nil
	}

	timeout := p.timeout
	if timeout < 2*time.Minute {
		timeout = 2 * time.Minute
	}
	syncCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{"sync", "--project", p.uv.projectDir, "--no-dev"}
	if p.uv.locked {
		args = append(args, "--locked")
	}
	execName, execArgs := p.executionSpec("uv", args)
	cmd := exec.CommandContext(syncCtx, execName, execArgs...)
	cmd.Dir = p.baseDir
	cmd.Env = p.runtimeEnv()

	stdout := &limitedBuffer{MaxBytes: maxOutputBytes}
	stderr := &limitedBuffer{MaxBytes: maxOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf(
			"external plugin %s uv warmup failed: %w; stderr=%s",
			fallbackLabel(p.id, p.pluginKey),
			err,
			compactOutput(stderr.String()),
		)
	}
	p.uv.ready = true
	return nil
}

func (p *Plugin) uvRunSpec(command string, args []string) (string, []string) {
	runArgs := []string{"run", "--project", p.uv.projectDir, "--no-sync", "--", command}
	runArgs = append(runArgs, args...)
	return "uv", runArgs
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

func (p *Plugin) runtimeEnv() []string {
	env := append(os.Environ(), mapToEnv(p.env)...)
	if p.uv != nil {
		env = append(env, "UV_CACHE_DIR="+p.uv.cacheDir)
		env = append(env, "UV_PROJECT_ENVIRONMENT="+p.uv.venvDir)
	}
	return env
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
