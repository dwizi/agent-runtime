package qmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

func RunSidecar(ctx context.Context, cfg Config, addr string, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	cfg.SidecarURL = ""
	service := New(cfg, logger.With("component", "qmd-sidecar"))
	defer service.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, req *http.Request) {
		writeSidecarJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/run", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			writeSidecarJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var payload struct {
			WorkspaceDir string   `json:"workspace_dir"`
			Args         []string `json:"args"`
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			writeSidecarJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
			return
		}
		workspaceDir, err := validateWorkspacePath(cfg.WorkspaceRoot, payload.WorkspaceDir)
		if err != nil {
			writeSidecarJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if len(payload.Args) == 0 {
			writeSidecarJSON(w, http.StatusBadRequest, map[string]string{"error": "args are required"})
			return
		}

		output, err := service.runQMD(req.Context(), workspaceDir, payload.Args...)
		if err != nil {
			writeSidecarJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeSidecarJSON(w, http.StatusOK, map[string]string{
			"output": base64.StdEncoding.EncodeToString(output),
		})
	})

	server := &http.Server{
		Addr:              strings.TrimSpace(addr),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	logger.Info("qmd sidecar starting", "addr", server.Addr, "workspace_root", cfg.WorkspaceRoot)
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func validateWorkspacePath(workspaceRoot, workspaceDir string) (string, error) {
	root := filepath.Clean(strings.TrimSpace(workspaceRoot))
	path := filepath.Clean(strings.TrimSpace(workspaceDir))
	if root == "" || path == "" {
		return "", errors.New("workspace path is required")
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(relative, "..") || relative == "." {
		return "", errors.New("workspace path must be inside workspace root")
	}
	return path, nil
}

func writeSidecarJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
