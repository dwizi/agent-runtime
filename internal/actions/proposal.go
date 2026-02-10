package actions

import (
	"encoding/json"
	"strings"
)

type Proposal struct {
	Type    string                 `json:"type"`
	Target  string                 `json:"target,omitempty"`
	Summary string                 `json:"summary,omitempty"`
	Payload map[string]any         `json:"payload,omitempty"`
	Raw     map[string]interface{} `json:"raw,omitempty"`
}

func ExtractProposal(input string) (cleanText string, proposal *Proposal) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", nil
	}

	jsonText, blockStart, blockEnd, ok := extractActionJSON(trimmed)
	if !ok {
		return trimmed, nil
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(jsonText), &decoded); err != nil {
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

	clean := strings.TrimSpace(trimmed[:blockStart] + trimmed[blockEnd:])
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

func extractActionJSON(input string) (jsonText string, blockStart int, blockEnd int, ok bool) {
	if jsonText, blockStart, blockEnd, ok = extractFencedActionJSON(input); ok {
		return jsonText, blockStart, blockEnd, true
	}
	return extractInlineActionJSON(input)
}

func extractFencedActionJSON(input string) (jsonText string, blockStart int, blockEnd int, ok bool) {
	lower := strings.ToLower(input)
	const marker = "```action"
	search := 0
	for {
		offset := strings.Index(lower[search:], marker)
		if offset < 0 {
			return "", 0, 0, false
		}
		start := search + offset
		cursor := skipASCIIWhitespace(input, start+len(marker))
		if cursor >= len(input) || input[cursor] != '{' {
			search = start + len(marker)
			continue
		}
		end, parsed := parseJSONObject(input, cursor)
		if !parsed {
			search = start + len(marker)
			continue
		}
		closing := skipASCIIWhitespace(input, end)
		if !strings.HasPrefix(input[closing:], "```") {
			search = start + len(marker)
			continue
		}
		return input[cursor:end], start, closing + 3, true
	}
}

func extractInlineActionJSON(input string) (jsonText string, blockStart int, blockEnd int, ok bool) {
	lower := strings.ToLower(input)
	const marker = "action"
	search := 0
	for {
		offset := strings.Index(lower[search:], marker)
		if offset < 0 {
			return "", 0, 0, false
		}
		start := search + offset
		if start > 0 && isASCIIWord(input[start-1]) {
			search = start + len(marker)
			continue
		}
		cursor := skipASCIIWhitespace(input, start+len(marker))
		if cursor < len(input) && input[cursor] == ':' {
			cursor = skipASCIIWhitespace(input, cursor+1)
		}
		if cursor >= len(input) || input[cursor] != '{' {
			search = start + len(marker)
			continue
		}
		end, parsed := parseJSONObject(input, cursor)
		if !parsed {
			search = start + len(marker)
			continue
		}
		return input[cursor:end], start, end, true
	}
}

func parseJSONObject(input string, start int) (int, bool) {
	if start < 0 || start >= len(input) || input[start] != '{' {
		return 0, false
	}
	depth := 0
	inString := false
	escaped := false
	for index := start; index < len(input); index++ {
		ch := input[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return index + 1, true
			}
		}
	}
	return 0, false
}

func skipASCIIWhitespace(input string, start int) int {
	index := start
	for index < len(input) {
		switch input[index] {
		case ' ', '\t', '\n', '\r':
			index++
		default:
			return index
		}
	}
	return index
}

func isASCIIWord(value byte) bool {
	return value == '_' ||
		(value >= 'a' && value <= 'z') ||
		(value >= 'A' && value <= 'Z') ||
		(value >= '0' && value <= '9')
}
