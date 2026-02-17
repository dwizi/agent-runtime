package gateway

import "github.com/dwizi/agent-runtime/internal/agent/tools"

// Ensure implementation
var _ tools.Tool = (*SearchTool)(nil)
var _ tools.Tool = (*OpenKnowledgeDocumentTool)(nil)
var _ tools.Tool = (*CreateTaskTool)(nil)
var _ tools.Tool = (*ModerationTriageTool)(nil)
var _ tools.Tool = (*DraftEscalationTool)(nil)
var _ tools.Tool = (*DraftFAQAnswerTool)(nil)
var _ tools.Tool = (*CreateObjectiveTool)(nil)
var _ tools.Tool = (*UpdateObjectiveTool)(nil)
var _ tools.Tool = (*UpdateTaskTool)(nil)
var _ tools.MetadataProvider = (*SearchTool)(nil)
var _ tools.MetadataProvider = (*OpenKnowledgeDocumentTool)(nil)
var _ tools.MetadataProvider = (*CreateTaskTool)(nil)
var _ tools.MetadataProvider = (*ModerationTriageTool)(nil)
var _ tools.MetadataProvider = (*DraftEscalationTool)(nil)
var _ tools.MetadataProvider = (*DraftFAQAnswerTool)(nil)
var _ tools.MetadataProvider = (*CreateObjectiveTool)(nil)
var _ tools.MetadataProvider = (*UpdateObjectiveTool)(nil)
var _ tools.MetadataProvider = (*UpdateTaskTool)(nil)

var _ tools.ArgumentValidator = (*SearchTool)(nil)
var _ tools.ArgumentValidator = (*OpenKnowledgeDocumentTool)(nil)
var _ tools.ArgumentValidator = (*CreateTaskTool)(nil)
var _ tools.ArgumentValidator = (*ModerationTriageTool)(nil)
var _ tools.ArgumentValidator = (*DraftEscalationTool)(nil)
var _ tools.ArgumentValidator = (*DraftFAQAnswerTool)(nil)
var _ tools.ArgumentValidator = (*CreateObjectiveTool)(nil)
var _ tools.ArgumentValidator = (*UpdateObjectiveTool)(nil)
var _ tools.ArgumentValidator = (*UpdateTaskTool)(nil)
var _ tools.ArgumentValidator = (*RunActionTool)(nil)

type contextKey string

const (
	ContextKeyRecord         contextKey = "context_record"
	ContextKeyInput          contextKey = "message_input"
	defaultObjectiveCronExpr            = "0 */6 * * *"
)
