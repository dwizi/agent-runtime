package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/dwizi/agent-runtime/internal/agent"
	"github.com/dwizi/agent-runtime/internal/agenterr"
	"github.com/dwizi/agent-runtime/internal/store"
)

func readToolContext(ctx context.Context) (store.ContextRecord, MessageInput, error) {
	record, ok := ctx.Value(ContextKeyRecord).(store.ContextRecord)
	if !ok {
		return store.ContextRecord{}, MessageInput{}, fmt.Errorf("internal error: context record missing from context")
	}
	input, ok := ctx.Value(ContextKeyInput).(MessageInput)
	if !ok {
		return store.ContextRecord{}, MessageInput{}, fmt.Errorf("internal error: message input missing from context")
	}
	return record, input, nil
}

func strictDecodeArgs(raw json.RawMessage, target any) error {
	payload := bytes.TrimSpace(raw)
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("unexpected trailing json")
	}
	return nil
}

func containsAnyKeyword(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

func checkAutoApproval(ctx context.Context, store Store) error {
	_ = store
	input, ok := ctx.Value(ContextKeyInput).(MessageInput)
	if !ok {
		return fmt.Errorf("%w: input context missing", agenterr.ErrAccessDenied)
	}
	if input.FromUserID == "system:task-worker" {
		return nil
	}
	if agent.HasSensitiveToolApproval(ctx) {
		return nil
	}
	return fmt.Errorf("%w: %w", agenterr.ErrApprovalRequired, agenterr.ErrAdminRole)
}

func looksLikePlaceholderValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	normalized := strings.ToUpper(strings.Trim(trimmed, `"'`))

	// Generic template markers such as <path>, <file>, ${VAR}, etc.
	if (strings.Contains(normalized, "<") && strings.Contains(normalized, ">")) ||
		(strings.Contains(normalized, "${") && strings.Contains(normalized, "}")) {
		return true
	}
	for _, keyword := range []string{"PLACEHOLDER", "REPLACE_ME", "TO_BE_FILLED", "FILL_ME", "EXAMPLE_VALUE"} {
		if strings.Contains(normalized, keyword) {
			return true
		}
	}

	// All-caps symbolic tokens like FILE_PATH, TARGET_URL, INPUT_FILE, etc.
	if isLikelySymbolicToken(normalized) && containsAnyKeyword(normalized,
		"PATH", "FILE", "URL", "URI", "HOST", "ENDPOINT", "TARGET", "INPUT", "OUTPUT", "DIR", "FOLDER", "ROUTE", "RUTA",
	) {
		return true
	}
	return false
}

func isLikelySymbolicToken(value string) bool {
	if len(value) < 5 {
		return false
	}
	if strings.ContainsAny(value, "/\\.:") {
		return false
	}
	hasLetter := false
	hasUnderscore := false
	for _, ch := range value {
		switch {
		case ch >= 'A' && ch <= 'Z':
			hasLetter = true
		case ch >= '0' && ch <= '9':
		case ch == '_':
			hasUnderscore = true
		default:
			return false
		}
	}
	return hasLetter && hasUnderscore
}

func runActionPayloadString(payload map[string]any, key string) string {
	value, ok := runActionPayloadValue(payload, key)
	if !ok || value == nil {
		return ""
	}
	switch casted := value.(type) {
	case string:
		return strings.TrimSpace(casted)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", value))
	}
}

func runActionPayloadValue(payload map[string]any, key string) (any, bool) {
	if payload == nil {
		return nil, false
	}
	if value, ok := payload[key]; ok {
		return value, true
	}
	nestedRaw, ok := payload["payload"]
	if !ok || nestedRaw == nil {
		return nil, false
	}
	nested, ok := nestedRaw.(map[string]any)
	if !ok {
		return nil, false
	}
	value, ok := nested[key]
	return value, ok
}

func runActionParseArgs(value any) ([]string, error) {
	switch casted := value.(type) {
	case []string:
		return casted, nil
	case []any:
		args := make([]string, 0, len(casted))
		for _, raw := range casted {
			args = append(args, strings.TrimSpace(fmt.Sprintf("%v", raw)))
		}
		return args, nil
	case string:
		trimmed := strings.TrimSpace(casted)
		if trimmed == "" {
			return nil, nil
		}
		return strings.Fields(trimmed), nil
	default:
		return nil, fmt.Errorf("%w: unsupported args payload", agenterr.ErrToolInvalidArgs)
	}
}

func validateRunCommandPreflight(target string, payload map[string]any) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("%w: target is required for run_command", agenterr.ErrToolInvalidArgs)
	}
	if looksLikePlaceholderValue(target) {
		return fmt.Errorf("%w: target contains a placeholder value; use a concrete command", agenterr.ErrToolPreflight)
	}
	if strings.Contains(target, "/") || strings.Contains(target, "\\") || strings.ContainsAny(target, " \t\r\n") {
		return fmt.Errorf("%w: target must be a bare executable name", agenterr.ErrToolPreflight)
	}

	if command := strings.TrimSpace(runActionPayloadString(payload, "command")); command != "" {
		if looksLikePlaceholderValue(command) {
			return fmt.Errorf("%w: payload.command contains a placeholder value; use a concrete command", agenterr.ErrToolPreflight)
		}
		commandParts := strings.Fields(command)
		if len(commandParts) > 0 {
			commandExec := strings.TrimSpace(commandParts[0])
			if strings.Contains(commandExec, "/") || strings.Contains(commandExec, "\\") {
				return fmt.Errorf("%w: payload.command must use a bare executable name", agenterr.ErrToolPreflight)
			}
			if !strings.EqualFold(commandExec, target) {
				return fmt.Errorf("%w: payload.command executable must match target", agenterr.ErrToolPreflight)
			}
		}
	}

	if rawArgs, ok := runActionPayloadValue(payload, "args"); ok {
		parsedArgs, err := runActionParseArgs(rawArgs)
		if err != nil {
			return fmt.Errorf("%w: payload.args is invalid: %w", agenterr.ErrToolInvalidArgs, err)
		}
		if len(parsedArgs) > 32 {
			return fmt.Errorf("%w: too many command args", agenterr.ErrToolPreflight)
		}
		for _, value := range parsedArgs {
			if looksLikePlaceholderValue(value) {
				return fmt.Errorf("%w: payload.args contains placeholder value %q; use concrete args", agenterr.ErrToolPreflight, value)
			}
			if len(value) > 512 {
				return fmt.Errorf("%w: command arg exceeds size limit", agenterr.ErrToolPreflight)
			}
		}
	}

	if cwd := strings.TrimSpace(runActionPayloadString(payload, "cwd")); cwd != "" {
		if looksLikePlaceholderValue(cwd) {
			return fmt.Errorf("%w: payload.cwd contains placeholder value; use a concrete path", agenterr.ErrToolPreflight)
		}
	}
	return nil
}
