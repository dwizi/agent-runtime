package envsync

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Result struct {
	EnvPath     string
	PKIDir      string
	UpdatedKeys []string
	BackupPath  string
	Skipped     bool
	Reason      string
}

func SyncLocalPKIEnv() (Result, error) {
	rootDir, err := os.Getwd()
	if err != nil {
		return Result{}, fmt.Errorf("resolve working directory: %w", err)
	}

	envPath := filepath.Join(rootDir, ".env")
	envExamplePath := filepath.Join(rootDir, ".env.example")
	pkiDir := filepath.Join(rootDir, "ops", "caddy", "pki")
	result := Result{EnvPath: envPath, PKIDir: pkiDir}

	envExisted := true
	if _, err := os.Stat(envPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return result, fmt.Errorf("check .env file: %w", err)
		}
		envExisted = false
		if _, exampleErr := os.Stat(envExamplePath); exampleErr == nil {
			if copyErr := copyFile(envExamplePath, envPath); copyErr != nil {
				return result, fmt.Errorf("create .env from .env.example: %w", copyErr)
			}
		} else if errors.Is(exampleErr, os.ErrNotExist) {
			if writeErr := os.WriteFile(envPath, []byte{}, 0o644); writeErr != nil {
				return result, fmt.Errorf("create empty .env: %w", writeErr)
			}
		} else {
			return result, fmt.Errorf("check .env.example file: %w", exampleErr)
		}
	}

	requiredFiles := map[string]string{
		"SPINNER_ADMIN_TLS_CA_FILE":   filepath.Join(pkiDir, "clients-ca.crt"),
		"SPINNER_ADMIN_TLS_CERT_FILE": filepath.Join(pkiDir, "admin-client.crt"),
		"SPINNER_ADMIN_TLS_KEY_FILE":  filepath.Join(pkiDir, "admin-client.key"),
	}

	for _, path := range requiredFiles {
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				result.Skipped = true
				result.Reason = "pki files not found"
				return result, nil
			}
			return result, fmt.Errorf("check pki file %s: %w", path, err)
		}
	}
	if !looksLikePEMCert(requiredFiles["SPINNER_ADMIN_TLS_CA_FILE"]) ||
		!looksLikePEMCert(requiredFiles["SPINNER_ADMIN_TLS_CERT_FILE"]) ||
		!looksLikePEMKey(requiredFiles["SPINNER_ADMIN_TLS_KEY_FILE"]) {
		result.Skipped = true
		result.Reason = "pki files are not valid PEM yet"
		return result, nil
	}

	lines, err := readLines(envPath)
	if err != nil {
		return result, err
	}

	for key, value := range requiredFiles {
		changed := setIfEmpty(&lines, key, value)
		if changed {
			result.UpdatedKeys = append(result.UpdatedKeys, key)
		}
	}

	if len(result.UpdatedKeys) > 0 {
		if envExisted {
			backupPath, err := backupFile(envPath)
			if err != nil {
				return result, err
			}
			result.BackupPath = backupPath
		}
		if err := writeLines(envPath, lines); err != nil {
			return result, err
		}
	}

	return result, nil
}

func readLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open env file: %w", err)
	}
	defer file.Close()

	lines := []string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read env file: %w", err)
	}
	return lines, nil
}

func writeLines(path string, lines []string) error {
	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write env file: %w", err)
	}
	return nil
}

func setIfEmpty(lines *[]string, key, value string) bool {
	prefix := key + "="
	index := -1
	for i, line := range *lines {
		if strings.HasPrefix(line, prefix) {
			index = i
			break
		}
	}

	if index == -1 {
		*lines = append(*lines, prefix+value)
		return true
	}

	currentValue := strings.TrimSpace(strings.TrimPrefix((*lines)[index], prefix))
	if currentValue != "" {
		return false
	}
	(*lines)[index] = prefix + value
	return true
}

func copyFile(src, dst string) error {
	bytes, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, bytes, 0o644)
}

func backupFile(path string) (string, error) {
	stamp := time.Now().UTC().Format("20060102150405")
	backupPath := path + ".bak." + stamp
	if err := copyFile(path, backupPath); err != nil {
		return "", fmt.Errorf("backup env file: %w", err)
	}
	return backupPath, nil
}

func looksLikePEMCert(path string) bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(content), "BEGIN CERTIFICATE")
}

func looksLikePEMKey(path string) bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	text := string(content)
	return strings.Contains(text, "BEGIN PRIVATE KEY") ||
		strings.Contains(text, "BEGIN RSA PRIVATE KEY") ||
		strings.Contains(text, "BEGIN EC PRIVATE KEY")
}
