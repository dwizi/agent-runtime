package envsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncLocalPKIEnvFillsEmptyValues(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	writeFile(t, filepath.Join(tempDir, ".env"), strings.Join([]string{
		"SPINNER_ADMIN_TLS_CA_FILE=",
		"SPINNER_ADMIN_TLS_CERT_FILE=",
		"SPINNER_ADMIN_TLS_KEY_FILE=",
	}, "\n")+"\n")
	writeFile(t, filepath.Join(tempDir, "ops/caddy/pki/clients-ca.crt"), "-----BEGIN CERTIFICATE-----\nca\n-----END CERTIFICATE-----\n")
	writeFile(t, filepath.Join(tempDir, "ops/caddy/pki/admin-client.crt"), "-----BEGIN CERTIFICATE-----\ncrt\n-----END CERTIFICATE-----\n")
	writeFile(t, filepath.Join(tempDir, "ops/caddy/pki/admin-client.key"), "-----BEGIN PRIVATE KEY-----\nkey\n-----END PRIVATE KEY-----\n")

	result, err := SyncLocalPKIEnv()
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if result.Skipped {
		t.Fatal("expected sync not skipped")
	}
	if len(result.UpdatedKeys) != 3 {
		t.Fatalf("expected 3 updated keys, got %d", len(result.UpdatedKeys))
	}
	if strings.TrimSpace(result.BackupPath) == "" {
		t.Fatal("expected backup path when env is updated")
	}
	if _, err := os.Stat(result.BackupPath); err != nil {
		t.Fatalf("expected backup file to exist: %v", err)
	}

	envContent := readFile(t, filepath.Join(tempDir, ".env"))
	if !strings.Contains(envContent, "SPINNER_ADMIN_TLS_CA_FILE="+filepath.Join(tempDir, "ops/caddy/pki/clients-ca.crt")) {
		t.Fatalf("expected ca path to be set, got %s", envContent)
	}
	if !strings.Contains(envContent, "SPINNER_ADMIN_TLS_CERT_FILE="+filepath.Join(tempDir, "ops/caddy/pki/admin-client.crt")) {
		t.Fatalf("expected cert path to be set, got %s", envContent)
	}
	if !strings.Contains(envContent, "SPINNER_ADMIN_TLS_KEY_FILE="+filepath.Join(tempDir, "ops/caddy/pki/admin-client.key")) {
		t.Fatalf("expected key path to be set, got %s", envContent)
	}
}

func TestSyncLocalPKIEnvDoesNotOverwriteExisting(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	writeFile(t, filepath.Join(tempDir, ".env"), strings.Join([]string{
		"SPINNER_ADMIN_TLS_CA_FILE=/custom/ca.crt",
		"SPINNER_ADMIN_TLS_CERT_FILE=",
		"SPINNER_ADMIN_TLS_KEY_FILE=",
	}, "\n")+"\n")
	writeFile(t, filepath.Join(tempDir, "ops/caddy/pki/clients-ca.crt"), "-----BEGIN CERTIFICATE-----\nca\n-----END CERTIFICATE-----\n")
	writeFile(t, filepath.Join(tempDir, "ops/caddy/pki/admin-client.crt"), "-----BEGIN CERTIFICATE-----\ncrt\n-----END CERTIFICATE-----\n")
	writeFile(t, filepath.Join(tempDir, "ops/caddy/pki/admin-client.key"), "-----BEGIN PRIVATE KEY-----\nkey\n-----END PRIVATE KEY-----\n")

	result, err := SyncLocalPKIEnv()
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if len(result.UpdatedKeys) != 2 {
		t.Fatalf("expected 2 updated keys, got %d", len(result.UpdatedKeys))
	}

	envContent := readFile(t, filepath.Join(tempDir, ".env"))
	if !strings.Contains(envContent, "SPINNER_ADMIN_TLS_CA_FILE=/custom/ca.crt") {
		t.Fatalf("expected existing ca path to be preserved, got %s", envContent)
	}
}

func TestSyncLocalPKIEnvSkipsWhenMissingPKI(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	writeFile(t, filepath.Join(tempDir, ".env"), "")

	result, err := SyncLocalPKIEnv()
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if !result.Skipped {
		t.Fatal("expected sync to be skipped when pki files are missing")
	}
}

func TestSyncLocalPKIEnvSkipsWhenInvalidPEM(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	writeFile(t, filepath.Join(tempDir, ".env"), "")
	writeFile(t, filepath.Join(tempDir, "ops/caddy/pki/clients-ca.crt"), "")
	writeFile(t, filepath.Join(tempDir, "ops/caddy/pki/admin-client.crt"), "")
	writeFile(t, filepath.Join(tempDir, "ops/caddy/pki/admin-client.key"), "")

	result, err := SyncLocalPKIEnv()
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if !result.Skipped {
		t.Fatal("expected sync to be skipped when pki files are invalid")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(bytes)
}
