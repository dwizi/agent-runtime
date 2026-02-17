package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (c *Connector) ingestMarkdownDocument(ctx context.Context, message telegramMessage, document telegramDocument) (string, error) {
	if c.workspace == "" || c.pairings == nil {
		return "", nil
	}
	filename := sanitizeFilename(document.FileName)
	if !isMarkdown(filename, document.MimeType) {
		return "", nil
	}

	contextRecord, err := c.pairings.EnsureContextForExternalChannel(
		ctx,
		"telegram",
		strconv.FormatInt(message.Chat.ID, 10),
		message.Chat.Title,
	)
	if err != nil {
		return "", err
	}
	workspacePath := filepath.Join(c.workspace, contextRecord.WorkspaceID)
	targetDir := filepath.Join(workspacePath, "inbox", "telegram", strconv.FormatInt(message.Chat.ID, 10))
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", err
	}

	filePath, err := c.lookupFilePath(ctx, document.FileID)
	if err != nil {
		return "", err
	}
	fileContent, err := c.downloadFile(ctx, filePath)
	if err != nil {
		return "", err
	}

	targetName := fmt.Sprintf("%d-%s", message.MessageID, filename)
	targetPath := filepath.Join(targetDir, targetName)
	if err := os.WriteFile(targetPath, fileContent, 0o644); err != nil {
		return "", err
	}

	relativePath, err := filepath.Rel(workspacePath, targetPath)
	if err != nil {
		relativePath = targetName
	}
	return fmt.Sprintf("Attachment saved: `%s`", filepath.ToSlash(relativePath)), nil
}

func (c *Connector) lookupFilePath(ctx context.Context, fileID string) (string, error) {
	url := fmt.Sprintf("%s/bot%s/getFile?file_id=%s", c.apiBase, c.token, fileID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	var payload struct {
		OK     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode getFile: %w", err)
	}
	if !payload.OK || strings.TrimSpace(payload.Result.FilePath) == "" {
		return "", fmt.Errorf("telegram getFile failed")
	}
	return payload.Result.FilePath, nil
}

func (c *Connector) downloadFile(ctx context.Context, filePath string) ([]byte, error) {
	url := fmt.Sprintf("%s/file/bot%s/%s", c.apiBase, c.token, strings.TrimLeft(filePath, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("telegram file download failed with status %d", res.StatusCode)
	}
	return ioReadAllLimited(res.Body, 2<<20)
}

func (c *Connector) fetchBotUsername(ctx context.Context) (string, error) {
	url := fmt.Sprintf("%s/bot%s/getMe", c.apiBase, c.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	var payload struct {
		OK     bool `json:"ok"`
		Result struct {
			Username string `json:"username"`
		} `json:"result"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return "", err
	}
	if !payload.OK {
		return "", fmt.Errorf("telegram getMe failed")
	}
	return strings.TrimSpace(payload.Result.Username), nil
}

func (c *Connector) sendMessage(ctx context.Context, chatID int64, text string) error {
	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", c.apiBase, c.token)
	body := map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	var response struct {
		OK          bool   `json:"ok"`
		ErrorCode   int    `json:"error_code"`
		Description string `json:"description"`
	}
	bodyBytes, err := io.ReadAll(io.LimitReader(res.Body, 8192))
	if err != nil {
		return fmt.Errorf("read sendMessage response: %w", err)
	}
	if err := json.Unmarshal(bodyBytes, &response); err != nil {
		return fmt.Errorf("decode sendMessage: status=%d body=%q err=%w", res.StatusCode, strings.TrimSpace(string(bodyBytes)), err)
	}
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("telegram sendMessage failed: status=%d body=%q", res.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}
	if !response.OK {
		description := strings.TrimSpace(response.Description)
		if description == "" {
			description = strings.TrimSpace(string(bodyBytes))
		}
		if response.ErrorCode > 0 {
			return fmt.Errorf("telegram sendMessage failed: status=%d error_code=%d description=%s", res.StatusCode, response.ErrorCode, description)
		}
		return fmt.Errorf("telegram sendMessage failed: status=%d description=%s", res.StatusCode, description)
	}
	return nil
}
