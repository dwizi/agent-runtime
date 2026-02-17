package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dwizi/agent-runtime/internal/store"
)

// LearnSkillTool implements tools.Tool for persisting knowledge.
type LearnSkillTool struct {
	workspaceRoot string
}

func NewLearnSkillTool(workspaceRoot string) *LearnSkillTool {
	return &LearnSkillTool{workspaceRoot: workspaceRoot}
}

func (t *LearnSkillTool) Name() string { return "learn_skill" }

func (t *LearnSkillTool) Description() string {
	return "Save a new fact, behavior, or operational procedure to your long-term knowledge base (skills)."
}

func (t *LearnSkillTool) ParametersSchema() string {
	return `{"name": "string (snake_case)", "content": "string (markdown)"}`
}

func (t *LearnSkillTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	record, ok := ctx.Value(ContextKeyRecord).(store.ContextRecord)
	if !ok {
		return "", fmt.Errorf("internal error: context record missing from context")
	}

	// We'll put it in context/skills/common for now, or a workspace-specific one
	skillDir := filepath.Join(t.workspaceRoot, record.WorkspaceID, "context", "skills", "common")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return "", err
	}

	path := filepath.Join(skillDir, args.Name+".md")
	if err := os.WriteFile(path, []byte(args.Content), 0o644); err != nil {
		return "", err
	}

	return fmt.Sprintf("I've learned a new skill: %s", args.Name), nil
}
