package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dwizi/agent-runtime/internal/adminclient"
	"github.com/dwizi/agent-runtime/internal/config"
)

var chatSectionPattern = regexp.MustCompile(`^##\s+(\S+)\s+` + "`" + `([^` + "`" + `]+)` + "`" + `\s*$`)
var usagePattern = regexp.MustCompile(`(?i)usage:\s*/([a-z0-9_-]+)`)

type parsedChatLog struct {
	SourcePath  string
	Connector   string
	ExternalID  string
	DisplayName string
	Entries     []parsedChatEntry
}

type parsedChatEntry struct {
	Timestamp time.Time
	Label     string
	Direction string
	Actor     string
	Text      string
}

type parsedChatTurn struct {
	Inbound   parsedChatEntry
	Outbounds []parsedChatEntry
	Tools     []parsedChatEntry
}

type evalReport struct {
	FilesScanned        int           `json:"files_scanned"`
	ConversationsParsed int           `json:"conversations_parsed"`
	Turns               int           `json:"turns"`
	InboundMessages     int           `json:"inbound_messages"`
	OutboundMessages    int           `json:"outbound_messages"`
	ToolMessages        int           `json:"tool_messages"`
	DuplicateToolBursts int           `json:"duplicate_tool_bursts"`
	PolicyExhaustions   int           `json:"policy_exhaustions"`
	CommandUsageBounces int           `json:"command_usage_bounces"`
	ApprovalWaitReplies int           `json:"approval_wait_replies"`
	Findings            []evalFinding `json:"findings"`
	Recommendations     []string      `json:"recommendations"`
}

type evalFinding struct {
	Code    string `json:"code"`
	Count   int    `json:"count"`
	Example string `json:"example,omitempty"`
	Detail  string `json:"detail"`
}

type replayResult struct {
	TotalTurns int
	SentTurns  int
	Failures   int
}

func newChatCommand(logger *slog.Logger) *cobra.Command {
	_ = logger
	var (
		connector  string
		externalID string
		fromUserID string
		display    string
		message    string
		timeoutSec int
	)

	cmd := &cobra.Command{
		Use:   "chat [message]",
		Short: "Talk to agent-runtime over admin API",
		Long:  "Interactive channel to message agent-runtime in real time, plus replay/eval utilities.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.FromEnv()
			client, err := adminclient.New(cfg)
			if err != nil {
				return err
			}

			text := strings.TrimSpace(message)
			if text == "" && len(args) > 0 {
				text = strings.TrimSpace(strings.Join(args, " "))
			}

			resolvedConnector, resolvedExternalID, resolvedFromUserID, resolvedDisplay := resolveChatIdentity(connector, externalID, fromUserID, display)

			if text != "" {
				ctx, cancel := context.WithTimeout(context.Background(), boundedTimeout(timeoutSec))
				defer cancel()
				response, err := client.Chat(ctx, adminclient.ChatRequest{
					Connector:   resolvedConnector,
					ExternalID:  resolvedExternalID,
					DisplayName: resolvedDisplay,
					FromUserID:  resolvedFromUserID,
					Text:        text,
				})
				if err != nil {
					return err
				}
				if strings.TrimSpace(response.Reply) == "" {
					cmd.Println("(no reply)")
					return nil
				}
				cmd.Println(strings.TrimSpace(response.Reply))
				return nil
			}

			cmd.Printf("Connected to %s as %s (%s). Type /exit to quit.\n", resolvedConnector, resolvedDisplay, resolvedExternalID)
			return runInteractiveChat(cmd, client, resolvedConnector, resolvedExternalID, resolvedFromUserID, resolvedDisplay, timeoutSec)
		},
	}
	cmd.Flags().StringVar(&connector, "connector", "codex", "connector identity for this chat session")
	cmd.Flags().StringVar(&externalID, "external-id", "codex-cli", "external channel/session id")
	cmd.Flags().StringVar(&fromUserID, "from-user-id", "", "origin user id (defaults to external-id)")
	cmd.Flags().StringVar(&display, "display-name", "Codex CLI", "display name for context")
	cmd.Flags().StringVarP(&message, "message", "m", "", "single message to send (non-interactive mode)")
	cmd.Flags().IntVar(&timeoutSec, "timeout-sec", 120, "request timeout in seconds")

	cmd.AddCommand(newChatReplayCommand(logger))
	cmd.AddCommand(newChatEvalCommand(logger))
	return cmd
}

func runInteractiveChat(cmd *cobra.Command, client *adminclient.Client, connector, externalID, fromUserID, displayName string, timeoutSec int) error {
	scanner := bufio.NewScanner(cmd.InOrStdin())
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for {
		cmd.Print("you> ")
		if !scanner.Scan() {
			break
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		if text == "/exit" || text == "/quit" {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), boundedTimeout(timeoutSec))
		response, err := client.Chat(ctx, adminclient.ChatRequest{
			Connector:   connector,
			ExternalID:  externalID,
			DisplayName: displayName,
			FromUserID:  fromUserID,
			Text:        text,
		})
		cancel()
		if err != nil {
			cmd.PrintErrf("chat request failed: %v\n", err)
			continue
		}
		printAgentReply(cmd, strings.TrimSpace(response.Reply))
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func printAgentReply(cmd *cobra.Command, reply string) {
	if reply == "" {
		cmd.Println("agent> (no reply)")
		return
	}
	lines := strings.Split(reply, "\n")
	for index, line := range lines {
		line = strings.TrimRight(line, "\r")
		if index == 0 {
			cmd.Printf("agent> %s\n", line)
			continue
		}
		cmd.Printf("      %s\n", line)
	}
}

func newChatReplayCommand(logger *slog.Logger) *cobra.Command {
	_ = logger
	var (
		logPath      string
		connector    string
		externalID   string
		fromUserID   string
		display      string
		maxTurns     int
		delayMS      int
		dryRun       bool
		showExpected bool
		timeoutSec   int
	)

	cmd := &cobra.Command{
		Use:   "replay",
		Short: "Replay inbound turns from a chat log through runtime",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(logPath) == "" {
				return fmt.Errorf("--log is required")
			}
			parsed, err := parseChatLogFile(logPath)
			if err != nil {
				return err
			}
			turns := buildChatTurns(parsed)
			if len(turns) == 0 {
				return fmt.Errorf("no inbound turns found in %s", logPath)
			}
			if maxTurns > 0 && len(turns) > maxTurns {
				turns = turns[:maxTurns]
			}

			resolvedConnector, resolvedExternalID, resolvedFromUserID, resolvedDisplay := resolveChatIdentity(
				firstNonEmpty(connector, parsed.Connector),
				firstNonEmpty(externalID, parsed.ExternalID),
				fromUserID,
				firstNonEmpty(display, parsed.DisplayName),
			)

			cmd.Printf("Replaying %d turn(s) into %s/%s\n", len(turns), resolvedConnector, resolvedExternalID)
			if dryRun {
				for index, turn := range turns {
					cmd.Printf("[%d] user: %s\n", index+1, compactLine(turn.Inbound.Text, 200))
					if showExpected {
						expected := strings.TrimSpace(firstTurnOutbound(turn))
						if expected == "" {
							expected = "(none)"
						}
						cmd.Printf("    expected: %s\n", compactLine(expected, 200))
					}
				}
				return nil
			}

			cfg := config.FromEnv()
			client, err := adminclient.New(cfg)
			if err != nil {
				return err
			}
			result := replayTurns(cmd, client, turns, replayRequest{
				Connector:    resolvedConnector,
				ExternalID:   resolvedExternalID,
				FromUserID:   resolvedFromUserID,
				DisplayName:  resolvedDisplay,
				Delay:        time.Duration(maxInt(delayMS, 0)) * time.Millisecond,
				ShowExpected: showExpected,
				TimeoutSec:   timeoutSec,
			})
			cmd.Printf("Replay complete: sent=%d failures=%d total=%d\n", result.SentTurns, result.Failures, result.TotalTurns)
			if result.Failures > 0 {
				return fmt.Errorf("replay finished with %d failed turn(s)", result.Failures)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&logPath, "log", "", "path to a chat markdown log")
	cmd.Flags().StringVar(&connector, "connector", "", "override connector from log header")
	cmd.Flags().StringVar(&externalID, "external-id", "", "override external id from log header")
	cmd.Flags().StringVar(&fromUserID, "from-user-id", "", "origin user id (defaults to external-id)")
	cmd.Flags().StringVar(&display, "display-name", "", "override display name from log header")
	cmd.Flags().IntVar(&maxTurns, "max-turns", 0, "max inbound turns to replay (0 means all)")
	cmd.Flags().IntVar(&delayMS, "delay-ms", 0, "delay between replayed turns")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print planned replay turns without sending")
	cmd.Flags().BoolVar(&showExpected, "show-expected", true, "print first historical outbound next to runtime replay output")
	cmd.Flags().IntVar(&timeoutSec, "timeout-sec", 120, "request timeout in seconds")

	return cmd
}

type replayRequest struct {
	Connector    string
	ExternalID   string
	FromUserID   string
	DisplayName  string
	Delay        time.Duration
	ShowExpected bool
	TimeoutSec   int
}

func replayTurns(cmd *cobra.Command, client *adminclient.Client, turns []parsedChatTurn, req replayRequest) replayResult {
	result := replayResult{TotalTurns: len(turns)}
	for index, turn := range turns {
		userText := strings.TrimSpace(turn.Inbound.Text)
		if userText == "" {
			continue
		}
		result.SentTurns++
		cmd.Printf("[%d] user: %s\n", index+1, compactLine(userText, 220))

		ctx, cancel := context.WithTimeout(context.Background(), boundedTimeout(req.TimeoutSec))
		response, err := client.Chat(ctx, adminclient.ChatRequest{
			Connector:   req.Connector,
			ExternalID:  req.ExternalID,
			FromUserID:  req.FromUserID,
			DisplayName: req.DisplayName,
			Text:        userText,
		})
		cancel()
		if err != nil {
			result.Failures++
			cmd.Printf("    error: %v\n", err)
			if req.Delay > 0 {
				time.Sleep(req.Delay)
			}
			continue
		}

		actual := strings.TrimSpace(response.Reply)
		if actual == "" {
			actual = "(no reply)"
		}
		cmd.Printf("    agent: %s\n", compactLine(actual, 220))
		if req.ShowExpected {
			expected := strings.TrimSpace(firstTurnOutbound(turn))
			if expected == "" {
				expected = "(none)"
			}
			cmd.Printf("    prev:  %s\n", compactLine(expected, 220))
		}
		if req.Delay > 0 {
			time.Sleep(req.Delay)
		}
	}
	return result
}

func firstTurnOutbound(turn parsedChatTurn) string {
	if len(turn.Outbounds) == 0 {
		return ""
	}
	return strings.TrimSpace(turn.Outbounds[0].Text)
}

func newChatEvalCommand(logger *slog.Logger) *cobra.Command {
	_ = logger
	var (
		targetPath string
		jsonMode   bool
	)
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Analyze chat logs for agent quality regressions and improvement targets",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(targetPath) == "" {
				return fmt.Errorf("--path is required")
			}
			files, err := collectChatLogFiles(targetPath)
			if err != nil {
				return err
			}
			if len(files) == 0 {
				return fmt.Errorf("no chat logs found at %s", targetPath)
			}

			report := evaluateChatLogFiles(files)
			if jsonMode {
				payload, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return err
				}
				cmd.Println(string(payload))
				return nil
			}

			cmd.Printf("Files scanned: %d\n", report.FilesScanned)
			cmd.Printf("Conversations parsed: %d\n", report.ConversationsParsed)
			cmd.Printf("Turns: %d (inbound=%d outbound=%d tool=%d)\n", report.Turns, report.InboundMessages, report.OutboundMessages, report.ToolMessages)
			cmd.Printf("Signals: duplicate-tool=%d policy-exhaustion=%d usage-bounce=%d approval-wait=%d\n", report.DuplicateToolBursts, report.PolicyExhaustions, report.CommandUsageBounces, report.ApprovalWaitReplies)
			if len(report.Findings) > 0 {
				cmd.Println("Findings:")
				for _, finding := range report.Findings {
					example := ""
					if finding.Example != "" {
						example = " [" + finding.Example + "]"
					}
					cmd.Printf("- %s: %d%s\n  %s\n", finding.Code, finding.Count, example, finding.Detail)
				}
			}
			if len(report.Recommendations) > 0 {
				cmd.Println("Recommendations:")
				for index, recommendation := range report.Recommendations {
					cmd.Printf("%d. %s\n", index+1, recommendation)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&targetPath, "path", "", "chat log file or directory to analyze")
	cmd.Flags().BoolVar(&jsonMode, "json", false, "emit JSON report")
	return cmd
}

func evaluateChatLogFiles(paths []string) evalReport {
	report := evalReport{FilesScanned: len(paths)}
	findingMap := map[string]*evalFinding{}

	for _, path := range paths {
		parsed, err := parseChatLogFile(path)
		if err != nil {
			continue
		}
		if len(parsed.Entries) == 0 {
			continue
		}
		report.ConversationsParsed++
		turns := buildChatTurns(parsed)
		report.Turns += len(turns)

		for _, entry := range parsed.Entries {
			switch strings.ToLower(strings.TrimSpace(entry.Direction)) {
			case "inbound":
				report.InboundMessages++
			case "outbound":
				report.OutboundMessages++
			case "tool":
				report.ToolMessages++
			}
		}

		for _, turn := range turns {
			dupCount := duplicateToolSignatures(turn)
			if dupCount > 0 {
				report.DuplicateToolBursts += dupCount
				addFinding(findingMap, "duplicate_tool_calls", dupCount, path, "Same tool+args executed repeatedly in one turn; this inflates approvals and hits policy limits.")
			}
			if hasPolicyExhaustion(turn) {
				report.PolicyExhaustions++
				addFinding(findingMap, "policy_exhaustion", 1, path, "Turn reached tool-call policy ceiling and failed to complete user goal.")
			}
			if hasCommandUsageBounce(turn) {
				report.CommandUsageBounces++
				addFinding(findingMap, "command_usage_bounce", 1, path, "Slash command was answered with usage text instead of progressing the task.")
			}
			if hasApprovalWaitReply(turn) {
				report.ApprovalWaitReplies++
				addFinding(findingMap, "approval_wait", 1, path, "Reply stopped at approval gating without giving the shortest actionable next step.")
			}
		}
	}

	report.Findings = flattenFindings(findingMap)
	report.Recommendations = buildRecommendations(report)
	return report
}

func addFinding(store map[string]*evalFinding, code string, count int, example, detail string) {
	if count <= 0 {
		return
	}
	item, exists := store[code]
	if !exists {
		store[code] = &evalFinding{
			Code:    code,
			Count:   count,
			Example: example,
			Detail:  detail,
		}
		return
	}
	item.Count += count
	if strings.TrimSpace(item.Example) == "" {
		item.Example = example
	}
}

func flattenFindings(findingMap map[string]*evalFinding) []evalFinding {
	if len(findingMap) == 0 {
		return nil
	}
	keys := make([]string, 0, len(findingMap))
	for key := range findingMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]evalFinding, 0, len(keys))
	for _, key := range keys {
		result = append(result, *findingMap[key])
	}
	return result
}

func buildRecommendations(report evalReport) []string {
	recommendations := make([]string, 0, 4)
	if report.DuplicateToolBursts > 0 {
		recommendations = append(recommendations, "Add turn-local dedupe keyed by tool name + canonical args so repeated attempts return cached outcome instead of creating new approvals.")
	}
	if report.CommandUsageBounces > 0 {
		recommendations = append(recommendations, "When a slash command is malformed, return a one-line fix with concrete example and preserve any detected action id/token automatically.")
	}
	if report.PolicyExhaustions > 0 {
		recommendations = append(recommendations, "When tool policy cap is hit, produce an automatic next-step plan and pause further tool calls instead of retrying similar actions.")
	}
	if report.ApprovalWaitReplies > 0 {
		recommendations = append(recommendations, "For approval-gated actions, always include exact next command plus latest pending action id and a '/pending-actions' hint.")
	}
	if len(recommendations) == 0 {
		recommendations = append(recommendations, "No major systemic regressions were detected in scanned logs; continue replay-based regression testing on new changes.")
	}
	return recommendations
}

func hasApprovalWaitReply(turn parsedChatTurn) bool {
	for _, outbound := range turn.Outbounds {
		text := strings.ToLower(strings.TrimSpace(outbound.Text))
		if text == "" {
			continue
		}
		if strings.Contains(text, "waiting on admin approval") ||
			strings.Contains(text, "admin approves") ||
			(strings.Contains(text, "pending") && strings.Contains(text, "approve")) {
			return true
		}
	}
	return false
}

func hasPolicyExhaustion(turn parsedChatTurn) bool {
	for _, tool := range turn.Tools {
		if strings.Contains(strings.ToLower(tool.Text), "exceeds per-turn policy") {
			return true
		}
	}
	for _, outbound := range turn.Outbounds {
		text := strings.ToLower(strings.TrimSpace(outbound.Text))
		if strings.Contains(text, "cannot run more tools") || strings.Contains(text, "tool call exceeds per-turn policy") {
			return true
		}
	}
	return false
}

func hasCommandUsageBounce(turn parsedChatTurn) bool {
	if extractSlashCommand(turn.Inbound.Text) == "" {
		return false
	}
	for _, outbound := range turn.Outbounds {
		if usagePattern.MatchString(outbound.Text) {
			return true
		}
	}
	return false
}

func duplicateToolSignatures(turn parsedChatTurn) int {
	counts := map[string]int{}
	for _, tool := range turn.Tools {
		signature := toolSignature(tool.Text)
		if signature == "" {
			continue
		}
		counts[signature]++
	}
	duplicates := 0
	for _, count := range counts {
		if count > 1 {
			duplicates += count - 1
		}
	}
	return duplicates
}

func toolSignature(text string) string {
	lines := strings.Split(text, "\n")
	toolName := ""
	args := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- tool:") {
			toolName = extractBacktickValue(trimmed)
			if toolName == "" {
				toolName = strings.TrimSpace(strings.TrimPrefix(trimmed, "- tool:"))
			}
		}
		if strings.HasPrefix(trimmed, "- args:") {
			args = extractBacktickValue(trimmed)
			if args == "" {
				args = strings.TrimSpace(strings.TrimPrefix(trimmed, "- args:"))
			}
		}
	}
	toolName = strings.ToLower(strings.TrimSpace(toolName))
	args = compactWhitespace(args)
	if toolName == "" {
		return ""
	}
	return toolName + "|" + args
}

func buildChatTurns(parsed parsedChatLog) []parsedChatTurn {
	turns := make([]parsedChatTurn, 0)
	var current *parsedChatTurn
	for _, entry := range parsed.Entries {
		direction := strings.ToLower(strings.TrimSpace(entry.Direction))
		switch direction {
		case "inbound":
			if current != nil {
				turns = append(turns, *current)
			}
			current = &parsedChatTurn{Inbound: entry}
		case "outbound":
			if current != nil {
				current.Outbounds = append(current.Outbounds, entry)
			}
		case "tool":
			if current != nil {
				current.Tools = append(current.Tools, entry)
			}
		}
	}
	if current != nil {
		turns = append(turns, *current)
	}
	return turns
}

func collectChatLogFiles(targetPath string) ([]string, error) {
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		return nil, fmt.Errorf("path is required")
	}
	info, err := os.Stat(targetPath)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{targetPath}, nil
	}

	files := make([]string, 0)
	err = filepath.WalkDir(targetPath, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if strings.ToLower(filepath.Ext(path)) != ".md" {
			return nil
		}
		normalized := filepath.ToSlash(strings.ToLower(path))
		if strings.Contains(normalized, "/logs/chats/") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func parseChatLogFile(path string) (parsedChatLog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return parsedChatLog{}, err
	}
	parsed, err := parseChatLogContent(string(data))
	if err != nil {
		return parsedChatLog{}, err
	}
	parsed.SourcePath = path
	return parsed, nil
}

func parseChatLogContent(content string) (parsedChatLog, error) {
	var parsed parsedChatLog
	lines := strings.Split(content, "\n")
	var current *parsedChatEntry
	bodyLines := make([]string, 0, 8)

	flushCurrent := func() {
		if current == nil {
			return
		}
		entry := *current
		entry.Text = strings.TrimSpace(strings.Join(bodyLines, "\n"))
		if entry.Direction == "" {
			switch strings.ToUpper(strings.TrimSpace(entry.Label)) {
			case "INBOUND":
				entry.Direction = "inbound"
			case "OUTBOUND":
				entry.Direction = "outbound"
			case "TOOL":
				entry.Direction = "tool"
			}
		}
		parsed.Entries = append(parsed.Entries, entry)
		current = nil
		bodyLines = bodyLines[:0]
	}

	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if matches := chatSectionPattern.FindStringSubmatch(trimmed); len(matches) == 3 {
			flushCurrent()
			ts, _ := time.Parse(time.RFC3339, matches[1])
			current = &parsedChatEntry{Timestamp: ts, Label: strings.TrimSpace(matches[2])}
			continue
		}

		if current == nil {
			switch {
			case strings.HasPrefix(trimmed, "- connector:"):
				parsed.Connector = extractBacktickOrRemainder(trimmed, "- connector:")
			case strings.HasPrefix(trimmed, "- external_id:"):
				parsed.ExternalID = extractBacktickOrRemainder(trimmed, "- external_id:")
			case strings.HasPrefix(trimmed, "- display_name:"):
				parsed.DisplayName = extractBacktickOrRemainder(trimmed, "- display_name:")
			}
			continue
		}

		if strings.HasPrefix(trimmed, "- direction:") {
			current.Direction = strings.ToLower(extractBacktickOrRemainder(trimmed, "- direction:"))
			continue
		}
		if strings.HasPrefix(trimmed, "- actor:") {
			current.Actor = extractBacktickOrRemainder(trimmed, "- actor:")
			continue
		}

		bodyLines = append(bodyLines, line)
	}
	flushCurrent()
	return parsed, nil
}

func resolveChatIdentity(connector, externalID, fromUserID, displayName string) (string, string, string, string) {
	connector = strings.ToLower(strings.TrimSpace(connector))
	if connector == "" {
		connector = "codex"
	}
	externalID = strings.TrimSpace(externalID)
	if externalID == "" {
		externalID = "codex-cli"
	}
	fromUserID = strings.TrimSpace(fromUserID)
	if fromUserID == "" {
		fromUserID = externalID
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = externalID
	}
	return connector, externalID, fromUserID, displayName
}

func extractBacktickOrRemainder(line, prefix string) string {
	value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if extracted := extractBacktickValue(value); extracted != "" {
		return extracted
	}
	return strings.Trim(value, "`")
}

func extractBacktickValue(input string) string {
	start := strings.Index(input, "`")
	if start < 0 {
		return ""
	}
	end := strings.Index(input[start+1:], "`")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(input[start+1 : start+1+end])
}

func extractSlashCommand(text string) string {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "/") {
			return ""
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			return ""
		}
		return strings.TrimPrefix(fields[0], "/")
	}
	return ""
}

func boundedTimeout(input int) time.Duration {
	if input < 1 {
		input = 120
	}
	if input > 600 {
		input = 600
	}
	return time.Duration(input) * time.Second
}

func compactLine(input string, maxLen int) string {
	line := compactWhitespace(input)
	if maxLen < 1 || len(line) <= maxLen {
		return line
	}
	return strings.TrimSpace(line[:maxLen]) + "..."
}

func compactWhitespace(input string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
