package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dwizi/agent-runtime/internal/agent/tools"
)

type ModerationTriageTool struct{}

func NewModerationTriageTool() *ModerationTriageTool {
	return &ModerationTriageTool{}
}

func (t *ModerationTriageTool) Name() string { return "moderation_triage" }
func (t *ModerationTriageTool) ToolClass() tools.ToolClass {
	return tools.ToolClassModeration
}
func (t *ModerationTriageTool) RequiresApproval() bool { return false }

func (t *ModerationTriageTool) Description() string {
	return "Classify moderation risk and suggest safe next steps for community messages."
}

func (t *ModerationTriageTool) ParametersSchema() string {
	return `{"message":"string","reporter_user_id":"string(optional)","channel":"string(optional)"}`
}

func (t *ModerationTriageTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		Message        string `json:"message"`
		ReporterUserID string `json:"reporter_user_id"`
		Channel        string `json:"channel"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return err
	}
	if strings.TrimSpace(args.Message) == "" {
		return fmt.Errorf("message is required")
	}
	if len(strings.TrimSpace(args.Message)) > 5000 {
		return fmt.Errorf("message is too long")
	}
	return nil
}

func (t *ModerationTriageTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Message        string `json:"message"`
		ReporterUserID string `json:"reporter_user_id"`
		Channel        string `json:"channel"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	text := strings.ToLower(strings.TrimSpace(args.Message))
	labels := []string{}
	severity := "low"
	action := "monitor and keep conversation civil."

	if containsAnyKeyword(text, "kill", "dox", "swat", "suicide", "self-harm", "bomb", "shoot") {
		severity = "critical"
		labels = append(labels, "threat")
		action = "escalate to moderators immediately, preserve evidence, and consider emergency protocol."
	} else if containsAnyKeyword(text, "hate", "slur", "harass", "stalk", "abuse") {
		severity = "high"
		labels = append(labels, "harassment")
		action = "escalate quickly, remove harmful content, and warn or mute according to policy."
	} else if containsAnyKeyword(text, "spam", "airdrops", "dm me", "crypto signal", "free nitro") {
		severity = "medium"
		labels = append(labels, "spam")
		action = "remove spam, apply anti-spam controls, and monitor repeat behavior."
	} else {
		labels = append(labels, "general")
	}

	lines := []string{
		fmt.Sprintf("Severity: %s", severity),
		fmt.Sprintf("Labels: %s", strings.Join(labels, ", ")),
		fmt.Sprintf("Suggested action: %s", action),
	}
	if userID := strings.TrimSpace(args.ReporterUserID); userID != "" {
		lines = append(lines, fmt.Sprintf("Reporter: %s", userID))
	}
	if channel := strings.TrimSpace(args.Channel); channel != "" {
		lines = append(lines, fmt.Sprintf("Channel: %s", channel))
	}
	return strings.Join(lines, "\n"), nil
}

type DraftEscalationTool struct{}

func NewDraftEscalationTool() *DraftEscalationTool {
	return &DraftEscalationTool{}
}

func (t *DraftEscalationTool) Name() string { return "draft_escalation" }
func (t *DraftEscalationTool) ToolClass() tools.ToolClass {
	return tools.ToolClassDrafting
}
func (t *DraftEscalationTool) RequiresApproval() bool { return false }

func (t *DraftEscalationTool) Description() string {
	return "Draft an escalation note for moderators/admins from incident details."
}

func (t *DraftEscalationTool) ParametersSchema() string {
	return `{"topic":"string","summary":"string","urgency":"low|medium|high|critical","evidence":["string"],"next_step":"string(optional)"}`
}

func (t *DraftEscalationTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		Topic    string   `json:"topic"`
		Summary  string   `json:"summary"`
		Urgency  string   `json:"urgency"`
		Evidence []string `json:"evidence"`
		NextStep string   `json:"next_step"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return err
	}
	if strings.TrimSpace(args.Topic) == "" {
		return fmt.Errorf("topic is required")
	}
	if strings.TrimSpace(args.Summary) == "" {
		return fmt.Errorf("summary is required")
	}
	switch strings.ToLower(strings.TrimSpace(args.Urgency)) {
	case "low", "medium", "high", "critical":
	default:
		return fmt.Errorf("urgency must be low, medium, high, or critical")
	}
	return nil
}

func (t *DraftEscalationTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Topic    string   `json:"topic"`
		Summary  string   `json:"summary"`
		Urgency  string   `json:"urgency"`
		Evidence []string `json:"evidence"`
		NextStep string   `json:"next_step"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	urgency := strings.ToUpper(strings.TrimSpace(args.Urgency))
	lines := []string{
		fmt.Sprintf("Escalation: %s", strings.TrimSpace(args.Topic)),
		fmt.Sprintf("Urgency: %s", urgency),
		fmt.Sprintf("Summary: %s", strings.TrimSpace(args.Summary)),
	}
	if len(args.Evidence) > 0 {
		lines = append(lines, "Evidence:")
		for _, item := range args.Evidence {
			clean := strings.TrimSpace(item)
			if clean == "" {
				continue
			}
			lines = append(lines, "- "+clean)
		}
	}
	nextStep := strings.TrimSpace(args.NextStep)
	if nextStep == "" {
		nextStep = "Assign an on-call moderator and post an update in the admin channel."
	}
	lines = append(lines, "Recommended next step: "+nextStep)
	return strings.Join(lines, "\n"), nil
}

type DraftFAQAnswerTool struct{}

func NewDraftFAQAnswerTool() *DraftFAQAnswerTool {
	return &DraftFAQAnswerTool{}
}

func (t *DraftFAQAnswerTool) Name() string { return "draft_faq_answer" }
func (t *DraftFAQAnswerTool) ToolClass() tools.ToolClass {
	return tools.ToolClassDrafting
}
func (t *DraftFAQAnswerTool) RequiresApproval() bool { return false }

func (t *DraftFAQAnswerTool) Description() string {
	return "Draft a concise FAQ-style community answer from key points."
}

func (t *DraftFAQAnswerTool) ParametersSchema() string {
	return `{"question":"string","key_points":["string"],"tone":"neutral|friendly|strict(optional)","include_follow_up":"boolean(optional)"}`
}

func (t *DraftFAQAnswerTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		Question        string   `json:"question"`
		KeyPoints       []string `json:"key_points"`
		Tone            string   `json:"tone"`
		IncludeFollowUp bool     `json:"include_follow_up"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return err
	}
	if strings.TrimSpace(args.Question) == "" {
		return fmt.Errorf("question is required")
	}
	if len(args.KeyPoints) == 0 {
		return fmt.Errorf("key_points must contain at least one item")
	}
	if tone := strings.ToLower(strings.TrimSpace(args.Tone)); tone != "" {
		switch tone {
		case "neutral", "friendly", "strict":
		default:
			return fmt.Errorf("tone must be neutral, friendly, or strict")
		}
	}
	return nil
}

func (t *DraftFAQAnswerTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Question        string   `json:"question"`
		KeyPoints       []string `json:"key_points"`
		Tone            string   `json:"tone"`
		IncludeFollowUp bool     `json:"include_follow_up"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	tonePrefix := ""
	switch strings.ToLower(strings.TrimSpace(args.Tone)) {
	case "friendly":
		tonePrefix = "Thanks for asking. "
	case "strict":
		tonePrefix = "Please follow the policy: "
	default:
		tonePrefix = ""
	}

	points := make([]string, 0, len(args.KeyPoints))
	for _, item := range args.KeyPoints {
		clean := strings.TrimSpace(item)
		if clean == "" {
			continue
		}
		points = append(points, clean)
	}
	answer := tonePrefix + strings.Join(points, " ")
	if args.IncludeFollowUp {
		answer += " If you need more detail, share your exact case and I can help further."
	}
	return strings.TrimSpace(answer), nil
}
