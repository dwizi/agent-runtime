package app

import (
	"strings"
	"time"

	"github.com/carlos/spinner/internal/memorylog"
)

func appendOutboundChatLog(workspaceRoot, workspaceID, connector, externalID, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	_ = memorylog.Append(memorylog.Entry{
		WorkspaceRoot: strings.TrimSpace(workspaceRoot),
		WorkspaceID:   strings.TrimSpace(workspaceID),
		Connector:     strings.ToLower(strings.TrimSpace(connector)),
		ExternalID:    strings.TrimSpace(externalID),
		Direction:     "outbound",
		ActorID:       "spinner",
		DisplayName:   strings.TrimSpace(externalID),
		Text:          strings.TrimSpace(text),
		Timestamp:     time.Now().UTC(),
	})
}
