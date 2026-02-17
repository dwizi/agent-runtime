package qmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

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

	output, err := s.runQMD(searchCtx, workspaceDir, "search", query, "--json", "-n", strconv.Itoa(limit))
	if err != nil {
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
	return filepath.Join(workspaceDir, ".qmd", "agent-runtime", fileName)
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
