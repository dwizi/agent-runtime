package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Environment                      string
	HTTPAddr                         string
	DataDir                          string
	DBPath                           string
	WorkspaceRoot                    string
	DefaultConcurrency               int
	QMDBinary                        string
	QMDSidecarURL                    string
	QMDSidecarAddr                   string
	QMDIndexName                     string
	QMDCollectionName                string
	QMDSharedModelsDir               string
	QMDEmbedExcludeGlobsCSV          string
	QMDSearchLimit                   int
	QMDOpenMaxBytes                  int
	QMDDebounceSeconds               int
	QMDIndexTimeoutSec               int
	QMDQueryTimeoutSec               int
	QMDAutoEmbed                     bool
	ObjectivePollSec                 int
	HeartbeatEnabled                 bool
	HeartbeatIntervalSec             int
	HeartbeatStaleSec                int
	HeartbeatNotifyAdmin             bool
	TriageEnabled                    bool
	TriageNotifyAdmin                bool
	TaskNotifyPolicy                 string
	TaskNotifySuccessPolicy          string
	TaskNotifyFailurePolicy          string
	AgentSensitiveApprovalTTLSeconds int
	CommandSyncEnabled               bool

	DiscordToken              string
	DiscordAPI                string
	DiscordWSURL              string
	DiscordApplicationID      string
	DiscordCommandGuildIDsCSV string
	TelegramToken             string
	TelegramAPI               string
	TelegramPoll              int
	IMAPHost                  string
	IMAPPort                  int
	IMAPUsername              string
	IMAPPassword              string
	IMAPMailbox               string
	IMAPPollSeconds           int
	IMAPTLSSkipVerify         bool

	LLMProvider   string // openai | anthropic
	LLMBaseURL    string
	LLMAPIKey     string
	LLMModel      string
	LLMTimeoutSec int

	SMTPHost                           string
	SMTPPort                           int
	SMTPUsername                       string
	SMTPPassword                       string
	SMTPFrom                           string
	SandboxEnabled                     bool
	SandboxAllowedCommandsCSV          string
	SandboxRunnerCommand               string
	SandboxRunnerArgs                  string
	SandboxTimeoutSec                  int
	SandboxMaxOutputBytes              int
	LLMEnabled                         bool
	LLMAllowDM                         bool
	LLMRequireMentionInGroups          bool
	LLMAllowedRolesCSV                 string
	LLMAllowedContextIDsCSV            string
	LLMRateLimitPerWindow              int
	LLMRateLimitWindowSec              int
	LLMGroundingTopK                   int
	LLMGroundingMaxDocExcerpt          int
	LLMGroundingMaxPromptBytes         int
	LLMGroundingChatTailLines          int
	LLMGroundingChatTailBytes          int
	LLMAdminSystemPrompt               string
	LLMPublicSystemPrompt              string
	AgentMaxTurnDurationSec            int
	AgentGroundingFirstStep            bool
	AgentGroundingEveryStep            bool
	AgentAutonomousMaxLoopSteps        int
	AgentAutonomousMaxTurnDurationSec  int
	AgentAutonomousMaxToolCallsPerTurn int
	AgentAutonomousMaxTasksPerHour     int
	AgentAutonomousMaxTasksPerDay      int
	AgentAutonomousMinConfidence       float64
	SoulGlobalFile                     string
	SoulWorkspaceRelPath               string
	SoulContextRelPath                 string
	SystemPromptGlobalFile             string
	SystemPromptWorkspacePath          string
	SystemPromptContextPath            string
	ReasoningPromptFile                string
	SkillsGlobalRoot                   string

	PublicHost string
	AdminHost  string

	AdminAPIURL        string
	AdminTLSSkipVerify bool
	AdminTLSCAFile     string
	AdminTLSCertFile   string
	AdminTLSKeyFile    string

	TUIApproverUserID string
	TUIApprovalRole   string
}

func FromEnv() Config {
	dataDir := stringOrDefault("AGENT_RUNTIME_DATA_DIR", "/data")
	workspaceRoot := stringOrDefault("AGENT_RUNTIME_WORKSPACE_ROOT", filepath.Join(dataDir, "workspaces"))
	dbPath := stringOrDefault("AGENT_RUNTIME_DB_PATH", filepath.Join(dataDir, "agent-runtime", "meta.sqlite"))

	return Config{
		Environment:                      stringOrDefault("AGENT_RUNTIME_ENV", "development"),
		HTTPAddr:                         stringOrDefault("AGENT_RUNTIME_HTTP_ADDR", ":8080"),
		DataDir:                          dataDir,
		DBPath:                           dbPath,
		WorkspaceRoot:                    workspaceRoot,
		DefaultConcurrency:               intOrDefault("AGENT_RUNTIME_DEFAULT_CONCURRENCY", 5),
		QMDBinary:                        stringOrDefault("AGENT_RUNTIME_QMD_BINARY", "qmd"),
		QMDSidecarURL:                    strings.TrimSpace(os.Getenv("AGENT_RUNTIME_QMD_SIDECAR_URL")),
		QMDSidecarAddr:                   stringOrDefault("AGENT_RUNTIME_QMD_SIDECAR_ADDR", ":8091"),
		QMDIndexName:                     stringOrDefault("AGENT_RUNTIME_QMD_INDEX", "agent-runtime"),
		QMDCollectionName:                stringOrDefault("AGENT_RUNTIME_QMD_COLLECTION", "workspace"),
		QMDSharedModelsDir:               stringOrDefault("AGENT_RUNTIME_QMD_SHARED_MODELS_DIR", filepath.Join(dataDir, "qmd-models")),
		QMDEmbedExcludeGlobsCSV:          strings.TrimSpace(os.Getenv("AGENT_RUNTIME_QMD_EMBED_EXCLUDE_GLOBS")),
		QMDSearchLimit:                   intOrDefault("AGENT_RUNTIME_QMD_SEARCH_LIMIT", 5),
		QMDOpenMaxBytes:                  intOrDefault("AGENT_RUNTIME_QMD_OPEN_MAX_BYTES", 1600),
		QMDDebounceSeconds:               intOrDefault("AGENT_RUNTIME_QMD_DEBOUNCE_SECONDS", 3),
		QMDIndexTimeoutSec:               intOrDefault("AGENT_RUNTIME_QMD_INDEX_TIMEOUT_SECONDS", 180),
		QMDQueryTimeoutSec:               intOrDefault("AGENT_RUNTIME_QMD_QUERY_TIMEOUT_SECONDS", 30),
		QMDAutoEmbed:                     boolOrDefault("AGENT_RUNTIME_QMD_AUTO_EMBED", true),
		ObjectivePollSec:                 intOrDefault("AGENT_RUNTIME_OBJECTIVE_POLL_SECONDS", 15),
		HeartbeatEnabled:                 boolOrDefault("AGENT_RUNTIME_HEARTBEAT_ENABLED", true),
		HeartbeatIntervalSec:             intOrDefault("AGENT_RUNTIME_HEARTBEAT_INTERVAL_SECONDS", 30),
		HeartbeatStaleSec:                intOrDefault("AGENT_RUNTIME_HEARTBEAT_STALE_SECONDS", 120),
		HeartbeatNotifyAdmin:             boolOrDefault("AGENT_RUNTIME_HEARTBEAT_NOTIFY_ADMIN", true),
		TriageEnabled:                    boolOrDefault("AGENT_RUNTIME_TRIAGE_ENABLED", true),
		TriageNotifyAdmin:                boolOrDefault("AGENT_RUNTIME_TRIAGE_NOTIFY_ADMIN", true),
		TaskNotifyPolicy:                 notificationPolicyOrDefault("AGENT_RUNTIME_TASK_NOTIFY_POLICY", "both"),
		TaskNotifySuccessPolicy:          notificationPolicyOrDefault("AGENT_RUNTIME_TASK_NOTIFY_SUCCESS_POLICY", ""),
		TaskNotifyFailurePolicy:          notificationPolicyOrDefault("AGENT_RUNTIME_TASK_NOTIFY_FAILURE_POLICY", ""),
		AgentSensitiveApprovalTTLSeconds: intOrDefault("AGENT_RUNTIME_AGENT_SENSITIVE_APPROVAL_TTL_SECONDS", 600),
		CommandSyncEnabled:               boolOrDefault("AGENT_RUNTIME_COMMAND_SYNC_ENABLED", true),
		DiscordToken:                     os.Getenv("AGENT_RUNTIME_DISCORD_TOKEN"),
		DiscordAPI:                       stringOrDefault("AGENT_RUNTIME_DISCORD_API_BASE", "https://discord.com/api/v10"),
		DiscordWSURL:                     stringOrDefault("AGENT_RUNTIME_DISCORD_GATEWAY_URL", "wss://gateway.discord.gg/?v=10&encoding=json"),
		DiscordApplicationID:             strings.TrimSpace(os.Getenv("AGENT_RUNTIME_DISCORD_APPLICATION_ID")),
		DiscordCommandGuildIDsCSV:        strings.TrimSpace(os.Getenv("AGENT_RUNTIME_DISCORD_COMMAND_GUILD_IDS")),
		TelegramToken:                    os.Getenv("AGENT_RUNTIME_TELEGRAM_TOKEN"),
		TelegramAPI:                      stringOrDefault("AGENT_RUNTIME_TELEGRAM_API_BASE", "https://api.telegram.org"),
		TelegramPoll:                     intOrDefault("AGENT_RUNTIME_TELEGRAM_POLL_SECONDS", 25),
		IMAPHost:                         strings.TrimSpace(os.Getenv("AGENT_RUNTIME_IMAP_HOST")),
		IMAPPort:                         intOrDefault("AGENT_RUNTIME_IMAP_PORT", 993),
		IMAPUsername:                     strings.TrimSpace(os.Getenv("AGENT_RUNTIME_IMAP_USERNAME")),
		IMAPPassword:                     os.Getenv("AGENT_RUNTIME_IMAP_PASSWORD"),
		IMAPMailbox:                      stringOrDefault("AGENT_RUNTIME_IMAP_MAILBOX", "INBOX"),
		IMAPPollSeconds:                  intOrDefault("AGENT_RUNTIME_IMAP_POLL_SECONDS", 60),
		IMAPTLSSkipVerify:                boolOrDefault("AGENT_RUNTIME_IMAP_TLS_SKIP_VERIFY", false),

		LLMProvider:   stringOrDefault("AGENT_RUNTIME_LLM_PROVIDER", "openai"),
		LLMBaseURL:    stringOrDefault("AGENT_RUNTIME_LLM_BASE_URL", "https://api.openai.com/v1"),
		LLMAPIKey:     strings.TrimSpace(os.Getenv("AGENT_RUNTIME_LLM_API_KEY")),
		LLMModel:      stringOrDefault("AGENT_RUNTIME_LLM_MODEL", "gpt-4o"),
		LLMTimeoutSec: intOrDefault("AGENT_RUNTIME_LLM_TIMEOUT_SECONDS", 60),

		SMTPHost:                           strings.TrimSpace(os.Getenv("AGENT_RUNTIME_SMTP_HOST")),
		SMTPPort:                           intOrDefault("AGENT_RUNTIME_SMTP_PORT", 587),
		SMTPUsername:                       strings.TrimSpace(os.Getenv("AGENT_RUNTIME_SMTP_USERNAME")),
		SMTPPassword:                       os.Getenv("AGENT_RUNTIME_SMTP_PASSWORD"),
		SMTPFrom:                           strings.TrimSpace(os.Getenv("AGENT_RUNTIME_SMTP_FROM")),
		SandboxEnabled:                     boolOrDefault("AGENT_RUNTIME_SANDBOX_ENABLED", true),
		SandboxAllowedCommandsCSV:          stringOrDefault("AGENT_RUNTIME_SANDBOX_ALLOWED_COMMANDS", "echo,cat,ls,curl,grep,rg,head,tail,python3,chromium,sh,bash,ash,apk,pip,pip3,git,jq,sed,awk,find,mkdir,rm,cp,mv,touch,chmod,unzip,tar,gzip,wc,sort,uniq,tee,date,sleep,whoami,pwd,ps,top,kill,node,npm"),
		SandboxRunnerCommand:               strings.TrimSpace(os.Getenv("AGENT_RUNTIME_SANDBOX_RUNNER_COMMAND")),
		SandboxRunnerArgs:                  strings.TrimSpace(os.Getenv("AGENT_RUNTIME_SANDBOX_RUNNER_ARGS")),
		SandboxTimeoutSec:                  intOrDefault("AGENT_RUNTIME_SANDBOX_TIMEOUT_SECONDS", 20),
		SandboxMaxOutputBytes:              intOrDefault("AGENT_RUNTIME_SANDBOX_MAX_OUTPUT_BYTES", 500*1024),
		LLMEnabled:                         boolOrDefault("AGENT_RUNTIME_LLM_ENABLED", true),
		LLMAllowDM:                         boolOrDefault("AGENT_RUNTIME_LLM_ALLOW_DM", true),
		LLMRequireMentionInGroups:          boolOrDefault("AGENT_RUNTIME_LLM_REQUIRE_MENTION_IN_GROUPS", true),
		LLMAllowedRolesCSV:                 strings.TrimSpace(os.Getenv("AGENT_RUNTIME_LLM_ALLOWED_ROLES")),
		LLMAllowedContextIDsCSV:            strings.TrimSpace(os.Getenv("AGENT_RUNTIME_LLM_ALLOWED_CONTEXT_IDS")),
		LLMRateLimitPerWindow:              intOrDefault("AGENT_RUNTIME_LLM_RATE_LIMIT_PER_WINDOW", 8),
		LLMRateLimitWindowSec:              intOrDefault("AGENT_RUNTIME_LLM_RATE_LIMIT_WINDOW_SECONDS", 60),
		LLMGroundingTopK:                   intOrDefault("AGENT_RUNTIME_LLM_GROUNDING_TOP_K", 3),
		LLMGroundingMaxDocExcerpt:          intOrDefault("AGENT_RUNTIME_LLM_GROUNDING_MAX_DOC_EXCERPT_BYTES", 1200),
		LLMGroundingMaxPromptBytes:         intOrDefault("AGENT_RUNTIME_LLM_GROUNDING_MAX_PROMPT_BYTES", 8000),
		LLMGroundingChatTailLines:          intOrDefault("AGENT_RUNTIME_LLM_GROUNDING_CHAT_TAIL_LINES", 24),
		LLMGroundingChatTailBytes:          intOrDefault("AGENT_RUNTIME_LLM_GROUNDING_CHAT_TAIL_BYTES", 1800),
		LLMAdminSystemPrompt:               stringOrDefault("AGENT_RUNTIME_LLM_ADMIN_SYSTEM_PROMPT", "You are assisting admin operators. Prioritize security, approvals, and operational clarity."),
		LLMPublicSystemPrompt:              stringOrDefault("AGENT_RUNTIME_LLM_PUBLIC_SYSTEM_PROMPT", "You are assisting community members. Be concise, safe, and policy-compliant."),
		AgentMaxTurnDurationSec:            intOrDefault("AGENT_RUNTIME_AGENT_MAX_TURN_DURATION_SECONDS", 120),
		AgentGroundingFirstStep:            boolOrDefault("AGENT_RUNTIME_AGENT_GROUNDING_FIRST_STEP", true),
		AgentGroundingEveryStep:            boolOrDefault("AGENT_RUNTIME_AGENT_GROUNDING_EVERY_STEP", false),
		AgentAutonomousMaxLoopSteps:        intOrDefault("AGENT_RUNTIME_AGENT_AUTONOMOUS_MAX_LOOP_STEPS", 50),
		AgentAutonomousMaxTurnDurationSec:  intOrDefault("AGENT_RUNTIME_AGENT_AUTONOMOUS_MAX_TURN_DURATION_SECONDS", 1200),
		AgentAutonomousMaxToolCallsPerTurn: intOrDefault("AGENT_RUNTIME_AGENT_AUTONOMOUS_MAX_TOOL_CALLS_PER_TURN", 100),
		AgentAutonomousMaxTasksPerHour:     intOrDefault("AGENT_RUNTIME_AGENT_AUTONOMOUS_MAX_TASKS_PER_HOUR", 200),
		AgentAutonomousMaxTasksPerDay:      intOrDefault("AGENT_RUNTIME_AGENT_AUTONOMOUS_MAX_TASKS_PER_DAY", 1000),
		AgentAutonomousMinConfidence:       floatOrDefault("AGENT_RUNTIME_AGENT_AUTONOMOUS_MIN_CONFIDENCE", 0.05),
		SoulGlobalFile:                     stringOrDefault("AGENT_RUNTIME_SOUL_GLOBAL_FILE", "/context/SOUL.md"),
		SoulWorkspaceRelPath:               stringOrDefault("AGENT_RUNTIME_SOUL_WORKSPACE_REL_PATH", "context/SOUL.md"),
		SoulContextRelPath:                 stringOrDefault("AGENT_RUNTIME_SOUL_CONTEXT_REL_PATH", "context/agents/{context_id}/SOUL.md"),
		SystemPromptGlobalFile:             stringOrDefault("AGENT_RUNTIME_SYSTEM_PROMPT_GLOBAL_FILE", "/context/SYSTEM_PROMPT.md"),
		SystemPromptWorkspacePath:          stringOrDefault("AGENT_RUNTIME_SYSTEM_PROMPT_WORKSPACE_REL_PATH", "context/SYSTEM_PROMPT.md"),
		SystemPromptContextPath:            stringOrDefault("AGENT_RUNTIME_SYSTEM_PROMPT_CONTEXT_REL_PATH", "context/agents/{context_id}/SYSTEM_PROMPT.md"),
		ReasoningPromptFile:                stringOrDefault("AGENT_RUNTIME_REASONING_PROMPT_FILE", "/context/REASONING.md"),
		SkillsGlobalRoot:                   stringOrDefault("AGENT_RUNTIME_SKILLS_GLOBAL_ROOT", "/context/skills"),
		PublicHost:                         stringOrDefault("PUBLIC_HOST", "localhost"),
		AdminHost:                          stringOrDefault("ADMIN_HOST", "admin.localhost"),
		AdminAPIURL:                        stringOrDefault("AGENT_RUNTIME_ADMIN_API_URL", "https://admin.localhost"),
		AdminTLSSkipVerify:                 boolOrDefault("AGENT_RUNTIME_ADMIN_TLS_SKIP_VERIFY", true),
		AdminTLSCAFile:                     strings.TrimSpace(os.Getenv("AGENT_RUNTIME_ADMIN_TLS_CA_FILE")),
		AdminTLSCertFile:                   strings.TrimSpace(os.Getenv("AGENT_RUNTIME_ADMIN_TLS_CERT_FILE")),
		AdminTLSKeyFile:                    strings.TrimSpace(os.Getenv("AGENT_RUNTIME_ADMIN_TLS_KEY_FILE")),
		TUIApproverUserID:                  stringOrDefault("AGENT_RUNTIME_TUI_APPROVER_USER_ID", "tui-admin"),
		TUIApprovalRole:                    stringOrDefault("AGENT_RUNTIME_TUI_APPROVAL_ROLE", "admin"),
	}
}

func stringOrDefault(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func intOrDefault(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return fallback
	}
	return parsed
}

func boolOrDefault(name string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func notificationPolicyOrDefault(name, fallback string) string {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	switch value {
	case "both", "admin", "origin":
		return value
	default:
		switch strings.ToLower(strings.TrimSpace(fallback)) {
		case "both", "admin", "origin", "":
			return strings.ToLower(strings.TrimSpace(fallback))
		default:
			return "both"
		}
	}
}

func floatOrDefault(name string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}
