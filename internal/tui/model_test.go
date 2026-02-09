package tui

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/carlos/spinner/internal/config"
)

func TestRecoverInvalidTLSConfigClearsInvalidPair(t *testing.T) {
	tempDir := t.TempDir()
	invalidCert := filepath.Join(tempDir, "invalid.crt")
	invalidKey := filepath.Join(tempDir, "invalid.key")
	if err := os.WriteFile(invalidCert, []byte("not-a-cert"), 0o644); err != nil {
		t.Fatalf("write invalid cert: %v", err)
	}
	if err := os.WriteFile(invalidKey, []byte("not-a-key"), 0o644); err != nil {
		t.Fatalf("write invalid key: %v", err)
	}

	cfg := config.Config{
		AdminTLSCertFile: invalidCert,
		AdminTLSKeyFile:  invalidKey,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	updated, _ := recoverInvalidTLSConfig(cfg, "", logger)
	if updated.AdminTLSCertFile != "" || updated.AdminTLSKeyFile != "" {
		t.Fatal("expected invalid client cert config to be cleared")
	}
}
