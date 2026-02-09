package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Environment        string
	HTTPAddr           string
	DataDir            string
	DBPath             string
	WorkspaceRoot      string
	DefaultConcurrency int
	QMDBinary          string
	QMDIndexName       string
	QMDCollectionName  string
	QMDSearchLimit     int
	QMDOpenMaxBytes    int
	QMDDebounceSeconds int
	QMDIndexTimeoutSec int
	QMDQueryTimeoutSec int
	QMDAutoEmbed       bool
	ObjectivePollSec   int

	DiscordToken              string
	DiscordAPI                string
	DiscordWSURL              string
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
	ZAIAPIKey                 string
	ZAIBaseURL                string
	ZAIModel                  string
	ZAITimeoutSec             int
	SMTPHost                  string
	SMTPPort                  int
	SMTPUsername              string
	SMTPPassword              string
	SMTPFrom                  string
	SandboxEnabled            bool
	SandboxAllowedCommandsCSV string
	SandboxRunnerCommand      string
	SandboxRunnerArgs         string
	SandboxTimeoutSec         int
	LLMEnabled                bool
	LLMAllowDM                bool
	LLMRequireMentionInGroups bool
	LLMAllowedRolesCSV        string
	LLMAllowedContextIDsCSV   string
	LLMRateLimitPerWindow     int
	LLMRateLimitWindowSec     int
	LLMAdminSystemPrompt      string
	LLMPublicSystemPrompt     string

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
	dataDir := stringOrDefault("SPINNER_DATA_DIR", "/data")
	workspaceRoot := stringOrDefault("SPINNER_WORKSPACE_ROOT", filepath.Join(dataDir, "workspaces"))
	dbPath := stringOrDefault("SPINNER_DB_PATH", filepath.Join(dataDir, "spinner", "meta.sqlite"))

	return Config{
		Environment:               stringOrDefault("SPINNER_ENV", "development"),
		HTTPAddr:                  stringOrDefault("SPINNER_HTTP_ADDR", ":8080"),
		DataDir:                   dataDir,
		DBPath:                    dbPath,
		WorkspaceRoot:             workspaceRoot,
		DefaultConcurrency:        intOrDefault("SPINNER_DEFAULT_CONCURRENCY", 5),
		QMDBinary:                 stringOrDefault("SPINNER_QMD_BINARY", "qmd"),
		QMDIndexName:              stringOrDefault("SPINNER_QMD_INDEX", "spinner"),
		QMDCollectionName:         stringOrDefault("SPINNER_QMD_COLLECTION", "workspace"),
		QMDSearchLimit:            intOrDefault("SPINNER_QMD_SEARCH_LIMIT", 5),
		QMDOpenMaxBytes:           intOrDefault("SPINNER_QMD_OPEN_MAX_BYTES", 1600),
		QMDDebounceSeconds:        intOrDefault("SPINNER_QMD_DEBOUNCE_SECONDS", 3),
		QMDIndexTimeoutSec:        intOrDefault("SPINNER_QMD_INDEX_TIMEOUT_SECONDS", 180),
		QMDQueryTimeoutSec:        intOrDefault("SPINNER_QMD_QUERY_TIMEOUT_SECONDS", 30),
		QMDAutoEmbed:              boolOrDefault("SPINNER_QMD_AUTO_EMBED", true),
		ObjectivePollSec:          intOrDefault("SPINNER_OBJECTIVE_POLL_SECONDS", 15),
		DiscordToken:              os.Getenv("SPINNER_DISCORD_TOKEN"),
		DiscordAPI:                stringOrDefault("SPINNER_DISCORD_API_BASE", "https://discord.com/api/v10"),
		DiscordWSURL:              stringOrDefault("SPINNER_DISCORD_GATEWAY_URL", "wss://gateway.discord.gg/?v=10&encoding=json"),
		TelegramToken:             os.Getenv("SPINNER_TELEGRAM_TOKEN"),
		TelegramAPI:               stringOrDefault("SPINNER_TELEGRAM_API_BASE", "https://api.telegram.org"),
		TelegramPoll:              intOrDefault("SPINNER_TELEGRAM_POLL_SECONDS", 25),
		IMAPHost:                  strings.TrimSpace(os.Getenv("SPINNER_IMAP_HOST")),
		IMAPPort:                  intOrDefault("SPINNER_IMAP_PORT", 993),
		IMAPUsername:              strings.TrimSpace(os.Getenv("SPINNER_IMAP_USERNAME")),
		IMAPPassword:              os.Getenv("SPINNER_IMAP_PASSWORD"),
		IMAPMailbox:               stringOrDefault("SPINNER_IMAP_MAILBOX", "INBOX"),
		IMAPPollSeconds:           intOrDefault("SPINNER_IMAP_POLL_SECONDS", 60),
		IMAPTLSSkipVerify:         boolOrDefault("SPINNER_IMAP_TLS_SKIP_VERIFY", false),
		ZAIAPIKey:                 os.Getenv("SPINNER_ZAI_API_KEY"),
		ZAIBaseURL:                stringOrDefault("SPINNER_ZAI_BASE_URL", "https://api.z.ai/api/paas/v4"),
		ZAIModel:                  stringOrDefault("SPINNER_ZAI_MODEL", "glm-4.7-flash"),
		ZAITimeoutSec:             intOrDefault("SPINNER_ZAI_TIMEOUT_SECONDS", 45),
		SMTPHost:                  strings.TrimSpace(os.Getenv("SPINNER_SMTP_HOST")),
		SMTPPort:                  intOrDefault("SPINNER_SMTP_PORT", 587),
		SMTPUsername:              strings.TrimSpace(os.Getenv("SPINNER_SMTP_USERNAME")),
		SMTPPassword:              os.Getenv("SPINNER_SMTP_PASSWORD"),
		SMTPFrom:                  strings.TrimSpace(os.Getenv("SPINNER_SMTP_FROM")),
		SandboxEnabled:            boolOrDefault("SPINNER_SANDBOX_ENABLED", true),
		SandboxAllowedCommandsCSV: stringOrDefault("SPINNER_SANDBOX_ALLOWED_COMMANDS", "echo,cat,ls,curl,grep,head,tail"),
		SandboxRunnerCommand:      strings.TrimSpace(os.Getenv("SPINNER_SANDBOX_RUNNER_COMMAND")),
		SandboxRunnerArgs:         strings.TrimSpace(os.Getenv("SPINNER_SANDBOX_RUNNER_ARGS")),
		SandboxTimeoutSec:         intOrDefault("SPINNER_SANDBOX_TIMEOUT_SECONDS", 20),
		LLMEnabled:                boolOrDefault("SPINNER_LLM_ENABLED", true),
		LLMAllowDM:                boolOrDefault("SPINNER_LLM_ALLOW_DM", true),
		LLMRequireMentionInGroups: boolOrDefault("SPINNER_LLM_REQUIRE_MENTION_IN_GROUPS", true),
		LLMAllowedRolesCSV:        strings.TrimSpace(os.Getenv("SPINNER_LLM_ALLOWED_ROLES")),
		LLMAllowedContextIDsCSV:   strings.TrimSpace(os.Getenv("SPINNER_LLM_ALLOWED_CONTEXT_IDS")),
		LLMRateLimitPerWindow:     intOrDefault("SPINNER_LLM_RATE_LIMIT_PER_WINDOW", 8),
		LLMRateLimitWindowSec:     intOrDefault("SPINNER_LLM_RATE_LIMIT_WINDOW_SECONDS", 60),
		LLMAdminSystemPrompt:      stringOrDefault("SPINNER_LLM_ADMIN_SYSTEM_PROMPT", "You are assisting admin operators. Prioritize security, approvals, and operational clarity."),
		LLMPublicSystemPrompt:     stringOrDefault("SPINNER_LLM_PUBLIC_SYSTEM_PROMPT", "You are assisting community members. Be concise, safe, and policy-compliant."),
		PublicHost:                stringOrDefault("PUBLIC_HOST", "localhost"),
		AdminHost:                 stringOrDefault("ADMIN_HOST", "admin.localhost"),
		AdminAPIURL:               stringOrDefault("SPINNER_ADMIN_API_URL", "https://admin.localhost"),
		AdminTLSSkipVerify:        boolOrDefault("SPINNER_ADMIN_TLS_SKIP_VERIFY", true),
		AdminTLSCAFile:            strings.TrimSpace(os.Getenv("SPINNER_ADMIN_TLS_CA_FILE")),
		AdminTLSCertFile:          strings.TrimSpace(os.Getenv("SPINNER_ADMIN_TLS_CERT_FILE")),
		AdminTLSKeyFile:           strings.TrimSpace(os.Getenv("SPINNER_ADMIN_TLS_KEY_FILE")),
		TUIApproverUserID:         stringOrDefault("SPINNER_TUI_APPROVER_USER_ID", "tui-admin"),
		TUIApprovalRole:           stringOrDefault("SPINNER_TUI_APPROVAL_ROLE", "admin"),
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
