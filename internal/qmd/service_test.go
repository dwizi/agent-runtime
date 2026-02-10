package qmd

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeRunner struct {
	mu       sync.Mutex
	calls    []string
	resolver func(cmd *exec.Cmd) ([]byte, error)
}

func (f *fakeRunner) Run(cmd *exec.Cmd) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, strings.Join(cmd.Args, " "))
	f.mu.Unlock()
	if f.resolver == nil {
		return nil, nil
	}
	return f.resolver(cmd)
}

func (f *fakeRunner) callsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]string, len(f.calls))
	copy(items, f.calls)
	return items
}

func TestSearchReturnsParsedResults(t *testing.T) {
	root := t.TempDir()
	workspaceID := "ws-test"
	workspacePath := filepath.Join(root, workspaceID)
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	runner := &fakeRunner{
		resolver: func(cmd *exec.Cmd) ([]byte, error) {
			args := strings.Join(cmd.Args, " ")
			switch {
			case strings.Contains(args, " query "):
				return []byte(`[{"path":"notes/today.md","docid":"#abc123","score":0.91,"snippet":"release prep notes"}]`), nil
			default:
				return []byte("ok"), nil
			}
		},
	}
	service := newService(
		Config{
			WorkspaceRoot: root,
			AutoEmbed:     false,
		},
		slog.Default(),
		runner,
	)

	results, err := service.Search(context.Background(), workspaceID, "release prep", 0)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	if results[0].Path != "notes/today.md" {
		t.Fatalf("unexpected path: %s", results[0].Path)
	}
	if results[0].DocID != "#abc123" {
		t.Fatalf("unexpected docid: %s", results[0].DocID)
	}
}

func TestSearchFallsBackWhenQueryExpansionIsKilled(t *testing.T) {
	root := t.TempDir()
	workspaceID := "ws-query-fallback"
	workspacePath := filepath.Join(root, workspaceID)
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	runner := &fakeRunner{
		resolver: func(cmd *exec.Cmd) ([]byte, error) {
			args := strings.Join(cmd.Args, " ")
			switch {
			case strings.Contains(args, " query "):
				return []byte("signal: killed: Expanding query..."), errors.New("exit status 1")
			case strings.Contains(args, " search "):
				return []byte(`[{"path":"pricing.md","docid":"#p1","score":0.88,"snippet":"pricing details"}]`), nil
			default:
				return []byte("ok"), nil
			}
		},
	}
	service := newService(
		Config{
			WorkspaceRoot: root,
			AutoEmbed:     false,
		},
		slog.Default(),
		runner,
	)

	results, err := service.Search(context.Background(), workspaceID, "pricing", 3)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one fallback result, got %d", len(results))
	}
	if results[0].Path != "pricing.md" {
		t.Fatalf("unexpected path: %s", results[0].Path)
	}

	calls := runner.callsSnapshot()
	seenQuery := false
	seenSearch := false
	for _, call := range calls {
		if strings.Contains(call, " query ") {
			seenQuery = true
		}
		if strings.Contains(call, " search ") {
			seenSearch = true
		}
	}
	if !seenQuery || !seenSearch {
		t.Fatalf("expected query then search fallback calls, got %v", calls)
	}
}

func TestOpenMarkdownFromWorkspacePath(t *testing.T) {
	root := t.TempDir()
	workspaceID := "ws-open"
	workspacePath := filepath.Join(root, workspaceID)
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	target := filepath.Join(workspacePath, "memory.md")
	if err := os.WriteFile(target, []byte("line one\nline two"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	service := newService(
		Config{
			WorkspaceRoot: root,
			OpenMaxBytes:  2048,
		},
		slog.Default(),
		&fakeRunner{},
	)

	result, err := service.OpenMarkdown(context.Background(), workspaceID, "memory.md")
	if err != nil {
		t.Fatalf("open markdown failed: %v", err)
	}
	if result.Path != "memory.md" {
		t.Fatalf("unexpected path: %s", result.Path)
	}
	if !strings.Contains(result.Content, "line two") {
		t.Fatalf("unexpected content: %s", result.Content)
	}
}

func TestOpenMarkdownRejectsTraversal(t *testing.T) {
	service := newService(
		Config{
			WorkspaceRoot: t.TempDir(),
		},
		slog.Default(),
		&fakeRunner{},
	)

	_, err := service.OpenMarkdown(context.Background(), "ws-traversal", "../secrets.md")
	if !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("expected invalid target, got %v", err)
	}
}

func TestSearchReturnsUnavailableWhenBinaryMissing(t *testing.T) {
	root := t.TempDir()
	workspaceID := "ws-unavailable"
	workspacePath := filepath.Join(root, workspaceID)
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	runner := &fakeRunner{
		resolver: func(cmd *exec.Cmd) ([]byte, error) {
			return nil, exec.ErrNotFound
		},
	}
	service := newService(
		Config{
			WorkspaceRoot: root,
			AutoEmbed:     false,
		},
		slog.Default(),
		runner,
	)

	_, err := service.Search(context.Background(), workspaceID, "anything", 1)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected unavailable error, got %v", err)
	}
}

func TestQueueWorkspaceIndexDebounces(t *testing.T) {
	root := t.TempDir()
	workspaceID := "ws-debounce"
	workspacePath := filepath.Join(root, workspaceID)
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	runner := &fakeRunner{
		resolver: func(cmd *exec.Cmd) ([]byte, error) {
			return []byte("ok"), nil
		},
	}
	service := newService(
		Config{
			WorkspaceRoot: root,
			Debounce:      25 * time.Millisecond,
			IndexTimeout:  2 * time.Second,
			AutoEmbed:     false,
		},
		slog.Default(),
		runner,
	)
	defer service.Close()

	service.QueueWorkspaceIndex(workspaceID)
	time.Sleep(8 * time.Millisecond)
	service.QueueWorkspaceIndex(workspaceID)
	time.Sleep(90 * time.Millisecond)

	calls := runner.callsSnapshot()
	updateCalls := 0
	for _, call := range calls {
		if strings.Contains(call, " update") {
			updateCalls++
		}
	}
	if updateCalls != 1 {
		t.Fatalf("expected one update call, got %d (all calls=%v)", updateCalls, calls)
	}
}

func TestQueueWorkspaceIndexForExcludedPathSkipsEmbed(t *testing.T) {
	root := t.TempDir()
	workspaceID := "ws-excluded"
	workspacePath := filepath.Join(root, workspaceID)
	if err := os.MkdirAll(filepath.Join(workspacePath, "logs", "chats"), 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	runner := &fakeRunner{
		resolver: func(cmd *exec.Cmd) ([]byte, error) {
			args := strings.Join(cmd.Args, " ")
			if strings.Contains(args, " update") {
				return []byte("Indexed: 0 new, 0 updated, 1 unchanged, 0 removed"), nil
			}
			return []byte("ok"), nil
		},
	}
	service := newService(
		Config{
			WorkspaceRoot: root,
			Debounce:      25 * time.Millisecond,
			IndexTimeout:  2 * time.Second,
			AutoEmbed:     true,
			EmbedExclude:  []string{"logs/chats/**"},
		},
		slog.Default(),
		runner,
	)
	defer service.Close()

	service.QueueWorkspaceIndexForPath(workspaceID, filepath.Join(workspacePath, "logs", "chats", "discord.md"))
	time.Sleep(120 * time.Millisecond)

	calls := runner.callsSnapshot()
	for _, call := range calls {
		if strings.Contains(call, " embed") {
			t.Fatalf("expected embed to be skipped for excluded path, got calls=%v", calls)
		}
	}
}

func TestIndexWorkspaceSkipsEmbedWhenUpdateHasNoPendingVectors(t *testing.T) {
	root := t.TempDir()
	workspaceID := "ws-no-embed-needed"
	workspacePath := filepath.Join(root, workspaceID)
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	runner := &fakeRunner{
		resolver: func(cmd *exec.Cmd) ([]byte, error) {
			args := strings.Join(cmd.Args, " ")
			switch {
			case strings.Contains(args, " update"):
				return []byte("Indexed: 0 new, 0 updated, 10 unchanged, 0 removed"), nil
			default:
				return []byte("ok"), nil
			}
		},
	}
	service := newService(
		Config{
			WorkspaceRoot: root,
			AutoEmbed:     true,
		},
		slog.Default(),
		runner,
	)

	if err := service.IndexWorkspace(context.Background(), workspaceID); err != nil {
		t.Fatalf("index workspace failed: %v", err)
	}

	calls := runner.callsSnapshot()
	for _, call := range calls {
		if strings.Contains(call, " embed") {
			t.Fatalf("expected embed to be skipped when update reports no pending vectors, got calls=%v", calls)
		}
	}
}

func TestIndexWorkspaceRunsEmbedWhenUpdateReportsPendingVectors(t *testing.T) {
	root := t.TempDir()
	workspaceID := "ws-embed-needed"
	workspacePath := filepath.Join(root, workspaceID)
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	runner := &fakeRunner{
		resolver: func(cmd *exec.Cmd) ([]byte, error) {
			args := strings.Join(cmd.Args, " ")
			switch {
			case strings.Contains(args, " update"):
				return []byte("Run 'qmd embed' to update embeddings (2 unique hashes need vectors)"), nil
			default:
				return []byte("ok"), nil
			}
		},
	}
	service := newService(
		Config{
			WorkspaceRoot: root,
			AutoEmbed:     true,
		},
		slog.Default(),
		runner,
	)

	if err := service.IndexWorkspace(context.Background(), workspaceID); err != nil {
		t.Fatalf("index workspace failed: %v", err)
	}

	calls := runner.callsSnapshot()
	seenEmbed := false
	for _, call := range calls {
		if strings.Contains(call, " embed") {
			seenEmbed = true
			break
		}
	}
	if !seenEmbed {
		t.Fatalf("expected embed call when update reports pending vectors, got calls=%v", calls)
	}
}

func TestStatusReturnsWorkspaceMetadata(t *testing.T) {
	root := t.TempDir()
	workspaceID := "ws-status"
	workspacePath := filepath.Join(root, workspaceID)
	if err := os.MkdirAll(filepath.Join(workspacePath, ".qmd", "cache", "qmd"), 0o755); err != nil {
		t.Fatalf("create qmd dirs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspacePath, ".qmd", "cache", "qmd", "index.sqlite"), []byte("db"), 0o644); err != nil {
		t.Fatalf("write index file: %v", err)
	}

	runner := &fakeRunner{
		resolver: func(cmd *exec.Cmd) ([]byte, error) {
			return []byte("collections: 1"), nil
		},
	}
	service := newService(
		Config{
			WorkspaceRoot: root,
		},
		slog.Default(),
		runner,
	)

	status, err := service.Status(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !status.WorkspaceExist {
		t.Fatal("expected workspace to exist")
	}
	if !status.IndexExists {
		t.Fatal("expected index file to exist")
	}
	if !strings.Contains(status.Summary, "collections") {
		t.Fatalf("expected qmd summary, got %q", status.Summary)
	}
}

func TestRunQMDRepairsSqliteVecDoubleSOAndRetries(t *testing.T) {
	root := t.TempDir()
	workspaceID := "ws-repair"
	workspacePath := filepath.Join(root, workspaceID)
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	extDir := filepath.Join(root, "sqlite-vec-linux-arm64")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatalf("create extension dir: %v", err)
	}
	basePath := filepath.Join(extDir, "vec0.so")
	if err := os.WriteFile(basePath, []byte("fake"), 0o755); err != nil {
		t.Fatalf("write extension file: %v", err)
	}
	missingPath := basePath + ".so"

	var attempts int
	runner := &fakeRunner{
		resolver: func(cmd *exec.Cmd) ([]byte, error) {
			attempts++
			if attempts == 1 {
				message := "error: Error loading shared library " + missingPath + ": No such file or directory"
				return []byte(message), errors.New("exit status 1")
			}
			return []byte("ok"), nil
		},
	}

	service := newService(
		Config{
			WorkspaceRoot: root,
			AutoEmbed:     false,
		},
		slog.Default(),
		runner,
	)

	output, err := service.runQMD(context.Background(), workspacePath, "status")
	if err != nil {
		t.Fatalf("run qmd failed: %v", err)
	}
	if strings.TrimSpace(string(output)) != "ok" {
		t.Fatalf("unexpected output: %s", output)
	}
	if attempts != 2 {
		t.Fatalf("expected retry after repair, got %d attempts", attempts)
	}
	if _, err := os.Lstat(missingPath); err != nil {
		t.Fatalf("expected compatibility path to exist: %v", err)
	}
}

func TestRunQMDRetriesOnSQLiteBusy(t *testing.T) {
	root := t.TempDir()
	workspacePath := filepath.Join(root, "ws-busy")

	var attempts int
	runner := &fakeRunner{
		resolver: func(cmd *exec.Cmd) ([]byte, error) {
			attempts++
			if attempts < 3 {
				return []byte("SQLiteError: database is locked\ncode: \"SQLITE_BUSY\""), errors.New("exit status 1")
			}
			return []byte("ok"), nil
		},
	}

	service := newService(
		Config{
			WorkspaceRoot: root,
			AutoEmbed:     false,
		},
		slog.Default(),
		runner,
	)

	output, err := service.runQMD(context.Background(), workspacePath, "status")
	if err != nil {
		t.Fatalf("run qmd failed: %v", err)
	}
	if strings.TrimSpace(string(output)) != "ok" {
		t.Fatalf("unexpected output: %s", output)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestRunQMDSerializesCommandsPerWorkspace(t *testing.T) {
	root := t.TempDir()
	workspacePath := filepath.Join(root, "ws-serialized")

	var inFlight int32
	var maxInFlight int32
	runner := &fakeRunner{
		resolver: func(cmd *exec.Cmd) ([]byte, error) {
			current := atomic.AddInt32(&inFlight, 1)
			for {
				previous := atomic.LoadInt32(&maxInFlight)
				if current <= previous {
					break
				}
				if atomic.CompareAndSwapInt32(&maxInFlight, previous, current) {
					break
				}
			}
			time.Sleep(70 * time.Millisecond)
			atomic.AddInt32(&inFlight, -1)
			return []byte("ok"), nil
		},
	}

	service := newService(
		Config{
			WorkspaceRoot: root,
			AutoEmbed:     false,
		},
		slog.Default(),
		runner,
	)

	var wg sync.WaitGroup
	wg.Add(2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			if _, err := service.runQMD(context.Background(), workspacePath, "status"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("run qmd failed: %v", err)
		}
	}
	if atomic.LoadInt32(&maxInFlight) != 1 {
		t.Fatalf("expected serialized runs, max in-flight = %d", atomic.LoadInt32(&maxInFlight))
	}
}

func TestRunQMDRetriesOnModelRenameENOENT(t *testing.T) {
	root := t.TempDir()
	workspacePath := filepath.Join(root, "ws-model")

	var attempts int
	runner := &fakeRunner{
		resolver: func(cmd *exec.Cmd) ([]byte, error) {
			attempts++
			if attempts < 3 {
				return []byte("ENOENT: no such file or directory, rename '/tmp/model.gguf.ipull' -> '/tmp/model.gguf'"), errors.New("exit status 1")
			}
			return []byte("ok"), nil
		},
	}

	service := newService(
		Config{
			WorkspaceRoot: root,
			AutoEmbed:     false,
		},
		slog.Default(),
		runner,
	)

	output, err := service.runQMD(context.Background(), workspacePath, "status")
	if err != nil {
		t.Fatalf("run qmd failed: %v", err)
	}
	if strings.TrimSpace(string(output)) != "ok" {
		t.Fatalf("unexpected output: %s", output)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestRunQMDSerializesCommandsAcrossServiceInstances(t *testing.T) {
	root := t.TempDir()
	workspacePath := filepath.Join(root, "ws-cross-service")

	var inFlight int32
	var maxInFlight int32
	sharedRunner := &fakeRunner{
		resolver: func(cmd *exec.Cmd) ([]byte, error) {
			current := atomic.AddInt32(&inFlight, 1)
			for {
				previous := atomic.LoadInt32(&maxInFlight)
				if current <= previous {
					break
				}
				if atomic.CompareAndSwapInt32(&maxInFlight, previous, current) {
					break
				}
			}
			time.Sleep(70 * time.Millisecond)
			atomic.AddInt32(&inFlight, -1)
			return []byte("ok"), nil
		},
	}

	serviceA := newService(
		Config{
			WorkspaceRoot: root,
			AutoEmbed:     false,
		},
		slog.Default(),
		sharedRunner,
	)
	serviceB := newService(
		Config{
			WorkspaceRoot: root,
			AutoEmbed:     false,
		},
		slog.Default(),
		sharedRunner,
	)

	var wg sync.WaitGroup
	wg.Add(2)
	errs := make(chan error, 2)
	go func() {
		defer wg.Done()
		if _, err := serviceA.runQMD(context.Background(), workspacePath, "status"); err != nil {
			errs <- err
		}
	}()
	go func() {
		defer wg.Done()
		if _, err := serviceB.runQMD(context.Background(), workspacePath, "status"); err != nil {
			errs <- err
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("run qmd failed: %v", err)
		}
	}
	if atomic.LoadInt32(&maxInFlight) != 1 {
		t.Fatalf("expected file-lock serialization, max in-flight = %d", atomic.LoadInt32(&maxInFlight))
	}
}

func TestRunQMDUsesSharedModelsDir(t *testing.T) {
	root := t.TempDir()
	workspacePath := filepath.Join(root, "ws-shared-models")
	sharedModels := filepath.Join(root, "shared-qmd-models")

	runner := &fakeRunner{
		resolver: func(cmd *exec.Cmd) ([]byte, error) {
			return []byte("ok"), nil
		},
	}

	service := newService(
		Config{
			WorkspaceRoot:   root,
			SharedModelsDir: sharedModels,
			AutoEmbed:       false,
		},
		slog.Default(),
		runner,
	)

	if _, err := service.runQMD(context.Background(), workspacePath, "status"); err != nil {
		t.Fatalf("run qmd failed: %v", err)
	}

	modelsPath := filepath.Join(workspacePath, ".qmd", "home", ".cache", "qmd", "models")
	info, err := os.Lstat(modelsPath)
	if err != nil {
		t.Fatalf("stat models symlink failed: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected %s to be symlink", modelsPath)
	}
	target, err := os.Readlink(modelsPath)
	if err != nil {
		t.Fatalf("readlink failed: %v", err)
	}
	if filepath.Clean(target) != filepath.Clean(sharedModels) {
		t.Fatalf("expected symlink target %s, got %s", sharedModels, target)
	}
}

func TestRunQMDMigratesExistingWorkspaceModelsToSharedDir(t *testing.T) {
	root := t.TempDir()
	workspacePath := filepath.Join(root, "ws-shared-migrate")
	workspaceModels := filepath.Join(workspacePath, ".qmd", "home", ".cache", "qmd", "models")
	sharedModels := filepath.Join(root, "shared-qmd-models")

	if err := os.MkdirAll(workspaceModels, 0o755); err != nil {
		t.Fatalf("mkdir workspace models: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceModels, "existing.gguf"), []byte("model"), 0o644); err != nil {
		t.Fatalf("write workspace model: %v", err)
	}

	runner := &fakeRunner{
		resolver: func(cmd *exec.Cmd) ([]byte, error) {
			return []byte("ok"), nil
		},
	}

	service := newService(
		Config{
			WorkspaceRoot:   root,
			SharedModelsDir: sharedModels,
			AutoEmbed:       false,
		},
		slog.Default(),
		runner,
	)

	if _, err := service.runQMD(context.Background(), workspacePath, "status"); err != nil {
		t.Fatalf("run qmd failed: %v", err)
	}

	modelsPath := filepath.Join(workspacePath, ".qmd", "home", ".cache", "qmd", "models")
	info, err := os.Lstat(modelsPath)
	if err != nil {
		t.Fatalf("stat models path failed: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected migrated models path to be symlink")
	}
	if _, err := os.Stat(filepath.Join(sharedModels, "existing.gguf")); err != nil {
		t.Fatalf("expected model moved into shared dir: %v", err)
	}
}

func TestIndexWorkspaceContinuesOnKnownBunEmbedCrash(t *testing.T) {
	root := t.TempDir()
	workspaceID := "ws-embed-bun-crash"
	workspacePath := filepath.Join(root, workspaceID)
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	runner := &fakeRunner{
		resolver: func(cmd *exec.Cmd) ([]byte, error) {
			args := strings.Join(cmd.Args, " ")
			switch {
			case strings.Contains(args, " update"):
				return []byte("Run 'qmd embed' to update embeddings (1 unique hashes need vectors)"), nil
			case strings.Contains(args, " embed"):
				return []byte("ASSERTION FAILED: Attempted to call a non-GC-safe function inside a NAPI finalizer\npanic: Aborted\noh no: Bun has crashed."), errors.New("exit status 1")
			default:
				return []byte("ok"), nil
			}
		},
	}

	service := newService(
		Config{
			WorkspaceRoot: root,
			AutoEmbed:     true,
		},
		slog.Default(),
		runner,
	)

	if err := service.IndexWorkspace(context.Background(), workspaceID); err != nil {
		t.Fatalf("index workspace failed: %v", err)
	}

	service.mu.Lock()
	indexed := service.indexed[workspaceID]
	service.mu.Unlock()
	if !indexed {
		t.Fatal("expected workspace to be marked indexed even when embed crashes with known Bun issue")
	}
}

func TestIndexWorkspaceFailsOnUnknownEmbedError(t *testing.T) {
	root := t.TempDir()
	workspaceID := "ws-embed-error"
	workspacePath := filepath.Join(root, workspaceID)
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	runner := &fakeRunner{
		resolver: func(cmd *exec.Cmd) ([]byte, error) {
			args := strings.Join(cmd.Args, " ")
			switch {
			case strings.Contains(args, " update"):
				return []byte("Run 'qmd embed' to update embeddings (1 unique hashes need vectors)"), nil
			case strings.Contains(args, " embed"):
				return []byte("embedding failed: provider returned 500"), errors.New("exit status 1")
			default:
				return []byte("ok"), nil
			}
		},
	}

	service := newService(
		Config{
			WorkspaceRoot: root,
			AutoEmbed:     true,
		},
		slog.Default(),
		runner,
	)

	if err := service.IndexWorkspace(context.Background(), workspaceID); err == nil {
		t.Fatal("expected unknown embed error to fail indexing")
	}
}
