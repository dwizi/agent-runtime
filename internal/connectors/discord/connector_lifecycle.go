package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

func (c *Connector) Publish(ctx context.Context, externalID, text string) error {
	channelID := strings.TrimSpace(externalID)
	if channelID == "" {
		return fmt.Errorf("discord external id is required")
	}
	content := strings.TrimSpace(text)
	if content == "" {
		return nil
	}
	return c.sendChannelMessage(ctx, channelID, content)
}

func (c *Connector) Start(ctx context.Context) error {
	if c.reporter != nil {
		c.reporter.Starting("connector:discord", "starting")
	}
	if c.token == "" {
		if c.reporter != nil {
			c.reporter.Disabled("connector:discord", "token missing")
		}
		c.logger.Info("connector disabled, token missing")
		<-ctx.Done()
		return nil
	}
	if c.pairings == nil || c.gateway == nil {
		if c.reporter != nil {
			c.reporter.Disabled("connector:discord", "dependencies missing")
		}
		c.logger.Info("connector disabled, dependencies missing")
		<-ctx.Done()
		return nil
	}

	if c.reporter != nil {
		c.reporter.Beat("connector:discord", "gateway session loop active")
	}
	c.logger.Info("connector started", "mode", "gateway")
	if c.commandSync {
		if err := c.syncCommands(ctx); err != nil {
			c.logger.Warn("discord command sync failed", "error", err)
		} else {
			c.logger.Info("discord commands synced", "guild_count", len(c.commandGuildIDs))
		}
	}
	for {
		if ctx.Err() != nil {
			if c.reporter != nil {
				c.reporter.Stopped("connector:discord", "stopped")
			}
			c.logger.Info("connector stopped")
			return nil
		}
		if err := c.runSession(ctx); err != nil {
			if ctx.Err() != nil {
				if c.reporter != nil {
					c.reporter.Stopped("connector:discord", "stopped")
				}
				c.logger.Info("connector stopped")
				return nil
			}
			if c.reporter != nil {
				c.reporter.Degrade("connector:discord", "gateway session error", err)
			}
			c.logger.Error("discord session ended, reconnecting", "error", err)
			select {
			case <-ctx.Done():
				if c.reporter != nil {
					c.reporter.Stopped("connector:discord", "stopped")
				}
				c.logger.Info("connector stopped")
				return nil
			case <-time.After(2 * time.Second):
			}
		}
	}
}

func (c *Connector) runSession(ctx context.Context) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.gatewayURL, nil)
	if err != nil {
		return fmt.Errorf("dial discord gateway: %w", err)
	}
	defer conn.Close()

	var (
		writeMu      sync.Mutex
		sequence     int64
		heartbeatSec = 30 * time.Second
	)

	readHelloDone := false
	for !readHelloDone {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read hello: %w", err)
		}
		var envelope gatewayEnvelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			return fmt.Errorf("decode hello payload: %w", err)
		}
		if envelope.Op != 10 {
			continue
		}
		var hello discordHello
		if err := json.Unmarshal(envelope.D, &hello); err != nil {
			return fmt.Errorf("decode hello body: %w", err)
		}
		heartbeatSec = time.Duration(hello.HeartbeatIntervalMS) * time.Millisecond
		readHelloDone = true
	}

	if err := c.sendIdentify(conn, &writeMu); err != nil {
		return err
	}
	if c.reporter != nil {
		c.reporter.Beat("connector:discord", "gateway session established")
	}

	heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)
	defer cancelHeartbeat()
	go c.heartbeatLoop(heartbeatCtx, conn, &writeMu, &sequence, heartbeatSec)

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read gateway message: %w", err)
		}

		var envelope gatewayEnvelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			c.logger.Error("decode gateway envelope failed", "error", err)
			continue
		}
		if envelope.S != nil {
			sequence = *envelope.S
		}

		switch envelope.Op {
		case 0:
			if c.reporter != nil {
				c.reporter.Beat("connector:discord", "gateway event received")
			}
			if envelope.T == "READY" {
				var ready discordReady
				if err := json.Unmarshal(envelope.D, &ready); err == nil {
					c.botUserID = strings.TrimSpace(ready.User.ID)
				}
			}
			if envelope.T == "MESSAGE_CREATE" {
				var message discordMessageCreate
				if err := json.Unmarshal(envelope.D, &message); err != nil {
					c.logger.Error("decode message create failed", "error", err)
					continue
				}
				if err := c.handleMessageCreate(ctx, message); err != nil {
					c.logger.Error("handle discord message failed", "error", err)
				}
			}
			if envelope.T == "INTERACTION_CREATE" {
				var interaction discordInteractionCreate
				if err := json.Unmarshal(envelope.D, &interaction); err != nil {
					c.logger.Error("decode interaction create failed", "error", err)
					continue
				}
				if err := c.handleInteractionCreate(ctx, interaction); err != nil {
					c.logger.Error("handle discord interaction failed", "error", err)
				}
			}
		case 1:
			if err := c.sendHeartbeat(conn, &writeMu, sequence); err != nil {
				return err
			}
		case 7:
			return fmt.Errorf("gateway requested reconnect")
		case 9:
			return fmt.Errorf("gateway invalid session")
		}
	}
}

func (c *Connector) heartbeatLoop(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, seq *int64, interval time.Duration) {
	if interval < time.Second {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.sendHeartbeat(conn, writeMu, *seq); err != nil {
				c.logger.Error("heartbeat failed", "error", err)
				return
			}
		}
	}
}

func (c *Connector) sendIdentify(conn *websocket.Conn, writeMu *sync.Mutex) error {
	payload := map[string]any{
		"op": 2,
		"d": map[string]any{
			"token": c.token,
			"intents": discordIntentGuilds |
				discordIntentGuildMessages |
				discordIntentDirectMessages |
				discordIntentMessageContents,
			"properties": map[string]string{
				"os":      "linux",
				"browser": "agent-runtime",
				"device":  "agent-runtime",
			},
		},
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	if err := conn.WriteJSON(payload); err != nil {
		return fmt.Errorf("send identify: %w", err)
	}
	return nil
}

func (c *Connector) sendHeartbeat(conn *websocket.Conn, writeMu *sync.Mutex, seq int64) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	payload := map[string]any{
		"op": 1,
		"d":  seq,
	}
	if err := conn.WriteJSON(payload); err != nil {
		return fmt.Errorf("send heartbeat: %w", err)
	}
	return nil
}
