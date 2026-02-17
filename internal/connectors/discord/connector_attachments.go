package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

func (c *Connector) ingestMarkdownAttachments(ctx context.Context, message discordMessageCreate) (string, error) {
	if c.workspace == "" || c.pairings == nil || len(message.Attachments) == 0 {
		return "", nil
	}
	displayName := message.ChannelID
	if message.GuildID != "" {
		displayName = message.GuildID
	}
	contextRecord, err := c.pairings.EnsureContextForExternalChannel(
		ctx,
		"discord",
		message.ChannelID,
		displayName,
	)
	if err != nil {
		return "", err
	}

	workspacePath := filepath.Join(c.workspace, contextRecord.WorkspaceID)
	targetDir := filepath.Join(workspacePath, "inbox", "discord", message.ChannelID)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", err
	}

	saved := []string{}
	for _, attachment := range message.Attachments {
		filename := sanitizeFilename(attachment.Filename)
		if !isMarkdown(filename, attachment.ContentType) {
			continue
		}
		content, err := c.downloadAttachment(ctx, attachment.URL)
		if err != nil {
			c.logger.Error("download discord attachment failed", "error", err, "url", attachment.URL)
			continue
		}
		targetName := fmt.Sprintf("%s-%s", message.ID, filename)
		targetPath := filepath.Join(targetDir, targetName)
		if err := os.WriteFile(targetPath, content, 0o644); err != nil {
			return "", err
		}
		relativePath, err := filepath.Rel(workspacePath, targetPath)
		if err != nil {
			relativePath = targetName
		}
		saved = append(saved, filepath.ToSlash(relativePath))
	}
	if len(saved) == 0 {
		return "", nil
	}
	if len(saved) == 1 {
		return fmt.Sprintf("Attachment saved: `%s`", saved[0]), nil
	}
	return fmt.Sprintf("Saved %d markdown attachments.", len(saved)), nil
}

func (c *Connector) downloadAttachment(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bot "+c.token)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("discord attachment download failed with status %d", res.StatusCode)
	}
	return ioReadAllLimited(res.Body, 2<<20)
}

func (c *Connector) sendChannelMessage(ctx context.Context, channelID, content string) error {
	endpoint := fmt.Sprintf("%s/channels/%s/messages", c.apiBase, channelID)
	body := map[string]string{"content": content}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+c.token)
	req.Header.Set("User-Agent", "agent-runtime/0.1")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return fmt.Errorf("discord send message failed: status=%d body=%s", res.StatusCode, string(bodyBytes))
	}
	return nil
}
