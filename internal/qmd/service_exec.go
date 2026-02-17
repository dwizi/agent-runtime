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
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

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
