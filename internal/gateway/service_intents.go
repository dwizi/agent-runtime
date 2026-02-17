package gateway

import (
	"context"
	"fmt"
	"strings"

	"github.com/dwizi/agent-runtime/internal/store"
)

func splitCommand(text string) (string, string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", ""
	}
	if strings.HasPrefix(trimmed, "/") {
		trimmed = strings.TrimPrefix(trimmed, "/")
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return "", ""
	}
	command := strings.ToLower(fields[0])
	if idx := strings.Index(command, "@"); idx >= 0 {
		command = command[:idx]
	}
	command = NormalizeCommandName(command)

	if len(fields) == 1 {
		return command, ""
	}
	argStart := strings.Index(trimmed, " ")
	if argStart < 0 {
		return command, ""
	}
	return command, strings.TrimSpace(trimmed[argStart+1:])
}

func parseIntentTask(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	phrases := []string{
		"task ",
		"create task ",
		"create a task ",
		"create a task to ",
		"please create a task ",
		"please create a task to ",
		"add task ",
		"add a task ",
		"queue task ",
		"queue a task ",
	}
	for _, phrase := range phrases {
		if !strings.HasPrefix(lower, phrase) {
			continue
		}
		value := strings.TrimSpace(trimmed[len(phrase):])
		if value == "" {
			return "", false
		}
		return value, true
	}
	return "", false
}

func parseApproveCommandAsActionArg(arg string) (string, bool) {
	trimmed := strings.TrimSpace(arg)
	if trimmed == "" {
		return latestPendingActionAlias, true
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "most recent") || strings.Contains(lower, "latest pending") || lower == "latest" || lower == "newest" {
		return mostRecentPendingActionAlias, true
	}
	if lower == "all" || lower == "everything" {
		return allPendingActionsAlias, true
	}
	if actionID, ok := findActionID(trimmed); ok {
		return actionID, true
	}
	if lower == "it" || lower == "this" || lower == "that" || lower == "action" {
		return latestPendingActionAlias, true
	}
	if strings.Contains(lower, "approve action") || strings.Contains(lower, "the action") || strings.Contains(lower, "approved action") {
		return latestPendingActionAlias, true
	}
	return "", false
}

func parseDenyCommandAsActionArg(arg string) (string, bool) {
	trimmed := strings.TrimSpace(arg)
	if trimmed == "" {
		return latestPendingActionAlias, true
	}
	lower := strings.ToLower(trimmed)
	if lower == "all" || lower == "everything" {
		return allPendingActionsAlias, true
	}
	if actionID, _, end, ok := findActionIDWithBounds(trimmed); ok {
		reason := strings.TrimSpace(trimmed[end:])
		reason = normalizeDenyReason(reason)
		if reason == "" {
			return actionID, true
		}
		return actionID + " " + reason, true
	}
	if lower == "it" || lower == "this" || lower == "that" || strings.HasPrefix(lower, "it ") ||
		strings.HasPrefix(lower, "this ") || strings.HasPrefix(lower, "that ") || strings.Contains(lower, "action") {
		reason := trimmed
		reasonLower := lower
		for _, marker := range []string{"it ", "this ", "that ", "because ", "reason ", "for "} {
			index := strings.Index(reasonLower, marker)
			if index < 0 {
				continue
			}
			value := strings.TrimSpace(reason[index+len(marker):])
			value = normalizeDenyReason(value)
			if value == "" {
				continue
			}
			return latestPendingActionAlias + " " + value, true
		}
		return latestPendingActionAlias, true
	}
	return "", false
}

func parseNaturalLanguageCommand(text string) (command, arg string, ok bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", "", false
	}
	lower := strings.ToLower(trimmed)

	if actionArg, found := parseIntentApproveMostRecentPendingAction(trimmed, lower); found {
		return "approve-action", actionArg, true
	}
	if actionID, found := parseIntentApproveAction(trimmed); found {
		return "approve-action", actionID, true
	}
	if actionArg, found := parseIntentDenyAction(trimmed); found {
		return "deny-action", actionArg, true
	}
	if isImplicitApproveActionIntent(lower) {
		return "approve-action", latestPendingActionAlias, true
	}
	if denyReason, found := parseImplicitDenyActionReason(trimmed, lower); found {
		if denyReason == "" {
			return "deny-action", latestPendingActionAlias, true
		}
		return "deny-action", latestPendingActionAlias + " " + denyReason, true
	}
	if token, found := parseIntentApprovePairing(trimmed); found {
		return "approve", token, true
	}
	if denyArg, found := parseIntentDenyPairing(trimmed); found {
		return "deny", denyArg, true
	}
	if isPendingActionsIntent(lower) {
		return "pending-actions", "", true
	}
	if strings.Contains(lower, "admin channel") && strings.Contains(lower, "enable") {
		return "admin-channel", "enable", true
	}
	if promptArg, found := parsePromptIntent(trimmed, lower); found {
		return "prompt", promptArg, true
	}
	if query, found := parseSearchIntent(trimmed, lower); found {
		return "search", query, true
	}
	if target, found := parseOpenIntent(trimmed, lower); found {
		return "open", target, true
	}
	if isStatusIntent(lower) {
		return "status", "", true
	}
	if goal, found := parseMonitorIntent(trimmed, lower); found {
		return "monitor", goal, true
	}
	if taskPrompt, found := parseTaskCreationIntent(trimmed, lower); found {
		return "task", taskPrompt, true
	}
	if prompt, found := parseIntentTask(trimmed); found {
		return "task", prompt, true
	}
	return "", "", false
}

func parseIntentApproveMostRecentPendingAction(trimmed, lower string) (string, bool) {
	if !strings.Contains(lower, "approve") {
		return "", false
	}
	if strings.Contains(lower, "pair") || strings.Contains(lower, "token") {
		return "", false
	}
	if !(strings.Contains(lower, "most recent") ||
		strings.Contains(lower, "latest") ||
		strings.Contains(lower, "newest") ||
		strings.Contains(lower, "last pending")) {
		return "", false
	}
	if !(strings.Contains(lower, "pending action") || strings.Contains(lower, "pending approval")) {
		return "", false
	}
	_ = trimmed
	return mostRecentPendingActionAlias, true
}

func isImplicitApproveActionIntent(lower string) bool {
	if lower == "approve" || lower == "yes" {
		return true
	}
	if !(strings.Contains(lower, "approve") || strings.Contains(lower, "approved")) {
		return false
	}
	if strings.Contains(lower, "pair") || strings.Contains(lower, "token") {
		return false
	}
	if strings.Contains(lower, "approve action") || strings.Contains(lower, "approve the action") {
		return true
	}
	return strings.Contains(lower, "approve it") ||
		strings.Contains(lower, "approve this") ||
		strings.Contains(lower, "approve that") ||
		strings.Contains(lower, "yes i approve")
}

func parseImplicitDenyActionReason(trimmed, lower string) (string, bool) {
	hasDeny := strings.Contains(lower, "deny") || strings.Contains(lower, "reject") || strings.Contains(lower, "decline")
	if !hasDeny {
		return "", false
	}
	if strings.Contains(lower, "pair") || strings.Contains(lower, "token") {
		return "", false
	}
	if !(strings.Contains(lower, "deny action") ||
		strings.Contains(lower, "reject action") ||
		strings.Contains(lower, "deny it") ||
		strings.Contains(lower, "reject it") ||
		strings.Contains(lower, "decline it") ||
		strings.Contains(lower, "deny this") ||
		strings.Contains(lower, "reject this") ||
		strings.Contains(lower, "decline this")) {
		return "", false
	}
	reason := trimmed
	reasonLower := lower
	for _, marker := range []string{"because ", "reason ", "for "} {
		index := strings.Index(reasonLower, marker)
		if index < 0 {
			continue
		}
		value := strings.TrimSpace(reason[index+len(marker):])
		value = normalizeDenyReason(value)
		return value, true
	}
	return "", true
}

func isPendingActionsIntent(lower string) bool {
	return strings.Contains(lower, "pending action") || strings.Contains(lower, "pending approval")
}

func parsePromptIntent(trimmed, lower string) (string, bool) {
	if strings.Contains(lower, "show prompt") || strings.Contains(lower, "prompt show") {
		return "show", true
	}
	if strings.Contains(lower, "clear prompt") || strings.Contains(lower, "prompt clear") {
		return "clear", true
	}
	for _, phrase := range []string{"set prompt", "update prompt"} {
		index := strings.Index(lower, phrase)
		if index < 0 {
			continue
		}
		value := strings.TrimSpace(trimmed[index+len(phrase):])
		if strings.HasPrefix(strings.ToLower(value), "to ") {
			value = strings.TrimSpace(value[len("to "):])
		}
		if value == "" {
			return "", false
		}
		return "set " + value, true
	}
	return "", false
}

func parseSearchIntent(trimmed, lower string) (string, bool) {
	for _, phrase := range []string{"search for ", "search docs for ", "find in docs ", "find docs for "} {
		if strings.HasPrefix(lower, phrase) {
			value := strings.TrimSpace(trimmed[len(phrase):])
			if value == "" {
				return "", false
			}
			return value, true
		}
	}
	if strings.HasPrefix(lower, "search ") {
		value := strings.TrimSpace(trimmed[len("search "):])
		if value == "" || value == "status" {
			return "", false
		}
		return value, true
	}
	return "", false
}

func parseOpenIntent(trimmed, lower string) (string, bool) {
	for _, phrase := range []string{"open file ", "open doc ", "open markdown ", "show file "} {
		if strings.HasPrefix(lower, phrase) {
			target := sanitizeOpenTarget(trimmed[len(phrase):])
			if target == "" {
				return "", false
			}
			return target, true
		}
	}
	if strings.HasPrefix(lower, "open ") {
		target := sanitizeOpenTarget(trimmed[len("open "):])
		if target == "" {
			return "", false
		}
		return target, true
	}
	return "", false
}

func sanitizeOpenTarget(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.Trim(trimmed, "`\"'")
	trimmed = strings.Trim(trimmed, " .,:;!?")
	return trimmed
}

func isStatusIntent(lower string) bool {
	if lower == "status" {
		return true
	}
	return strings.Contains(lower, "qmd status") ||
		strings.Contains(lower, "index status") ||
		strings.Contains(lower, "search index status")
}

func parseMonitorIntent(trimmed, lower string) (string, bool) {
	prefixes := []string{
		"monitor ",
		"track ",
		"keep monitoring ",
		"set an alert for ",
		"set an alert to monitor ",
		"create a monitoring objective for ",
		"create monitoring objective for ",
		"create a monitor objective for ",
		"set up a monitoring objective for ",
		"setup a monitoring objective for ",
		"create an objective to monitor ",
		"create a monitoring objective to monitor ",
		"set up monitoring for ",
		"setup monitoring for ",
	}
	for _, prefix := range prefixes {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		value := cleanMonitorGoal(trimmed[len(prefix):])
		if value == "" {
			return "", false
		}
		return value, true
	}
	for _, phrase := range []string{
		"set an alert and monitor ",
		"create an alert and monitor ",
	} {
		index := strings.Index(lower, phrase)
		if index < 0 {
			continue
		}
		value := cleanMonitorGoal(trimmed[index+len(phrase):])
		if value != "" {
			return value, true
		}
	}
	if strings.Contains(lower, "monitoring objective") || strings.Contains(lower, "monitor objective") {
		for _, marker := range []string{" for ", " to monitor "} {
			index := strings.Index(lower, marker)
			if index < 0 {
				continue
			}
			value := cleanMonitorGoal(trimmed[index+len(marker):])
			if value != "" {
				return value, true
			}
		}
	}
	return "", false
}

func parseTaskCreationIntent(trimmed, lower string) (string, bool) {
	prefixes := []string{
		"turn that into an actionable task",
		"turn this into an actionable task",
		"turn that into a task",
		"turn this into a task",
		"create one actionable task",
		"please create one actionable task",
		"create an actionable task",
		"make this a task",
		"create a task from this",
	}
	for _, prefix := range prefixes {
		index := strings.Index(lower, prefix)
		if index < 0 {
			continue
		}
		after := strings.TrimSpace(trimmed[index+len(prefix):])
		afterLower := strings.ToLower(after)
		for _, marker := range []string{
			" and tell me the task id",
			", and tell me the task id",
			" and return only the task id",
			", return only the task id",
		} {
			markerIndex := strings.Index(afterLower, marker)
			if markerIndex < 0 {
				continue
			}
			after = strings.TrimSpace(after[:markerIndex])
			afterLower = strings.ToLower(after)
		}
		after = strings.TrimSpace(strings.Trim(after, " .,:;!?"))
		if strings.HasPrefix(strings.ToLower(after), "in this workspace") {
			after = strings.TrimSpace(after[len("in this workspace"):])
		}
		after = strings.TrimSpace(strings.Trim(after, " .,:;!?"))
		if after != "" {
			return "Create one actionable task: " + after, true
		}
		if strings.Contains(lower, "rollout plan") {
			return "Create one actionable task from the rollout plan discussed in this conversation.", true
		}
		return "Create one actionable task from the latest plan discussed in this conversation.", true
	}
	return "", false
}

func cleanMonitorGoal(value string) string {
	goal := strings.TrimSpace(value)
	if goal == "" {
		return ""
	}
	lower := strings.ToLower(goal)
	for _, marker := range []string{
		" and tell me",
		", and tell me",
		" and then tell me",
		", then tell me",
		" and show me",
		", and show me",
		" and report",
		", and report",
	} {
		index := strings.Index(lower, marker)
		if index < 0 {
			continue
		}
		goal = strings.TrimSpace(goal[:index])
		lower = strings.ToLower(goal)
	}
	goal = strings.TrimSpace(strings.Trim(goal, " .,:;!?"))
	if strings.HasPrefix(strings.ToLower(goal), "to ") {
		goal = strings.TrimSpace(goal[len("to "):])
	}
	return goal
}

func parseIntentApproveAction(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	if !strings.Contains(lower, "approve") {
		return "", false
	}
	if strings.Contains(lower, "deny") || strings.Contains(lower, "reject") || strings.Contains(lower, "decline") {
		return "", false
	}
	actionID, ok := findActionID(trimmed)
	if !ok {
		return "", false
	}
	return actionID, true
}

func parseIntentDenyAction(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	hasDenyVerb := strings.Contains(lower, "deny") || strings.Contains(lower, "reject") || strings.Contains(lower, "decline")
	if !hasDenyVerb {
		return "", false
	}
	actionID, _, end, ok := findActionIDWithBounds(trimmed)
	if !ok {
		return "", false
	}
	reason := strings.TrimSpace(trimmed[end:])
	reason = normalizeDenyReason(reason)
	if reason == "" {
		return actionID, true
	}
	return actionID + " " + reason, true
}

func parseIntentApprovePairing(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	if !strings.Contains(lower, "approve") {
		return "", false
	}
	if strings.Contains(lower, "deny") || strings.Contains(lower, "reject") || strings.Contains(lower, "decline") {
		return "", false
	}
	token, _, _, ok := findPairingTokenWithBounds(trimmed)
	if !ok {
		return "", false
	}
	return token, true
}

func parseIntentDenyPairing(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	if !(strings.Contains(lower, "deny") || strings.Contains(lower, "reject") || strings.Contains(lower, "decline")) {
		return "", false
	}
	token, _, end, ok := findPairingTokenWithBounds(trimmed)
	if !ok {
		return "", false
	}
	reason := strings.TrimSpace(trimmed[end:])
	reason = normalizeDenyReason(reason)
	if reason == "" {
		return token, true
	}
	return token + " " + reason, true
}

func normalizeDenyReason(value string) string {
	reason := strings.TrimSpace(value)
	reason = strings.Trim(reason, " .,:;!?-")
	reasonLower := strings.ToLower(reason)
	switch {
	case strings.HasPrefix(reasonLower, "because "):
		reason = strings.TrimSpace(reason[len("because "):])
	case strings.HasPrefix(reasonLower, "reason "):
		reason = strings.TrimSpace(reason[len("reason "):])
	case strings.HasPrefix(reasonLower, "for "):
		reason = strings.TrimSpace(reason[len("for "):])
	}
	return strings.TrimSpace(strings.Trim(reason, " .,:;!?-"))
}

func (s *Service) resolveSinglePendingActionID(ctx context.Context, input MessageInput) (string, string) {
	items, err := s.store.ListPendingActionApprovals(ctx, input.Connector, input.ExternalID, 2)
	if err != nil {
		return "", "Unable to load pending actions right now."
	}
	if len(items) == 1 {
		return strings.TrimSpace(items[0].ID), ""
	}
	if len(items) > 1 {
		return "", "Multiple pending actions found. Use `/pending-actions` and approve by id."
	}
	items, err = s.store.ListPendingActionApprovalsGlobal(ctx, 2)
	if err != nil {
		return "", "Unable to load pending actions right now."
	}
	if len(items) == 0 {
		return "", "No pending actions."
	}
	if len(items) > 1 {
		return "", "Multiple pending actions found across contexts. Use `/pending-actions` and approve by id."
	}
	return strings.TrimSpace(items[0].ID), ""
}

func (s *Service) resolveMostRecentPendingActionID(ctx context.Context, input MessageInput) (string, string) {
	items, err := s.store.ListPendingActionApprovals(ctx, input.Connector, input.ExternalID, 50)
	if err != nil {
		return "", "Unable to load pending actions right now."
	}
	if len(items) == 0 {
		items, err = s.store.ListPendingActionApprovalsGlobal(ctx, 50)
		if err != nil {
			return "", "Unable to load pending actions right now."
		}
	}
	if len(items) == 0 {
		return "", "No pending actions."
	}
	latest := items[len(items)-1]
	actionID := strings.TrimSpace(latest.ID)
	if actionID == "" {
		return "", "Unable to determine the latest pending action id."
	}
	return actionID, ""
}

func normalizeActionCommandID(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.Trim(trimmed, "`\"'")
	trimmed = strings.Trim(trimmed, "[](){}<>,.;:!?")
	if trimmed == "" {
		return ""
	}
	if actionID, ok := findActionID(trimmed); ok {
		return actionID
	}
	return trimmed
}

func findActionID(text string) (string, bool) {
	actionID, _, _, ok := findActionIDWithBounds(text)
	return actionID, ok
}

func findActionIDWithBounds(text string) (actionID string, start int, end int, ok bool) {
	lower := strings.ToLower(text)
	const prefix = "act_"
	search := 0
	for {
		offset := strings.Index(lower[search:], prefix)
		if offset < 0 {
			return "", 0, 0, false
		}
		start = search + offset
		end = start + len(prefix)
		for end < len(lower) {
			ch := lower[end]
			if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
				end++
				continue
			}
			break
		}
		if end-start < len(prefix)+4 {
			search = start + len(prefix)
			continue
		}
		return strings.Trim(lower[start:end], "`"), start, end, true
	}
}

func findPairingTokenWithBounds(text string) (token string, start int, end int, ok bool) {
	lower := strings.ToLower(text)
	contextHint := strings.Contains(lower, "pair") || strings.Contains(lower, "token")
	search := 0
	for {
		start = nextAlphaNumericStart(text, search)
		if start < 0 {
			return "", 0, 0, false
		}
		end = start
		for end < len(text) && isASCIIAlphaNumeric(text[end]) {
			end++
		}
		candidate := text[start:end]
		lowerCandidate := strings.ToLower(candidate)
		if isLikelyPairingToken(candidate, lowerCandidate, contextHint) {
			return strings.ToUpper(candidate), start, end, true
		}
		search = end + 1
	}
}

func nextAlphaNumericStart(text string, from int) int {
	for index := from; index < len(text); index++ {
		if isASCIIAlphaNumeric(text[index]) {
			return index
		}
	}
	return -1
}

func isLikelyPairingToken(candidate, lowerCandidate string, contextHint bool) bool {
	if len(candidate) < 8 || len(candidate) > 64 {
		return false
	}
	if strings.HasPrefix(lowerCandidate, "act_") {
		return false
	}
	switch lowerCandidate {
	case "approve", "approved", "approval", "action", "pair", "pairing", "token", "please", "deny", "denied", "reject", "rejected", "decline", "because", "reason":
		return false
	}
	if contextHint {
		return true
	}
	return hasASCIIDigit(candidate) || candidate == strings.ToUpper(candidate)
}

func hasASCIIDigit(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] >= '0' && value[index] <= '9' {
			return true
		}
	}
	return false
}

func isASCIIAlphaNumeric(value byte) bool {
	return (value >= 'a' && value <= 'z') ||
		(value >= 'A' && value <= 'Z') ||
		(value >= '0' && value <= '9')
}

func compactSnippet(input string) string {
	text := strings.TrimSpace(input)
	if text == "" {
		return ""
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= 120 {
		return text
	}
	return text[:120] + "..."
}

func formatActionExecutionReply(record store.ActionApproval) string {
	actionID := strings.TrimSpace(record.ID)
	if actionID == "" {
		actionID = "(unknown-action)"
	}
	switch strings.ToLower(strings.TrimSpace(record.ExecutionStatus)) {
	case "skipped":
		reason := humanizeExecutionMessage(record.ExecutionMessage)
		if reason == "" {
			reason = "No executor is configured for this workspace."
		}
		return fmt.Sprintf("I approved action `%s`, but it was not run. Outcome: %s", actionID, reason)
	case "failed":
		detail := humanizeExecutionFailure(record.ExecutionMessage)
		if detail == "" {
			detail = "Execution failed without additional details."
		}
		return fmt.Sprintf("I approved action `%s`, but execution failed. Outcome: %s", actionID, detail)
	default:
		plugin := fallbackPluginLabel(record.ExecutorPlugin)
		outcome := humanizeExecutionMessage(record.ExecutionMessage)
		if outcome == "" {
			outcome = "Completed successfully."
		}
		return fmt.Sprintf("I approved action `%s` and ran it with `%s`. Outcome: %s", actionID, plugin, outcome)
	}
}

func humanizeExecutionMessage(message string) string {
	text := strings.TrimSpace(message)
	if text == "" {
		return ""
	}
	text = trimCaseInsensitivePrefix(text, "command succeeded:")
	text = trimCaseInsensitivePrefix(text, "command completed:")
	text = trimCaseInsensitivePrefix(text, "webhook request completed with status")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(message)), "webhook request completed with status") {
		return "Webhook request completed with status " + text
	}
	return compactSnippet(text)
}

func humanizeExecutionFailure(message string) string {
	text := strings.TrimSpace(message)
	if text == "" {
		return ""
	}
	text = trimCaseInsensitivePrefix(text, "command failed:")
	parts := strings.SplitN(text, "; output=", 2)
	switch len(parts) {
	case 2:
		cause := compactSnippet(parts[0])
		output := compactSnippet(parts[1])
		if output == "" {
			return cause
		}
		if cause == "" {
			return "Output: " + output
		}
		return cause + ". Output: " + output
	default:
		return compactSnippet(text)
	}
}

func trimCaseInsensitivePrefix(value, prefix string) string {
	trimmedValue := strings.TrimSpace(value)
	trimmedPrefix := strings.TrimSpace(prefix)
	if trimmedValue == "" || trimmedPrefix == "" {
		return trimmedValue
	}
	if strings.HasPrefix(strings.ToLower(trimmedValue), strings.ToLower(trimmedPrefix)) {
		return strings.TrimSpace(trimmedValue[len(trimmedPrefix):])
	}
	return trimmedValue
}

func isAdminRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "overlord", "admin":
		return true
	default:
		return false
	}
}
