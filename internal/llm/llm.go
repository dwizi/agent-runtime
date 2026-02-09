package llm

import (
	"context"
	"errors"
)

var ErrUnavailable = errors.New("llm unavailable")

type MessageInput struct {
	Connector    string
	WorkspaceID  string
	ContextID    string
	ExternalID   string
	DisplayName  string
	FromUserID   string
	Text         string
	SystemPrompt string
	IsDM         bool
}

type Responder interface {
	Reply(ctx context.Context, input MessageInput) (string, error)
}
