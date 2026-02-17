package telegram

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

const pairingMessage = "pair"

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
	token       string
	apiBase     string
	workspace   string
	pollSeconds int
	commandSync bool
	pairings    PairingStore
	gateway     CommandGateway
	responder   Responder
	policy      SafetyPolicy
	httpClient  *http.Client
	logger      *slog.Logger
	botUsername string
	offset      int64
	reporter    heartbeat.Reporter
}

type Option func(*Connector)

func WithCommandSync(enabled bool) Option {
	return func(connector *Connector) {
		connector.commandSync = enabled
	}
}

func New(token, apiBase, workspaceRoot string, pollSeconds int, pairings PairingStore, commandGateway CommandGateway, responder Responder, policy SafetyPolicy, logger *slog.Logger, opts ...Option) *Connector {
	if strings.TrimSpace(apiBase) == "" {
		apiBase = "https://api.telegram.org"
	}
	if pollSeconds < 1 {
		pollSeconds = 25
	}
	connector := &Connector{
		token:       strings.TrimSpace(token),
		apiBase:     strings.TrimRight(strings.TrimSpace(apiBase), "/"),
		workspace:   strings.TrimSpace(workspaceRoot),
		pollSeconds: pollSeconds,
		commandSync: true,
		pairings:    pairings,
		gateway:     commandGateway,
		responder:   responder,
		policy:      policy,
		httpClient: &http.Client{
			Timeout: time.Duration(pollSeconds+10) * time.Second,
		},
		logger: logger,
		offset: 0,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(connector)
		}
	}
	return connector
}

func (c *Connector) Name() string {
	return "telegram"
}

func (c *Connector) SetHeartbeatReporter(reporter heartbeat.Reporter) {
	c.reporter = reporter
}
