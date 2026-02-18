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
var _ tools.Tool = (*MCPDynamicTool)(nil)
var _ tools.MetadataProvider = (*MCPDynamicTool)(nil)
var _ tools.ArgumentValidator = (*MCPDynamicTool)(nil)
var _ tools.Tool = (*MCPListServersTool)(nil)
var _ tools.MetadataProvider = (*MCPListServersTool)(nil)
var _ tools.ArgumentValidator = (*MCPListServersTool)(nil)
var _ tools.Tool = (*MCPListResourcesTool)(nil)
var _ tools.MetadataProvider = (*MCPListResourcesTool)(nil)
var _ tools.ArgumentValidator = (*MCPListResourcesTool)(nil)
var _ tools.Tool = (*MCPReadResourceTool)(nil)
var _ tools.MetadataProvider = (*MCPReadResourceTool)(nil)
var _ tools.ArgumentValidator = (*MCPReadResourceTool)(nil)
var _ tools.Tool = (*MCPListResourceTemplatesTool)(nil)
var _ tools.MetadataProvider = (*MCPListResourceTemplatesTool)(nil)
var _ tools.ArgumentValidator = (*MCPListResourceTemplatesTool)(nil)
var _ tools.Tool = (*MCPListPromptsTool)(nil)
var _ tools.MetadataProvider = (*MCPListPromptsTool)(nil)
var _ tools.ArgumentValidator = (*MCPListPromptsTool)(nil)
var _ tools.Tool = (*MCPGetPromptTool)(nil)
var _ tools.MetadataProvider = (*MCPGetPromptTool)(nil)
var _ tools.ArgumentValidator = (*MCPGetPromptTool)(nil)

type contextKey string

const (
	ContextKeyRecord         contextKey = "context_record"
	ContextKeyInput          contextKey = "message_input"
	defaultObjectiveCronExpr            = "0 */6 * * *"
)
