package discord

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/gateway"
	"github.com/dwizi/agent-runtime/internal/heartbeat"
	"github.com/dwizi/agent-runtime/internal/llm"
	llmsafety "github.com/dwizi/agent-runtime/internal/llm/safety"
	"github.com/dwizi/agent-runtime/internal/store"
)

const (
	pairCommand = "pair"

	discordIntentGuilds          = 1 << 0
	discordIntentGuildMessages   = 1 << 9
	discordIntentDirectMessages  = 1 << 12
	discordIntentMessageContents = 1 << 15
)

type PairingStore interface {
	CreatePairingRequest(ctx context.Context, input store.CreatePairingRequestInput) (store.PairingRequestWithToken, error)
	EnsureContextForExternalChannel(ctx context.Context, connector, externalID, displayName string) (store.ContextRecord, error)
	LookupUserIdentity(ctx context.Context, connector, connectorUserID string) (store.UserIdentity, error)
	CreateActionApproval(ctx context.Context, input store.CreateActionApprovalInput) (store.ActionApproval, error)
}

type CommandGateway interface {
	HandleMessage(ctx context.Context, input gateway.MessageInput) (gateway.MessageOutput, error)
}

type Responder interface {
	Reply(ctx context.Context, input llm.MessageInput) (string, error)
}

type SafetyPolicy interface {
	Check(input llmsafety.Request) llmsafety.Decision
}

type Connector struct {
	token           string
	apiBase         string
	gatewayURL      string
	workspace       string
	commandSync     bool
	commandGuildIDs []string
	applicationID   string
	pairings        PairingStore
	gateway         CommandGateway
	responder       Responder
	policy          SafetyPolicy
	httpClient      *http.Client
	logger          *slog.Logger
	botUserID       string
	reporter        heartbeat.Reporter
}

type Option func(*Connector)

func WithCommandSync(enabled bool) Option {
	return func(connector *Connector) {
		connector.commandSync = enabled
	}
}

func WithCommandGuildIDs(guildIDs []string) Option {
	return func(connector *Connector) {
		clean := make([]string, 0, len(guildIDs))
		seen := map[string]struct{}{}
		for _, guildID := range guildIDs {
			value := strings.TrimSpace(guildID)
			if value == "" {
				continue
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			clean = append(clean, value)
		}
		connector.commandGuildIDs = clean
	}
}

func WithApplicationID(applicationID string) Option {
	return func(connector *Connector) {
		connector.applicationID = strings.TrimSpace(applicationID)
	}
}

func New(token, apiBase, gatewayURL, workspaceRoot string, pairings PairingStore, commandGateway CommandGateway, responder Responder, policy SafetyPolicy, logger *slog.Logger, opts ...Option) *Connector {
	if strings.TrimSpace(apiBase) == "" {
		apiBase = "https://discord.com/api/v10"
	}
	if strings.TrimSpace(gatewayURL) == "" {
		gatewayURL = "wss://gateway.discord.gg/?v=10&encoding=json"
	}
	connector := &Connector{
		token:       strings.TrimSpace(token),
		apiBase:     strings.TrimRight(strings.TrimSpace(apiBase), "/"),
		gatewayURL:  strings.TrimSpace(gatewayURL),
		workspace:   strings.TrimSpace(workspaceRoot),
		commandSync: true,
		pairings:    pairings,
		gateway:     commandGateway,
		responder:   responder,
		policy:      policy,
		httpClient:  &http.Client{Timeout: 12 * time.Second},
		logger:      logger,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(connector)
		}
	}
	return connector
}

func (c *Connector) Name() string {
	return "discord"
}

func (c *Connector) SetHeartbeatReporter(reporter heartbeat.Reporter) {
	c.reporter = reporter
}
