package qmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrUnavailable   = errors.New("qmd is unavailable")
	ErrNotFound      = errors.New("document not found")
	ErrInvalidTarget = errors.New("invalid document target")
)

type Config struct {
	WorkspaceRoot string
	Binary        string
	IndexName     string
	Collection    string
	SearchLimit   int
	OpenMaxBytes  int
	Debounce      time.Duration
	IndexTimeout  time.Duration
	QueryTimeout  time.Duration
	AutoEmbed     bool
}

type SearchResult struct {
	Path    string
	DocID   string
	Score   float64
	Snippet string
}

type OpenResult struct {
	Path      string
	Content   string
	Truncated bool
}

type Status struct {
	WorkspaceID    string
	WorkspacePath  string
	WorkspaceExist bool
	Pending        bool
	Indexed        bool
	LastIndexedAt  time.Time
	QMDBinary      string
	IndexFile      string
	IndexExists    bool
	Summary        string
}

type Service struct {
	cfg         Config
	logger      *slog.Logger
	runner      commandRunner
	mu          sync.Mutex
	timers      map[string]*time.Timer
	indexed     map[string]bool
	lastIndexed map[string]time.Time
	closed      bool
}

type commandRunner interface {
	Run(cmd *exec.Cmd) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(cmd *exec.Cmd) ([]byte, error) {
	return cmd.CombinedOutput()
}

type commandError struct {
	command string
	output  string
	err     error
}

func (e *commandError) Error() string {
	if e.output == "" {
		return fmt.Sprintf("%s: %v", e.command, e.err)
	}
	return fmt.Sprintf("%s: %v: %s", e.command, e.err, e.output)
}

func (e *commandError) Unwrap() error {
	return e.err
}

func New(cfg Config, logger *slog.Logger) *Service {
	return newService(cfg, logger, execRunner{})
}

func newService(cfg Config, logger *slog.Logger, runner commandRunner) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	if runner == nil {
		runner = execRunner{}
	}
	cfg = withDefaults(cfg)
	return &Service{
		cfg:         cfg,
		logger:      logger,
		runner:      runner,
		timers:      map[string]*time.Timer{},
		indexed:     map[string]bool{},
		lastIndexed: map[string]time.Time{},
	}
}

func withDefaults(cfg Config) Config {
	if strings.TrimSpace(cfg.Binary) == "" {
		cfg.Binary = "qmd"
	}
	if strings.TrimSpace(cfg.IndexName) == "" {
		cfg.IndexName = "spinner"
	}
	if strings.TrimSpace(cfg.Collection) == "" {
		cfg.Collection = "workspace"
	}
	if cfg.SearchLimit < 1 {
		cfg.SearchLimit = 5
	}
	if cfg.OpenMaxBytes < 256 {
		cfg.OpenMaxBytes = 1600
	}
	if cfg.Debounce <= 0 {
		cfg.Debounce = 3 * time.Second
	}
	if cfg.IndexTimeout <= 0 {
		cfg.IndexTimeout = 3 * time.Minute
	}
	if cfg.QueryTimeout <= 0 {
		cfg.QueryTimeout = 30 * time.Second
	}
	return cfg
}

func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	for workspaceID, timer := range s.timers {
		timer.Stop()
		delete(s.timers, workspaceID)
	}
}

func (s *Service) QueueWorkspaceIndex(workspaceID string) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}

	if timer, ok := s.timers[workspaceID]; ok {
		timer.Stop()
	}
	s.timers[workspaceID] = time.AfterFunc(s.cfg.Debounce, func() {
		s.runQueuedIndex(workspaceID)
	})
}

func (s *Service) runQueuedIndex(workspaceID string) {
	s.mu.Lock()
	if s.closed {
		delete(s.timers, workspaceID)
		s.mu.Unlock()
		return
	}
	delete(s.timers, workspaceID)
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.IndexTimeout)
	defer cancel()
	if err := s.IndexWorkspace(ctx, workspaceID); err != nil {
		s.logger.Error("qmd workspace index failed", "workspace_id", workspaceID, "error", err)
	}
}

func (s *Service) IndexWorkspace(ctx context.Context, workspaceID string) error {
	workspaceDir, err := s.workspaceDir(workspaceID, true)
	if err != nil {
		return err
	}

	if err := s.ensureCollection(ctx, workspaceDir); err != nil {
		return err
	}

	if _, err := s.runQMD(ctx, workspaceDir, "update"); err != nil {
		return err
	}
	if s.cfg.AutoEmbed {
		if _, err := s.runQMD(ctx, workspaceDir, "embed"); err != nil {
			return err
		}
	}

	s.mu.Lock()
	s.indexed[workspaceID] = true
	s.lastIndexed[workspaceID] = time.Now().UTC()
	s.mu.Unlock()
	return nil
}

func (s *Service) Status(ctx context.Context, workspaceID string) (Status, error) {
	workspaceDir, err := s.workspaceDir(workspaceID, false)
	if err != nil {
		return Status{}, err
	}
	status := Status{
		WorkspaceID:   workspaceID,
		WorkspacePath: workspaceDir,
		QMDBinary:     s.cfg.Binary,
		IndexFile:     filepath.Join(workspaceDir, ".qmd", "cache", "qmd", "index.sqlite"),
	}

	info, statErr := os.Stat(workspaceDir)
	if errors.Is(statErr, os.ErrNotExist) {
		return status, nil
	}
	if statErr != nil {
		return Status{}, statErr
	}
	status.WorkspaceExist = info.IsDir()

	s.mu.Lock()
	status.Pending = s.timers[workspaceID] != nil
	status.Indexed = s.indexed[workspaceID]
	status.LastIndexedAt = s.lastIndexed[workspaceID]
	s.mu.Unlock()

	if _, err := os.Stat(status.IndexFile); err == nil {
		status.IndexExists = true
	}

	statusCtx, cancel := context.WithTimeout(ctx, s.cfg.QueryTimeout)
	defer cancel()
	output, err := s.runQMD(statusCtx, workspaceDir, "status")
	if err != nil {
		return status, err
	}
	status.Summary = compactLine(string(output), 800)
	return status, nil
}

func (s *Service) Search(ctx context.Context, workspaceID, query string, limit int) ([]SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	workspaceDir, err := s.workspaceDir(workspaceID, false)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(workspaceDir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	if limit < 1 {
		limit = s.cfg.SearchLimit
	}
	if err := s.ensureIndexed(ctx, workspaceID); err != nil {
		return nil, err
	}

	searchCtx, cancel := context.WithTimeout(ctx, s.cfg.QueryTimeout)
	defer cancel()

	output, err := s.runQMD(searchCtx, workspaceDir, "query", query, "--json", "-n", strconv.Itoa(limit))
	if err != nil {
		return nil, err
	}
	return parseSearchResults(output), nil
}

func (s *Service) OpenMarkdown(ctx context.Context, workspaceID, target string) (OpenResult, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return OpenResult{}, ErrInvalidTarget
	}

	if strings.HasPrefix(target, "#") {
		workspaceDir, err := s.workspaceDir(workspaceID, false)
		if err != nil {
			return OpenResult{}, err
		}
		if _, err := os.Stat(workspaceDir); errors.Is(err, os.ErrNotExist) {
			return OpenResult{}, ErrNotFound
		} else if err != nil {
			return OpenResult{}, err
		}

		readCtx, cancel := context.WithTimeout(ctx, s.cfg.QueryTimeout)
		defer cancel()
		output, err := s.runQMD(readCtx, workspaceDir, "get", target, "--full")
		if err != nil {
			return OpenResult{}, err
		}
		content, truncated := truncateOutput(strings.TrimSpace(string(output)), s.cfg.OpenMaxBytes)
		if content == "" {
			return OpenResult{}, ErrNotFound
		}
		return OpenResult{Path: target, Content: content, Truncated: truncated}, nil
	}

	workspaceDir, err := s.workspaceDir(workspaceID, false)
	if err != nil {
		return OpenResult{}, err
	}
	relativePath, fullPath, err := sanitizePath(workspaceDir, target)
	if err != nil {
		return OpenResult{}, err
	}
	if strings.ToLower(filepath.Ext(relativePath)) != ".md" {
		return OpenResult{}, ErrInvalidTarget
	}

	content, err := os.ReadFile(fullPath)
	if errors.Is(err, os.ErrNotExist) {
		return OpenResult{}, ErrNotFound
	}
	if err != nil {
		return OpenResult{}, err
	}

	text, truncated := truncateOutput(string(content), s.cfg.OpenMaxBytes)
	return OpenResult{
		Path:      relativePath,
		Content:   text,
		Truncated: truncated,
	}, nil
}

func (s *Service) ensureIndexed(ctx context.Context, workspaceID string) error {
	s.mu.Lock()
	indexed := s.indexed[workspaceID]
	s.mu.Unlock()
	if indexed {
		return nil
	}

	indexCtx, cancel := context.WithTimeout(ctx, s.cfg.IndexTimeout)
	defer cancel()
	return s.IndexWorkspace(indexCtx, workspaceID)
}

func (s *Service) ensureCollection(ctx context.Context, workspaceDir string) error {
	_, err := s.runQMD(ctx, workspaceDir, "collection", "add", ".", "--name", s.cfg.Collection, "--mask", "**/*.md")
	if err == nil {
		return nil
	}
	if looksLikeAlreadyExists(err) {
		return nil
	}
	return err
}

func looksLikeAlreadyExists(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "already exists") || strings.Contains(message, "duplicate")
}

func (s *Service) workspaceDir(workspaceID string, create bool) (string, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" || strings.Contains(workspaceID, "..") || strings.ContainsAny(workspaceID, `/\`) {
		return "", fmt.Errorf("invalid workspace id")
	}
	if strings.TrimSpace(s.cfg.WorkspaceRoot) == "" {
		return "", fmt.Errorf("workspace root is not configured")
	}

	root := filepath.Clean(s.cfg.WorkspaceRoot)
	path := filepath.Join(root, workspaceID)
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(relative, "..") || relative == "." {
		return "", fmt.Errorf("invalid workspace path")
	}

	if create {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return "", err
		}
	}
	return path, nil
}

func sanitizePath(workspaceDir, target string) (string, string, error) {
	normalized := filepath.Clean(strings.TrimSpace(target))
	if normalized == "." || normalized == "" || filepath.IsAbs(normalized) || strings.HasPrefix(normalized, "..") {
		return "", "", ErrInvalidTarget
	}
	fullPath := filepath.Join(workspaceDir, normalized)
	rel, err := filepath.Rel(workspaceDir, fullPath)
	if err != nil {
		return "", "", ErrInvalidTarget
	}
	if strings.HasPrefix(rel, "..") {
		return "", "", ErrInvalidTarget
	}
	return rel, fullPath, nil
}

func truncateOutput(input string, maxBytes int) (string, bool) {
	if maxBytes < 1 {
		return input, false
	}
	trimmed := strings.TrimSpace(input)
	if len(trimmed) <= maxBytes {
		return trimmed, false
	}
	return strings.TrimSpace(trimmed[:maxBytes]), true
}

func compactLine(input string, maxBytes int) string {
	compact := strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
	if maxBytes < 1 || len(compact) <= maxBytes {
		return compact
	}
	return compact[:maxBytes]
}

func (s *Service) runQMD(ctx context.Context, workspaceDir string, args ...string) ([]byte, error) {
	cacheDir := filepath.Join(workspaceDir, ".qmd", "cache")
	homeDir := filepath.Join(workspaceDir, ".qmd", "home")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		return nil, err
	}

	fullArgs := []string{"--index", s.cfg.IndexName}
	fullArgs = append(fullArgs, args...)

	cmd := exec.CommandContext(ctx, s.cfg.Binary, fullArgs...)
	cmd.Dir = workspaceDir
	cmd.Env = append(
		os.Environ(),
		"NO_COLOR=1",
		"XDG_CACHE_HOME="+cacheDir,
		"HOME="+homeDir,
	)

	output, err := s.runner.Run(cmd)
	if err == nil {
		return output, nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return nil, fmt.Errorf("%w: install qmd and ensure it is in PATH", ErrUnavailable)
	}
	return nil, &commandError{
		command: cmd.String(),
		output:  strings.TrimSpace(string(output)),
		err:     err,
	}
}

func parseSearchResults(input []byte) []SearchResult {
	trimmed := strings.TrimSpace(string(input))
	if trimmed == "" {
		return nil
	}

	var root any
	if err := json.Unmarshal([]byte(trimmed), &root); err != nil {
		return nil
	}
	items := extractSearchItems(root)
	results := make([]SearchResult, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		path := nonEmptyString(record["path"], record["filepath"], record["file"])
		docID := nonEmptyString(record["docid"], record["doc_id"], record["id"])
		snippet := nonEmptyString(record["snippet"], record["text"], record["content"], record["title"])
		if strings.TrimSpace(path) == "" && strings.TrimSpace(docID) == "" {
			continue
		}
		results = append(results, SearchResult{
			Path:    strings.TrimSpace(path),
			DocID:   strings.TrimSpace(docID),
			Score:   toFloat(record["score"]),
			Snippet: strings.TrimSpace(snippet),
		})
	}
	return results
}

func extractSearchItems(root any) []any {
	switch value := root.(type) {
	case []any:
		return value
	case map[string]any:
		for _, key := range []string{"results", "items", "data"} {
			if raw, ok := value[key]; ok {
				if list, ok := raw.([]any); ok {
					return list
				}
			}
		}
	}
	return nil
}

func nonEmptyString(values ...any) string {
	for _, value := range values {
		text := strings.TrimSpace(fmt.Sprintf("%v", value))
		if text == "" || text == "<nil>" {
			continue
		}
		return text
	}
	return ""
}

func toFloat(value any) float64 {
	switch number := value.(type) {
	case float64:
		return number
	case float32:
		return float64(number)
	case int:
		return float64(number)
	case int64:
		return float64(number)
	case json.Number:
		parsed, err := number.Float64()
		if err == nil {
			return parsed
		}
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(number), 64)
		if err == nil {
			return parsed
		}
	}
	return 0
}
