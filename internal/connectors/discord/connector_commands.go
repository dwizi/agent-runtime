package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/dwizi/agent-runtime/internal/gateway"
)

func (c *Connector) syncCommands(ctx context.Context) error {
	applicationID, err := c.resolveApplicationID(ctx)
	if err != nil {
		return err
	}
	commandsPayload := buildDiscordCommandPayload(gateway.SlashCommands())
	if len(commandsPayload) == 0 {
		return nil
	}
	if len(c.commandGuildIDs) == 0 {
		return c.upsertGlobalCommands(ctx, applicationID, commandsPayload)
	}
	for _, guildID := range c.commandGuildIDs {
		if err := c.upsertGuildCommands(ctx, applicationID, guildID, commandsPayload); err != nil {
			return err
		}
	}
	return nil
}

func (c *Connector) resolveApplicationID(ctx context.Context) (string, error) {
	if strings.TrimSpace(c.applicationID) != "" {
		return c.applicationID, nil
	}
	applicationID, err := c.fetchApplicationID(ctx)
	if err != nil {
		return "", err
	}
	c.applicationID = applicationID
	return applicationID, nil
}

func (c *Connector) fetchApplicationID(ctx context.Context) (string, error) {
	url := fmt.Sprintf("%s/oauth2/applications/@me", c.apiBase)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bot "+c.token)
	req.Header.Set("User-Agent", "agent-runtime/0.1")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return "", fmt.Errorf("discord application lookup failed: status=%d body=%s", res.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	var payload struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode discord application lookup: %w", err)
	}
	applicationID := strings.TrimSpace(payload.ID)
	if applicationID == "" {
		return "", fmt.Errorf("discord application lookup returned empty id")
	}
	return applicationID, nil
}

func (c *Connector) upsertGlobalCommands(ctx context.Context, applicationID string, payload []map[string]any) error {
	url := fmt.Sprintf("%s/applications/%s/commands", c.apiBase, applicationID)
	return c.putCommands(ctx, url, payload)
}

func (c *Connector) upsertGuildCommands(ctx context.Context, applicationID, guildID string, payload []map[string]any) error {
	url := fmt.Sprintf("%s/applications/%s/guilds/%s/commands", c.apiBase, applicationID, strings.TrimSpace(guildID))
	return c.putCommands(ctx, url, payload)
}

func (c *Connector) putCommands(ctx context.Context, url string, payload []map[string]any) error {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "agent-runtime/0.1")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return fmt.Errorf("discord command upsert failed: status=%d body=%s", res.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

func buildDiscordCommandPayload(commands []gateway.SlashCommand) []map[string]any {
	payload := make([]map[string]any, 0, len(commands))
	for _, command := range commands {
		name := strings.TrimSpace(command.Name)
		if name == "" {
			continue
		}
		entry := map[string]any{
			"name":        name,
			"description": discordCommandDescription(command.Description),
			"type":        1,
		}
		if strings.TrimSpace(command.ArgumentName) != "" {
			entry["options"] = []map[string]any{
				{
					"type":        3,
					"name":        command.ArgumentName,
					"description": discordCommandDescription(command.ArgumentDescription),
					"required":    command.ArgumentRequired,
				},
			}
		}
		payload = append(payload, entry)
	}
	return payload
}

func discordCommandDescription(description string) string {
	trimmed := strings.TrimSpace(description)
	if trimmed == "" {
		return "Agent Runtime command"
	}
	if len(trimmed) > 100 {
		return strings.TrimSpace(trimmed[:100])
	}
	return trimmed
}

func (c *Connector) handleInteractionCreate(ctx context.Context, interaction discordInteractionCreate) error {
	if interaction.Type != 2 {
		return nil
	}
	commandText := interactionToCommandText(interaction)
	if commandText == "" {
		return c.sendInteractionResponse(ctx, interaction.ID, interaction.Token, "Unsupported command payload.")
	}
	userID := interaction.userID()
	if userID == "" {
		return c.sendInteractionResponse(ctx, interaction.ID, interaction.Token, "Missing user context.")
	}
	displayName := strings.TrimSpace(interaction.GuildID)
	if displayName == "" {
		displayName = strings.TrimSpace(interaction.ChannelID)
	}
	output, err := c.gateway.HandleMessage(ctx, gateway.MessageInput{
		Connector:   "discord",
		ExternalID:  strings.TrimSpace(interaction.ChannelID),
		DisplayName: displayName,
		FromUserID:  userID,
		Text:        commandText,
	})
	if err != nil {
		return c.sendInteractionResponse(ctx, interaction.ID, interaction.Token, "I hit an error while running that command.")
	}
	reply := strings.TrimSpace(output.Reply)
	if reply == "" {
		reply = "Command received."
	}
	return c.sendInteractionResponse(ctx, interaction.ID, interaction.Token, clipDiscordMessage(reply))
}

func interactionToCommandText(interaction discordInteractionCreate) string {
	name := strings.TrimSpace(interaction.Data.Name)
	if name == "" {
		return ""
	}
	parts := []string{"/" + name}
	for _, option := range interaction.Data.Options {
		value := strings.TrimSpace(option.valueAsString())
		if value == "" {
			continue
		}
		parts = append(parts, value)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func (c *Connector) sendInteractionResponse(ctx context.Context, interactionID, interactionToken, content string) error {
	if strings.TrimSpace(interactionID) == "" || strings.TrimSpace(interactionToken) == "" {
		return fmt.Errorf("missing interaction id or token")
	}
	endpoint := fmt.Sprintf("%s/interactions/%s/%s/callback", c.apiBase, interactionID, interactionToken)
	body := map[string]any{
		"type": 4,
		"data": map[string]any{
			"content": content,
		},
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
	req.Header.Set("User-Agent", "agent-runtime/0.1")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return fmt.Errorf("discord interaction response failed: status=%d body=%s", res.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}
	return nil
}
