package qmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	ErrUnavailable   = errors.New("qmd is unavailable")
	ErrNotFound      = errors.New("document not found")
	ErrInvalidTarget = errors.New("invalid document target")
)

type Config struct {
	WorkspaceRoot   string
	Binary          string
	SidecarURL      string
	IndexName       string
	Collection      string
	SharedModelsDir string
	EmbedExclude    []string
	SearchLimit     int
	OpenMaxBytes    int
	Debounce        time.Duration
	IndexTimeout    time.Duration
	QueryTimeout    time.Duration
	AutoEmbed       bool
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
	cfg          Config
	logger       *slog.Logger
	runner       commandRunner
	mu           sync.Mutex
	timers       map[string]*time.Timer
	pendingEmbed map[string]bool
	locks        map[string]*sync.Mutex
	collections  map[string]bool
	indexed      map[string]bool
	lastIndexed  map[string]time.Time
	closed       bool
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
		cfg:          cfg,
		logger:       logger,
		runner:       runner,
		timers:       map[string]*time.Timer{},
		pendingEmbed: map[string]bool{},
		locks:        map[string]*sync.Mutex{},
		collections:  map[string]bool{},
		indexed:      map[string]bool{},
		lastIndexed:  map[string]time.Time{},
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
	if len(cfg.EmbedExclude) > 0 {
		normalized := make([]string, 0, len(cfg.EmbedExclude))
		seen := map[string]struct{}{}
		for _, pattern := range cfg.EmbedExclude {
			value := normalizeEmbedPattern(pattern)
			if value == "" {
				continue
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			normalized = append(normalized, value)
		}
		cfg.EmbedExclude = normalized
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
		delete(s.pendingEmbed, workspaceID)
	}
	for workspaceDir := range s.collections {
		delete(s.collections, workspaceDir)
	}
}

func (s *Service) QueueWorkspaceIndex(workspaceID string) {
	s.QueueWorkspaceIndexForPath(workspaceID, "")
}

func (s *Service) QueueWorkspaceIndexForPath(workspaceID, changedPath string) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return
	}
	embedRequested := s.shouldEmbedForPath(workspaceID, changedPath)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}

	if current, ok := s.pendingEmbed[workspaceID]; ok {
		s.pendingEmbed[workspaceID] = current || embedRequested
	} else {
		s.pendingEmbed[workspaceID] = embedRequested
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
		delete(s.pendingEmbed, workspaceID)
		s.mu.Unlock()
		return
	}
	delete(s.timers, workspaceID)
	embedRequested := s.pendingEmbed[workspaceID]
	delete(s.pendingEmbed, workspaceID)
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.IndexTimeout)
	defer cancel()
	if err := s.indexWorkspace(ctx, workspaceID, embedRequested); err != nil {
		s.logger.Error("qmd workspace index failed", "workspace_id", workspaceID, "error", err)
	}
}

func (s *Service) IndexWorkspace(ctx context.Context, workspaceID string) error {
	return s.indexWorkspace(ctx, workspaceID, true)
}

func (s *Service) indexWorkspace(ctx context.Context, workspaceID string, embedRequested bool) error {
	workspaceDir, err := s.workspaceDir(workspaceID, true)
	if err != nil {
		return err
	}

	if err := s.ensureCollection(ctx, workspaceDir); err != nil {
		return err
	}

	updateOutput, err := s.runQMD(ctx, workspaceDir, "update")
	if err != nil {
		return err
	}
	if s.cfg.AutoEmbed && embedRequested {
		if !shouldRunEmbedAfterUpdate(updateOutput) {
			s.logger.Debug("qmd embed skipped", "workspace_id", workspaceID, "reason", "no pending vectors")
		} else if _, err := s.runQMD(ctx, workspaceDir, "embed"); err != nil {
			if looksLikeKnownBunEmbedCrash(err) {
				s.logger.Warn(
					"qmd embed failed with known Bun crash; continuing without refreshed embeddings",
					"workspace_id", workspaceID,
					"error", err,
				)
			} else {
				return err
			}
		}
	} else if s.cfg.AutoEmbed && !embedRequested {
		s.logger.Debug("qmd embed skipped", "workspace_id", workspaceID, "reason", "path excluded")
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
	searchCtx, cancel := context.WithTimeout(ctx, s.cfg.QueryTimeout)
	defer cancel()

	command := "query"
	if shouldUseFastSearch(query) {
		command = "search"
	}
	output, err := s.runQMD(searchCtx, workspaceDir, command, query, "--json", "-n", strconv.Itoa(limit))
	if err != nil {
		if command == "query" && looksLikeQueryExpansionKilled(err) {
			s.logger.Warn("qmd query expansion failed; falling back to bm25 search", "workspace", workspaceID, "error", err)
			fallbackCtx, fallbackCancel := context.WithTimeout(ctx, s.cfg.QueryTimeout)
			defer fallbackCancel()
			output, err = s.runQMD(fallbackCtx, workspaceDir, "search", query, "--json", "-n", strconv.Itoa(limit))
		}
		if err != nil && looksLikeIndexNotReady(err) {
			s.logger.Debug("qmd index not ready during search; queueing async index", "workspace_id", workspaceID, "error", err)
			s.QueueWorkspaceIndex(workspaceID)
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
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
	s.mu.Lock()
	if s.collections[workspaceDir] {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	indexPath := filepath.Join(workspaceDir, ".qmd", "cache", "qmd", "index.sqlite")
	if _, err := os.Stat(indexPath); err == nil {
		s.mu.Lock()
		s.collections[workspaceDir] = true
		s.mu.Unlock()
		return nil
	}
	markerPath := collectionMarkerPath(workspaceDir, s.cfg.IndexName, s.cfg.Collection)
	if _, err := os.Stat(markerPath); err == nil {
		s.mu.Lock()
		s.collections[workspaceDir] = true
		s.mu.Unlock()
		return nil
	}

	_, err := s.runQMD(ctx, workspaceDir, "collection", "add", ".", "--name", s.cfg.Collection, "--mask", "**/*.md")
	if err == nil {
		s.writeCollectionMarker(markerPath)
		s.mu.Lock()
		s.collections[workspaceDir] = true
		s.mu.Unlock()
		return nil
	}
	if looksLikeAlreadyExists(err) {
		s.writeCollectionMarker(markerPath)
		s.mu.Lock()
		s.collections[workspaceDir] = true
		s.mu.Unlock()
		return nil
	}
	return err
}

func (s *Service) writeCollectionMarker(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		s.logger.Debug("qmd collection marker mkdir failed", "path", path, "error", err)
		return
	}
	if err := os.WriteFile(path, []byte("ok\n"), 0o644); err != nil {
		s.logger.Debug("qmd collection marker write failed", "path", path, "error", err)
	}
}

func collectionMarkerPath(workspaceDir, indexName, collectionName string) string {
	sanitize := func(value string) string {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" {
			return "default"
		}
		var builder strings.Builder
		builder.Grow(len(value))
		for _, ch := range value {
			if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
				builder.WriteRune(ch)
			} else {
				builder.WriteRune('_')
			}
		}
		return builder.String()
	}
	fileName := sanitize(indexName) + "__" + sanitize(collectionName) + ".ready"
	return filepath.Join(workspaceDir, ".qmd", "spinner", fileName)
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
	if strings.TrimSpace(s.cfg.SidecarURL) != "" {
		return s.runQMDViaSidecar(ctx, workspaceDir, args...)
	}

	lock := s.workspaceLock(workspaceDir)
	lock.Lock()
	defer lock.Unlock()

	cacheDir := filepath.Join(workspaceDir, ".qmd", "cache")
	homeDir := filepath.Join(workspaceDir, ".qmd", "home")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		return nil, err
	}

	lockFilePath := filepath.Join(workspaceDir, ".qmd", "qmd.lock")
	fileLock, lockErr := acquireFileLock(ctx, lockFilePath)
	if lockErr != nil {
		return nil, lockErr
	}
	defer releaseFileLock(fileLock)
	if err := s.ensureModelCache(homeDir); err != nil {
		return nil, err
	}

	fullArgs := []string{"--index", s.cfg.IndexName}
	fullArgs = append(fullArgs, args...)

	const maxBusyRetries = 4
	repairedExtension := false
	attempt := 0

	var (
		cmd    *exec.Cmd
		output []byte
		err    error
	)
	for {
		cmd = s.qmdCommand(ctx, workspaceDir, cacheDir, homeDir, fullArgs)
		output, err = s.runner.Run(cmd)
		if err == nil {
			return output, nil
		}
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("%w: install qmd and ensure it is in PATH", ErrUnavailable)
		}

		if !repairedExtension {
			if repaired, repairPath, repairErr := repairSQLVecDoubleSO(string(output)); repaired {
				repairedExtension = true
				if repairErr != nil {
					s.logger.Warn("sqlite-vec compatibility repair failed", "path", repairPath, "error", repairErr)
				} else {
					s.logger.Info("applied sqlite-vec compatibility repair", "path", repairPath)
					continue
				}
			}
		}

		if !looksLikeRetryableQmdFailure(string(output)) || attempt >= maxBusyRetries {
			break
		}
		attempt++
		wait := time.Duration(attempt) * 200 * time.Millisecond
		s.logger.Warn("qmd transient failure; retrying", "workspace", workspaceDir, "attempt", attempt, "wait", wait)
		select {
		case <-ctx.Done():
			return nil, &commandError{
				command: cmd.String(),
				output:  strings.TrimSpace(string(output)),
				err:     ctx.Err(),
			}
		case <-time.After(wait):
		}
	}

	return nil, &commandError{
		command: cmd.String(),
		output:  strings.TrimSpace(string(output)),
		err:     err,
	}
}

func (s *Service) workspaceLock(workspaceDir string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if lock, ok := s.locks[workspaceDir]; ok {
		return lock
	}
	lock := &sync.Mutex{}
	s.locks[workspaceDir] = lock
	return lock
}

func (s *Service) ensureModelCache(homeDir string) error {
	workspaceModelsDir := filepath.Join(homeDir, ".cache", "qmd", "models")
	sharedModelsDir := strings.TrimSpace(s.cfg.SharedModelsDir)
	if sharedModelsDir == "" {
		return os.MkdirAll(workspaceModelsDir, 0o755)
	}
	sharedModelsDir = filepath.Clean(sharedModelsDir)
	if err := os.MkdirAll(sharedModelsDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(workspaceModelsDir), 0o755); err != nil {
		return err
	}

	info, err := os.Lstat(workspaceModelsDir)
	if errors.Is(err, os.ErrNotExist) {
		return os.Symlink(sharedModelsDir, workspaceModelsDir)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	if !info.IsDir() {
		return fmt.Errorf("qmd models path is not a directory: %s", workspaceModelsDir)
	}
	if err := migrateModelCacheDir(workspaceModelsDir, sharedModelsDir); err != nil {
		return err
	}
	return replaceWithSymlink(workspaceModelsDir, sharedModelsDir)
}

func looksLikeRetryableQmdFailure(output string) bool {
	return looksLikeSQLiteBusy(output) || looksLikeModelRenameENOENT(output)
}

func looksLikeSQLiteBusy(output string) bool {
	text := strings.ToLower(output)
	return strings.Contains(text, "database is locked") || strings.Contains(text, "sqlite_busy")
}

func looksLikeModelRenameENOENT(output string) bool {
	text := strings.ToLower(output)
	return strings.Contains(text, "enoent") && strings.Contains(text, "rename") && strings.Contains(text, ".ipull")
}

func looksLikeQueryExpansionKilled(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	if !strings.Contains(text, "expanding query") {
		return false
	}
	return strings.Contains(text, "signal: killed") || strings.Contains(text, "timed out after")
}

func looksLikeIndexNotReady(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "collection") && strings.Contains(text, "not found") ||
		strings.Contains(text, "no collections found") ||
		strings.Contains(text, "no such table") ||
		strings.Contains(text, "index not found")
}

func shouldUseFastSearch(query string) bool {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return true
	}
	if strings.Contains(trimmed, "\n") {
		return true
	}
	return len(trimmed) > 180
}

func looksLikeKnownBunEmbedCrash(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "attempted to call a non-gc-safe function inside a napi finalizer") ||
		strings.Contains(text, "src/bun.js/bindings/napi.h") ||
		strings.Contains(text, "oh no: bun has crashed") ||
		strings.Contains(text, "panic: aborted")
}

func shouldRunEmbedAfterUpdate(output []byte) bool {
	text := strings.ToLower(string(output))
	return strings.Contains(text, "run 'qmd embed' to update embeddings")
}

func normalizeEmbedPattern(pattern string) string {
	value := filepath.ToSlash(strings.TrimSpace(pattern))
	value = strings.TrimPrefix(value, "./")
	value = strings.TrimPrefix(value, "/")
	return value
}

func matchEmbedPattern(pattern, relativePath string) bool {
	pattern = normalizeEmbedPattern(pattern)
	relativePath = normalizeEmbedPattern(relativePath)
	if pattern == "" || relativePath == "" {
		return false
	}
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return relativePath == prefix || strings.HasPrefix(relativePath, prefix+"/")
	}
	matched, err := filepath.Match(filepath.FromSlash(pattern), filepath.FromSlash(relativePath))
	return err == nil && matched
}

func (s *Service) shouldEmbedForPath(workspaceID, changedPath string) bool {
	if len(s.cfg.EmbedExclude) == 0 {
		return true
	}
	changedPath = strings.TrimSpace(changedPath)
	if changedPath == "" {
		return true
	}
	workspaceDir, err := s.workspaceDir(workspaceID, false)
	if err != nil {
		return true
	}
	relativePath, _, err := sanitizePath(workspaceDir, changedPath)
	if err != nil {
		if filepath.IsAbs(changedPath) {
			cleaned := filepath.Clean(changedPath)
			if rel, relErr := filepath.Rel(workspaceDir, cleaned); relErr == nil {
				rel = filepath.ToSlash(rel)
				if rel != "." && !strings.HasPrefix(rel, "../") {
					relativePath = rel
				}
			}
		}
		if relativePath == "" {
			return true
		}
	}
	relativePath = filepath.ToSlash(relativePath)
	for _, pattern := range s.cfg.EmbedExclude {
		if matchEmbedPattern(pattern, relativePath) {
			return false
		}
	}
	return true
}

func acquireFileLock(ctx context.Context, lockPath string) (*os.File, error) {
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return file, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func releaseFileLock(file *os.File) {
	if file == nil {
		return
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}

func migrateModelCacheDir(sourceDir, sharedDir string) error {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name())
		if name == "" || name == "." || name == ".." {
			continue
		}
		sourcePath := filepath.Join(sourceDir, name)
		targetPath := filepath.Join(sharedDir, name)

		if _, err := os.Stat(targetPath); err == nil {
			if removeErr := os.RemoveAll(sourcePath); removeErr != nil {
				return removeErr
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}

		if renameErr := os.Rename(sourcePath, targetPath); renameErr == nil {
			continue
		} else if !errors.Is(renameErr, syscall.EXDEV) {
			return renameErr
		}

		if copyErr := copyPath(sourcePath, targetPath); copyErr != nil {
			return copyErr
		}
		if removeErr := os.RemoveAll(sourcePath); removeErr != nil {
			return removeErr
		}
	}
	return nil
}

func replaceWithSymlink(path, target string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return os.Symlink(target, path)
}

func copyPath(sourcePath, targetPath string) error {
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return err
	}
	switch {
	case info.Mode().IsRegular():
		return copyRegularFile(sourcePath, targetPath, info.Mode().Perm())
	case info.IsDir():
		return copyDir(sourcePath, targetPath)
	case info.Mode()&os.ModeSymlink != 0:
		linkTarget, err := os.Readlink(sourcePath)
		if err != nil {
			return err
		}
		return os.Symlink(linkTarget, targetPath)
	default:
		return fmt.Errorf("unsupported model cache entry type: %s", sourcePath)
	}
}

func copyDir(sourceDir, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name())
		if name == "" || name == "." || name == ".." {
			continue
		}
		if err := copyPath(filepath.Join(sourceDir, name), filepath.Join(targetDir, name)); err != nil {
			return err
		}
	}
	return nil
}

func copyRegularFile(sourcePath, targetPath string, perm fs.FileMode) error {
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	targetFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer targetFile.Close()

	if _, err := io.Copy(targetFile, sourceFile); err != nil {
		return err
	}
	return nil
}

func (s *Service) qmdCommand(ctx context.Context, workspaceDir, cacheDir, homeDir string, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, s.cfg.Binary, args...)
	cmd.Dir = workspaceDir
	cmd.Env = append(
		os.Environ(),
		"NO_COLOR=1",
		"XDG_CACHE_HOME="+cacheDir,
		"HOME="+homeDir,
	)
	return cmd
}

func (s *Service) runQMDViaSidecar(ctx context.Context, workspaceDir string, args ...string) ([]byte, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(s.cfg.SidecarURL), "/") + "/run"
	requestBody, err := json.Marshal(map[string]any{
		"workspace_dir": workspaceDir,
		"args":          args,
	})
	if err != nil {
		return nil, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: qmd sidecar request failed: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()

	responseBytes, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}

	var payload struct {
		Output string `json:"output"`
		Error  string `json:"error"`
	}
	_ = json.Unmarshal(responseBytes, &payload)

	if response.StatusCode != http.StatusOK {
		message := strings.TrimSpace(payload.Error)
		if message == "" {
			message = strings.TrimSpace(string(responseBytes))
		}
		if message == "" {
			message = "qmd sidecar request failed"
		}
		return nil, errors.New(message)
	}

	if strings.TrimSpace(payload.Output) == "" {
		return nil, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(payload.Output)
	if err != nil {
		return nil, fmt.Errorf("decode qmd sidecar output: %w", err)
	}
	return decoded, nil
}

func repairSQLVecDoubleSO(output string) (bool, string, error) {
	const marker = "Error loading shared library "
	index := strings.Index(output, marker)
	if index < 0 {
		return false, "", nil
	}
	remainder := output[index+len(marker):]
	separator := strings.Index(remainder, ":")
	if separator <= 0 {
		return false, "", nil
	}
	missingPath := strings.TrimSpace(remainder[:separator])
	if !strings.HasSuffix(missingPath, ".so.so") {
		return false, "", nil
	}
	sourcePath := strings.TrimSuffix(missingPath, ".so")
	if sourcePath == missingPath {
		return false, "", nil
	}
	if _, err := os.Stat(sourcePath); err != nil {
		return false, missingPath, nil
	}
	if _, err := os.Stat(missingPath); err == nil {
		return false, missingPath, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return true, missingPath, err
	}
	if err := os.Symlink(filepath.Base(sourcePath), missingPath); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return true, missingPath, nil
		}
		return true, missingPath, err
	}
	return true, missingPath, nil
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
