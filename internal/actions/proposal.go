package actions

import (
	"encoding/json"
	"regexp"
	"strings"
)

type Proposal struct {
	Type    string                 `json:"type"`
	Target  string                 `json:"target,omitempty"`
	Summary string                 `json:"summary,omitempty"`
	Payload map[string]any         `json:"payload,omitempty"`
	Raw     map[string]interface{} `json:"raw,omitempty"`
}

var actionFencePattern = regexp.MustCompile("(?s)```action\\s*(\\{.*?\\})\\s*```")

func ExtractProposal(input string) (cleanText string, proposal *Proposal) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", nil
	}

	matches := actionFencePattern.FindStringSubmatch(trimmed)
	if len(matches) < 2 {
		return trimmed, nil
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(matches[1]), &decoded); err != nil {
		return trimmed, nil
	}
	actionType := strings.TrimSpace(toString(decoded["type"]))
	if actionType == "" {
		return trimmed, nil
	}
	result := &Proposal{
		Type:    actionType,
		Target:  strings.TrimSpace(toString(decoded["target"])),
		Summary: strings.TrimSpace(toString(decoded["summary"])),
		Payload: map[string]any{},
		Raw:     map[string]interface{}{},
	}
	for key, value := range decoded {
		if key == "type" || key == "target" || key == "summary" {
			continue
		}
		result.Payload[key] = value
	}
	for key, value := range decoded {
		result.Raw[key] = value
	}

	clean := strings.TrimSpace(strings.Replace(trimmed, matches[0], "", 1))
	return clean, result
}

func toString(value any) string {
	if value == nil {
		return ""
	}
	switch casted := value.(type) {
	case string:
		return casted
	default:
		return ""
	}
}
