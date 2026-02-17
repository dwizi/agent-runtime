package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (c *Connector) Publish(ctx context.Context, externalID, text string) error {
	chatID, err := strconv.ParseInt(strings.TrimSpace(externalID), 10, 64)
	if err != nil {
		return fmt.Errorf("parse telegram external id: %w", err)
	}
	message := strings.TrimSpace(text)
	if message == "" {
		return nil
	}
	return c.sendMessage(ctx, chatID, message)
}

func (c *Connector) Start(ctx context.Context) error {
	if c.reporter != nil {
		c.reporter.Starting("connector:telegram", "starting")
	}
	if c.token == "" {
		if c.reporter != nil {
			c.reporter.Disabled("connector:telegram", "token missing")
		}
		c.logger.Info("connector disabled, token missing")
		<-ctx.Done()
		return nil
	}
	if c.pairings == nil {
		if c.reporter != nil {
			c.reporter.Disabled("connector:telegram", "pairing store missing")
		}
		c.logger.Info("connector disabled, pairing store missing")
		<-ctx.Done()
		return nil
	}
	if c.gateway == nil {
		if c.reporter != nil {
			c.reporter.Disabled("connector:telegram", "gateway missing")
		}
		c.logger.Info("connector disabled, gateway missing")
		<-ctx.Done()
		return nil
	}

	if c.reporter != nil {
		c.reporter.Beat("connector:telegram", "polling updates")
	}
	c.logger.Info("connector started", "api_base", c.apiBase)
	if username, err := c.fetchBotUsername(ctx); err == nil {
		c.botUsername = username
		if c.botUsername != "" {
			c.logger.Info("telegram bot identity loaded", "username", c.botUsername)
		}
	} else {
		c.logger.Warn("telegram bot username lookup failed", "error", err)
	}
	if c.commandSync {
		if err := c.syncCommands(ctx); err != nil {
			c.logger.Warn("telegram command sync failed", "error", err)
		} else {
			c.logger.Info("telegram commands synced")
		}
	}

	for {
		if ctx.Err() != nil {
			if c.reporter != nil {
				c.reporter.Stopped("connector:telegram", "stopped")
			}
			c.logger.Info("connector stopped")
			return nil
		}
		if err := c.pollOnce(ctx); err != nil && ctx.Err() == nil {
			if c.reporter != nil {
				c.reporter.Degrade("connector:telegram", "poll failed", err)
			}
			c.logger.Error("poll failed", "error", err)
			select {
			case <-ctx.Done():
				if c.reporter != nil {
					c.reporter.Stopped("connector:telegram", "stopped")
				}
				c.logger.Info("connector stopped")
				return nil
			case <-time.After(1500 * time.Millisecond):
			}
		} else if c.reporter != nil {
			c.reporter.Beat("connector:telegram", "poll cycle ok")
		}
	}
}

func (c *Connector) pollOnce(ctx context.Context) error {
	url := fmt.Sprintf("%s/bot%s/getUpdates?timeout=%d&offset=%d", c.apiBase, c.token, c.pollSeconds, c.offset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	var payload getUpdatesResponse
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return fmt.Errorf("decode getUpdates: %w", err)
	}
	if !payload.OK {
		return fmt.Errorf("telegram getUpdates failed")
	}

	for _, update := range payload.Result {
		if update.UpdateID >= c.offset {
			c.offset = update.UpdateID + 1
		}
		if update.Message == nil {
			continue
		}
		if err := c.handleMessage(ctx, *update.Message); err != nil {
			c.logger.Error("handle message failed", "error", err, "update_id", update.UpdateID)
		}
	}
	return nil
}
