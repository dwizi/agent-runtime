package config

import (
	"path/filepath"
	"testing"
)

func TestFromEnvDefaults(t *testing.T) {
	t.Setenv("SPINNER_DATA_DIR", "")
	t.Setenv("SPINNER_WORKSPACE_ROOT", "")
	t.Setenv("SPINNER_DB_PATH", "")
	t.Setenv("SPINNER_DEFAULT_CONCURRENCY", "")
	t.Setenv("SPINNER_QMD_BINARY", "")
	t.Setenv("SPINNER_QMD_SIDECAR_URL", "")
	t.Setenv("SPINNER_QMD_SIDECAR_ADDR", "")
	t.Setenv("SPINNER_QMD_INDEX", "")
	t.Setenv("SPINNER_QMD_COLLECTION", "")
	t.Setenv("SPINNER_QMD_SHARED_MODELS_DIR", "")
	t.Setenv("SPINNER_QMD_EMBED_EXCLUDE_GLOBS", "")
	t.Setenv("SPINNER_QMD_SEARCH_LIMIT", "")
	t.Setenv("SPINNER_QMD_OPEN_MAX_BYTES", "")
	t.Setenv("SPINNER_QMD_DEBOUNCE_SECONDS", "")
	t.Setenv("SPINNER_QMD_INDEX_TIMEOUT_SECONDS", "")
	t.Setenv("SPINNER_QMD_QUERY_TIMEOUT_SECONDS", "")
	t.Setenv("SPINNER_QMD_AUTO_EMBED", "")
	t.Setenv("SPINNER_OBJECTIVE_POLL_SECONDS", "")
	t.Setenv("SPINNER_HEARTBEAT_ENABLED", "")
	t.Setenv("SPINNER_HEARTBEAT_INTERVAL_SECONDS", "")
	t.Setenv("SPINNER_HEARTBEAT_STALE_SECONDS", "")
	t.Setenv("SPINNER_HEARTBEAT_NOTIFY_ADMIN", "")
	t.Setenv("SPINNER_TRIAGE_ENABLED", "")
	t.Setenv("SPINNER_TRIAGE_NOTIFY_ADMIN", "")
	t.Setenv("SPINNER_TASK_NOTIFY_POLICY", "")
	t.Setenv("SPINNER_TASK_NOTIFY_SUCCESS_POLICY", "")
	t.Setenv("SPINNER_TASK_NOTIFY_FAILURE_POLICY", "")
	t.Setenv("SPINNER_COMMAND_SYNC_ENABLED", "")
	t.Setenv("SPINNER_ADMIN_TLS_SKIP_VERIFY", "")
	t.Setenv("SPINNER_DISCORD_API_BASE", "")
	t.Setenv("SPINNER_DISCORD_GATEWAY_URL", "")
	t.Setenv("SPINNER_DISCORD_APPLICATION_ID", "")
	t.Setenv("SPINNER_DISCORD_COMMAND_GUILD_IDS", "")
	t.Setenv("SPINNER_TELEGRAM_API_BASE", "")
	t.Setenv("SPINNER_TELEGRAM_POLL_SECONDS", "")
	t.Setenv("SPINNER_IMAP_HOST", "")
	t.Setenv("SPINNER_IMAP_PORT", "")
	t.Setenv("SPINNER_IMAP_USERNAME", "")
	t.Setenv("SPINNER_IMAP_PASSWORD", "")
	t.Setenv("SPINNER_IMAP_MAILBOX", "")
	t.Setenv("SPINNER_IMAP_POLL_SECONDS", "")
	t.Setenv("SPINNER_IMAP_TLS_SKIP_VERIFY", "")
	t.Setenv("SPINNER_LLM_PROVIDER", "")
	t.Setenv("SPINNER_LLM_BASE_URL", "")
	t.Setenv("SPINNER_LLM_API_KEY", "")
	t.Setenv("SPINNER_LLM_MODEL", "")
	t.Setenv("SPINNER_LLM_TIMEOUT_SECONDS", "")
	t.Setenv("SPINNER_SMTP_HOST", "")
	t.Setenv("SPINNER_SMTP_PORT", "")
	t.Setenv("SPINNER_SMTP_USERNAME", "")
	t.Setenv("SPINNER_SMTP_PASSWORD", "")
	t.Setenv("SPINNER_SMTP_FROM", "")
	t.Setenv("SPINNER_SANDBOX_ENABLED", "")
	t.Setenv("SPINNER_SANDBOX_ALLOWED_COMMANDS", "")
	t.Setenv("SPINNER_SANDBOX_RUNNER_COMMAND", "")
	t.Setenv("SPINNER_SANDBOX_RUNNER_ARGS", "")
	t.Setenv("SPINNER_SANDBOX_TIMEOUT_SECONDS", "")
	t.Setenv("SPINNER_LLM_ENABLED", "")
	t.Setenv("SPINNER_LLM_ALLOW_DM", "")
	t.Setenv("SPINNER_LLM_REQUIRE_MENTION_IN_GROUPS", "")
	t.Setenv("SPINNER_LLM_ALLOWED_ROLES", "")
	t.Setenv("SPINNER_LLM_ALLOWED_CONTEXT_IDS", "")
	t.Setenv("SPINNER_LLM_RATE_LIMIT_PER_WINDOW", "")
	t.Setenv("SPINNER_LLM_RATE_LIMIT_WINDOW_SECONDS", "")
	t.Setenv("SPINNER_LLM_ADMIN_SYSTEM_PROMPT", "")
	t.Setenv("SPINNER_LLM_PUBLIC_SYSTEM_PROMPT", "")
	t.Setenv("SPINNER_SOUL_GLOBAL_FILE", "")
	t.Setenv("SPINNER_SOUL_WORKSPACE_REL_PATH", "")
	t.Setenv("SPINNER_SOUL_CONTEXT_REL_PATH", "")
	t.Setenv("SPINNER_SYSTEM_PROMPT_GLOBAL_FILE", "")
	t.Setenv("SPINNER_SYSTEM_PROMPT_WORKSPACE_REL_PATH", "")
	t.Setenv("SPINNER_SYSTEM_PROMPT_CONTEXT_REL_PATH", "")
	t.Setenv("SPINNER_SKILLS_GLOBAL_ROOT", "")

	cfg := FromEnv()
	if cfg.DataDir != "/data" {
		t.Fatalf("expected default data dir /data, got %s", cfg.DataDir)
	}
	if cfg.WorkspaceRoot != filepath.Join("/data", "workspaces") {
		t.Fatalf("unexpected default workspace root: %s", cfg.WorkspaceRoot)
	}
	if cfg.DBPath != filepath.Join("/data", "spinner", "meta.sqlite") {
		t.Fatalf("unexpected default db path: %s", cfg.DBPath)
	}
	if cfg.DefaultConcurrency != 5 {
		t.Fatalf("expected default concurrency 5, got %d", cfg.DefaultConcurrency)
	}
	if cfg.QMDBinary != "qmd" {
		t.Fatalf("expected default qmd binary, got %s", cfg.QMDBinary)
	}
	if cfg.QMDSidecarURL != "" {
		t.Fatalf("expected default qmd sidecar url empty, got %s", cfg.QMDSidecarURL)
	}
	if cfg.QMDSidecarAddr != ":8091" {
		t.Fatalf("expected default qmd sidecar addr :8091, got %s", cfg.QMDSidecarAddr)
	}
	if cfg.QMDIndexName != "spinner" {
		t.Fatalf("expected default qmd index name, got %s", cfg.QMDIndexName)
	}
	if cfg.QMDCollectionName != "workspace" {
		t.Fatalf("expected default qmd collection name, got %s", cfg.QMDCollectionName)
	}
	if cfg.QMDSharedModelsDir != filepath.Join("/data", "qmd-models") {
		t.Fatalf("expected default qmd shared models dir /data/qmd-models, got %s", cfg.QMDSharedModelsDir)
	}
	if cfg.QMDEmbedExcludeGlobsCSV != "" {
		t.Fatalf("expected default qmd embed exclude globs to be empty, got %s", cfg.QMDEmbedExcludeGlobsCSV)
	}
	if cfg.QMDSearchLimit != 5 {
		t.Fatalf("expected default qmd search limit 5, got %d", cfg.QMDSearchLimit)
	}
	if cfg.QMDOpenMaxBytes != 1600 {
		t.Fatalf("expected default qmd open max bytes 1600, got %d", cfg.QMDOpenMaxBytes)
	}
	if cfg.QMDDebounceSeconds != 3 {
		t.Fatalf("expected default qmd debounce seconds 3, got %d", cfg.QMDDebounceSeconds)
	}
	if cfg.QMDIndexTimeoutSec != 180 {
		t.Fatalf("expected default qmd index timeout 180, got %d", cfg.QMDIndexTimeoutSec)
	}
	if cfg.QMDQueryTimeoutSec != 30 {
		t.Fatalf("expected default qmd query timeout 30, got %d", cfg.QMDQueryTimeoutSec)
	}
	if !cfg.QMDAutoEmbed {
		t.Fatal("expected qmd auto embed to default to true")
	}
	if cfg.ObjectivePollSec != 15 {
		t.Fatalf("expected default objective poll seconds 15, got %d", cfg.ObjectivePollSec)
	}
	if !cfg.HeartbeatEnabled {
		t.Fatal("expected heartbeat enabled by default")
	}
	if cfg.HeartbeatIntervalSec != 30 {
		t.Fatalf("expected default heartbeat interval 30, got %d", cfg.HeartbeatIntervalSec)
	}
	if cfg.HeartbeatStaleSec != 120 {
		t.Fatalf("expected default heartbeat stale seconds 120, got %d", cfg.HeartbeatStaleSec)
	}
	if !cfg.HeartbeatNotifyAdmin {
		t.Fatal("expected heartbeat admin notifications enabled by default")
	}
	if !cfg.TriageEnabled {
		t.Fatal("expected triage enabled by default")
	}
	if !cfg.TriageNotifyAdmin {
		t.Fatal("expected triage admin notifications enabled by default")
	}
	if cfg.TaskNotifyPolicy != "both" {
		t.Fatalf("expected default task notify policy both, got %s", cfg.TaskNotifyPolicy)
	}
	if cfg.TaskNotifySuccessPolicy != "" {
		t.Fatalf("expected default task notify success policy empty, got %s", cfg.TaskNotifySuccessPolicy)
	}
	if cfg.TaskNotifyFailurePolicy != "" {
		t.Fatalf("expected default task notify failure policy empty, got %s", cfg.TaskNotifyFailurePolicy)
	}
	if !cfg.CommandSyncEnabled {
		t.Fatal("expected command sync enabled by default")
	}
	if cfg.DiscordAPI != "https://discord.com/api/v10" {
		t.Fatalf("expected default discord api base, got %s", cfg.DiscordAPI)
	}
	if cfg.DiscordWSURL != "wss://gateway.discord.gg/?v=10&encoding=json" {
		t.Fatalf("expected default discord gateway url, got %s", cfg.DiscordWSURL)
	}
	if cfg.DiscordApplicationID != "" {
		t.Fatalf("expected default discord application id empty, got %s", cfg.DiscordApplicationID)
	}
	if cfg.DiscordCommandGuildIDsCSV != "" {
		t.Fatalf("expected default discord command guild ids empty, got %s", cfg.DiscordCommandGuildIDsCSV)
	}
	if cfg.TelegramAPI != "https://api.telegram.org" {
		t.Fatalf("expected default telegram api base, got %s", cfg.TelegramAPI)
	}
	if cfg.TelegramPoll != 25 {
		t.Fatalf("expected default telegram poll seconds 25, got %d", cfg.TelegramPoll)
	}
	if cfg.IMAPHost != "" {
		t.Fatalf("expected default imap host empty, got %s", cfg.IMAPHost)
	}
	if cfg.IMAPPort != 993 {
		t.Fatalf("expected default imap port 993, got %d", cfg.IMAPPort)
	}
	if cfg.IMAPUsername != "" {
		t.Fatalf("expected default imap username empty, got %s", cfg.IMAPUsername)
	}
	if cfg.IMAPPassword != "" {
		t.Fatal("expected default imap password empty")
	}
	if cfg.IMAPMailbox != "INBOX" {
		t.Fatalf("expected default imap mailbox INBOX, got %s", cfg.IMAPMailbox)
	}
	if cfg.IMAPPollSeconds != 60 {
		t.Fatalf("expected default imap poll seconds 60, got %d", cfg.IMAPPollSeconds)
	}
	if cfg.IMAPTLSSkipVerify {
		t.Fatal("expected default imap tls skip verify false")
	}
	if cfg.LLMProvider != "openai" {
		t.Fatalf("expected default llm provider openai, got %s", cfg.LLMProvider)
	}
	if cfg.LLMBaseURL != "https://api.openai.com/v1" {
		t.Fatalf("expected default llm base url https://api.openai.com/v1, got %s", cfg.LLMBaseURL)
	}
	if cfg.LLMAPIKey != "" {
		t.Fatalf("expected default llm api key empty, got %s", cfg.LLMAPIKey)
	}
	if cfg.LLMModel != "gpt-4o" {
		t.Fatalf("expected default llm model gpt-4o, got %s", cfg.LLMModel)
	}
	if cfg.LLMTimeoutSec != 60 {
		t.Fatalf("expected default llm timeout 60, got %d", cfg.LLMTimeoutSec)
	}
	if cfg.SMTPHost != "" {
		t.Fatalf("expected default smtp host empty, got %s", cfg.SMTPHost)
	}
	if cfg.SMTPPort != 587 {
		t.Fatalf("expected default smtp port 587, got %d", cfg.SMTPPort)
	}
	if cfg.SMTPUsername != "" {
		t.Fatalf("expected default smtp username empty, got %s", cfg.SMTPUsername)
	}
	if cfg.SMTPPassword != "" {
		t.Fatal("expected default smtp password empty")
	}
	if cfg.SMTPFrom != "" {
		t.Fatalf("expected default smtp from empty, got %s", cfg.SMTPFrom)
	}
	if !cfg.SandboxEnabled {
		t.Fatal("expected sandbox enabled by default")
	}
	if cfg.SandboxAllowedCommandsCSV == "" {
		t.Fatal("expected default sandbox allowlist")
	}
	if cfg.SandboxRunnerCommand != "" {
		t.Fatalf("expected default sandbox runner command empty, got %s", cfg.SandboxRunnerCommand)
	}
	if cfg.SandboxRunnerArgs != "" {
		t.Fatalf("expected default sandbox runner args empty, got %s", cfg.SandboxRunnerArgs)
	}
	if cfg.SandboxTimeoutSec != 20 {
		t.Fatalf("expected default sandbox timeout 20, got %d", cfg.SandboxTimeoutSec)
	}
	if !cfg.LLMEnabled {
		t.Fatal("expected llm enabled by default")
	}
	if !cfg.LLMAllowDM {
		t.Fatal("expected llm dm enabled by default")
	}
	if !cfg.LLMRequireMentionInGroups {
		t.Fatal("expected llm mention requirement enabled by default")
	}
	if cfg.LLMRateLimitPerWindow != 8 {
		t.Fatalf("expected default llm rate limit 8, got %d", cfg.LLMRateLimitPerWindow)
	}
	if cfg.LLMRateLimitWindowSec != 60 {
		t.Fatalf("expected default llm rate limit window 60, got %d", cfg.LLMRateLimitWindowSec)
	}
	if cfg.LLMAdminSystemPrompt == "" {
		t.Fatal("expected default admin system prompt")
	}
	if cfg.LLMPublicSystemPrompt == "" {
		t.Fatal("expected default public system prompt")
	}
	if cfg.SoulGlobalFile != "/context/SOUL.md" {
		t.Fatalf("expected default soul global file /context/SOUL.md, got %s", cfg.SoulGlobalFile)
	}
	if cfg.SoulWorkspaceRelPath != "context/SOUL.md" {
		t.Fatalf("expected default soul workspace rel path context/SOUL.md, got %s", cfg.SoulWorkspaceRelPath)
	}
	if cfg.SoulContextRelPath != "context/agents/{context_id}/SOUL.md" {
		t.Fatalf("expected default soul context rel path, got %s", cfg.SoulContextRelPath)
	}
	if cfg.SystemPromptGlobalFile != "/context/SYSTEM_PROMPT.md" {
		t.Fatalf("expected default system prompt global file /context/SYSTEM_PROMPT.md, got %s", cfg.SystemPromptGlobalFile)
	}
	if cfg.SystemPromptWorkspacePath != "context/SYSTEM_PROMPT.md" {
		t.Fatalf("expected default workspace system prompt path context/SYSTEM_PROMPT.md, got %s", cfg.SystemPromptWorkspacePath)
	}
	if cfg.SystemPromptContextPath != "context/agents/{context_id}/SYSTEM_PROMPT.md" {
		t.Fatalf("expected default context system prompt path, got %s", cfg.SystemPromptContextPath)
	}
	if cfg.SkillsGlobalRoot != "/context/skills" {
		t.Fatalf("expected default skills global root /context/skills, got %s", cfg.SkillsGlobalRoot)
	}
	if !cfg.AdminTLSSkipVerify {
		t.Fatal("expected admin tls skip verify to default to true")
	}
	if cfg.AdminAPIURL != "https://admin.localhost" {
		t.Fatalf("expected default admin api url, got %s", cfg.AdminAPIURL)
	}
}

func TestFromEnvOverrides(t *testing.T) {
	t.Setenv("SPINNER_DATA_DIR", "/var/spinner")
	t.Setenv("SPINNER_WORKSPACE_ROOT", "/var/spinner/ws")
	t.Setenv("SPINNER_DB_PATH", "/var/spinner/db.sqlite")
	t.Setenv("SPINNER_DEFAULT_CONCURRENCY", "9")
	t.Setenv("SPINNER_QMD_BINARY", "/usr/local/bin/qmd")
	t.Setenv("SPINNER_QMD_SIDECAR_URL", "http://spinner-qmd:8091")
	t.Setenv("SPINNER_QMD_SIDECAR_ADDR", ":19091")
	t.Setenv("SPINNER_QMD_INDEX", "community")
	t.Setenv("SPINNER_QMD_COLLECTION", "docs")
	t.Setenv("SPINNER_QMD_SHARED_MODELS_DIR", "/var/spinner/qmd-models")
	t.Setenv("SPINNER_QMD_EMBED_EXCLUDE_GLOBS", "logs/chats/**,tasks/**")
	t.Setenv("SPINNER_QMD_SEARCH_LIMIT", "9")
	t.Setenv("SPINNER_QMD_OPEN_MAX_BYTES", "2400")
	t.Setenv("SPINNER_QMD_DEBOUNCE_SECONDS", "7")
	t.Setenv("SPINNER_QMD_INDEX_TIMEOUT_SECONDS", "420")
	t.Setenv("SPINNER_QMD_QUERY_TIMEOUT_SECONDS", "44")
	t.Setenv("SPINNER_QMD_AUTO_EMBED", "false")
	t.Setenv("SPINNER_OBJECTIVE_POLL_SECONDS", "11")
	t.Setenv("SPINNER_HEARTBEAT_ENABLED", "true")
	t.Setenv("SPINNER_HEARTBEAT_INTERVAL_SECONDS", "20")
	t.Setenv("SPINNER_HEARTBEAT_STALE_SECONDS", "75")
	t.Setenv("SPINNER_HEARTBEAT_NOTIFY_ADMIN", "false")
	t.Setenv("SPINNER_TRIAGE_ENABLED", "false")
	t.Setenv("SPINNER_TRIAGE_NOTIFY_ADMIN", "false")
	t.Setenv("SPINNER_TASK_NOTIFY_POLICY", "admin")
	t.Setenv("SPINNER_TASK_NOTIFY_SUCCESS_POLICY", "origin")
	t.Setenv("SPINNER_TASK_NOTIFY_FAILURE_POLICY", "admin")
	t.Setenv("SPINNER_COMMAND_SYNC_ENABLED", "false")
	t.Setenv("SPINNER_DISCORD_API_BASE", "https://discord.test/api/v10")
	t.Setenv("SPINNER_DISCORD_GATEWAY_URL", "wss://discord.test/gateway")
	t.Setenv("SPINNER_DISCORD_APPLICATION_ID", "1234567890")
	t.Setenv("SPINNER_DISCORD_COMMAND_GUILD_IDS", "111,222")
	t.Setenv("SPINNER_TELEGRAM_API_BASE", "https://telegram.test")
	t.Setenv("SPINNER_TELEGRAM_POLL_SECONDS", "12")
	t.Setenv("SPINNER_IMAP_HOST", "imap.example.com")
	t.Setenv("SPINNER_IMAP_PORT", "1993")
	t.Setenv("SPINNER_IMAP_USERNAME", "inbox@example.com")
	t.Setenv("SPINNER_IMAP_PASSWORD", "imap-secret")
	t.Setenv("SPINNER_IMAP_MAILBOX", "Support")
	t.Setenv("SPINNER_IMAP_POLL_SECONDS", "33")
	t.Setenv("SPINNER_IMAP_TLS_SKIP_VERIFY", "true")
	t.Setenv("SPINNER_LLM_PROVIDER", "anthropic")
	t.Setenv("SPINNER_LLM_BASE_URL", "https://api.anthropic.com")
	t.Setenv("SPINNER_LLM_API_KEY", "anthropic-key")
	t.Setenv("SPINNER_LLM_MODEL", "claude-3.5-sonic")
	t.Setenv("SPINNER_LLM_TIMEOUT_SECONDS", "90")
	t.Setenv("SPINNER_SMTP_HOST", "smtp.example.com")
	t.Setenv("SPINNER_SMTP_PORT", "2525")
	t.Setenv("SPINNER_SMTP_USERNAME", "bot@example.com")
	t.Setenv("SPINNER_SMTP_PASSWORD", "secret")
	t.Setenv("SPINNER_SMTP_FROM", "Spinner Bot <bot@example.com>")
	t.Setenv("SPINNER_SANDBOX_ENABLED", "false")
	t.Setenv("SPINNER_SANDBOX_ALLOWED_COMMANDS", "curl,git,rg")
	t.Setenv("SPINNER_SANDBOX_RUNNER_COMMAND", "just-bash")
	t.Setenv("SPINNER_SANDBOX_RUNNER_ARGS", "--network=off --readonly")
	t.Setenv("SPINNER_SANDBOX_TIMEOUT_SECONDS", "45")
	t.Setenv("SPINNER_LLM_ENABLED", "true")
	t.Setenv("SPINNER_LLM_ALLOW_DM", "false")
	t.Setenv("SPINNER_LLM_REQUIRE_MENTION_IN_GROUPS", "false")
	t.Setenv("SPINNER_LLM_ALLOWED_ROLES", "admin,overlord")
	t.Setenv("SPINNER_LLM_ALLOWED_CONTEXT_IDS", "ctx-1,ctx-2")
	t.Setenv("SPINNER_LLM_RATE_LIMIT_PER_WINDOW", "3")
	t.Setenv("SPINNER_LLM_RATE_LIMIT_WINDOW_SECONDS", "120")
	t.Setenv("SPINNER_LLM_ADMIN_SYSTEM_PROMPT", "admin prompt")
	t.Setenv("SPINNER_LLM_PUBLIC_SYSTEM_PROMPT", "public prompt")
	t.Setenv("SPINNER_SOUL_GLOBAL_FILE", "/context/GLOBAL_SOUL.md")
	t.Setenv("SPINNER_SOUL_WORKSPACE_REL_PATH", "persona/SOUL.md")
	t.Setenv("SPINNER_SOUL_CONTEXT_REL_PATH", "persona/agents/{context_id}.md")
	t.Setenv("SPINNER_SYSTEM_PROMPT_GLOBAL_FILE", "/context/GLOBAL_SYSTEM_PROMPT.md")
	t.Setenv("SPINNER_SYSTEM_PROMPT_WORKSPACE_REL_PATH", "persona/SYSTEM_PROMPT.md")
	t.Setenv("SPINNER_SYSTEM_PROMPT_CONTEXT_REL_PATH", "persona/agents/{context_id}/SYSTEM_PROMPT.md")
	t.Setenv("SPINNER_SKILLS_GLOBAL_ROOT", "/context/skill-packs")
	t.Setenv("PUBLIC_HOST", "chat.example.com")
	t.Setenv("ADMIN_HOST", "admin.example.com")
	t.Setenv("SPINNER_ADMIN_API_URL", "https://admin.example.com")
	t.Setenv("SPINNER_ADMIN_TLS_SKIP_VERIFY", "false")
	t.Setenv("SPINNER_TUI_APPROVER_USER_ID", "overlord-1")
	t.Setenv("SPINNER_TUI_APPROVAL_ROLE", "overlord")

	cfg := FromEnv()
	if cfg.DataDir != "/var/spinner" {
		t.Fatalf("expected overridden data dir, got %s", cfg.DataDir)
	}
	if cfg.WorkspaceRoot != "/var/spinner/ws" {
		t.Fatalf("expected overridden workspace root, got %s", cfg.WorkspaceRoot)
	}
	if cfg.DBPath != "/var/spinner/db.sqlite" {
		t.Fatalf("expected overridden db path, got %s", cfg.DBPath)
	}
	if cfg.DefaultConcurrency != 9 {
		t.Fatalf("expected overridden concurrency, got %d", cfg.DefaultConcurrency)
	}
	if cfg.QMDBinary != "/usr/local/bin/qmd" {
		t.Fatalf("expected overridden qmd binary, got %s", cfg.QMDBinary)
	}
	if cfg.QMDSidecarURL != "http://spinner-qmd:8091" {
		t.Fatalf("expected overridden qmd sidecar url, got %s", cfg.QMDSidecarURL)
	}
	if cfg.QMDSidecarAddr != ":19091" {
		t.Fatalf("expected overridden qmd sidecar addr, got %s", cfg.QMDSidecarAddr)
	}
	if cfg.QMDIndexName != "community" {
		t.Fatalf("expected overridden qmd index name, got %s", cfg.QMDIndexName)
	}
	if cfg.QMDCollectionName != "docs" {
		t.Fatalf("expected overridden qmd collection name, got %s", cfg.QMDCollectionName)
	}
	if cfg.QMDSharedModelsDir != "/var/spinner/qmd-models" {
		t.Fatalf("expected overridden qmd shared models dir, got %s", cfg.QMDSharedModelsDir)
	}
	if cfg.QMDEmbedExcludeGlobsCSV != "logs/chats/**,tasks/**" {
		t.Fatalf("expected overridden qmd embed exclude globs, got %s", cfg.QMDEmbedExcludeGlobsCSV)
	}
	if cfg.QMDSearchLimit != 9 {
		t.Fatalf("expected overridden qmd search limit, got %d", cfg.QMDSearchLimit)
	}
	if cfg.QMDOpenMaxBytes != 2400 {
		t.Fatalf("expected overridden qmd open max bytes, got %d", cfg.QMDOpenMaxBytes)
	}
	if cfg.QMDDebounceSeconds != 7 {
		t.Fatalf("expected overridden qmd debounce seconds, got %d", cfg.QMDDebounceSeconds)
	}
	if cfg.QMDIndexTimeoutSec != 420 {
		t.Fatalf("expected overridden qmd index timeout, got %d", cfg.QMDIndexTimeoutSec)
	}
	if cfg.QMDQueryTimeoutSec != 44 {
		t.Fatalf("expected overridden qmd query timeout, got %d", cfg.QMDQueryTimeoutSec)
	}
	if cfg.QMDAutoEmbed {
		t.Fatal("expected qmd auto embed false")
	}
	if cfg.ObjectivePollSec != 11 {
		t.Fatalf("expected overridden objective poll seconds, got %d", cfg.ObjectivePollSec)
	}
	if !cfg.HeartbeatEnabled {
		t.Fatal("expected overridden heartbeat enabled true")
	}
	if cfg.HeartbeatIntervalSec != 20 {
		t.Fatalf("expected overridden heartbeat interval, got %d", cfg.HeartbeatIntervalSec)
	}
	if cfg.HeartbeatStaleSec != 75 {
		t.Fatalf("expected overridden heartbeat stale seconds, got %d", cfg.HeartbeatStaleSec)
	}
	if cfg.HeartbeatNotifyAdmin {
		t.Fatal("expected overridden heartbeat notify admin false")
	}
	if cfg.TriageEnabled {
		t.Fatal("expected triage enabled false")
	}
	if cfg.TriageNotifyAdmin {
		t.Fatal("expected triage notify admin false")
	}
	if cfg.TaskNotifyPolicy != "admin" {
		t.Fatalf("expected overridden task notify policy admin, got %s", cfg.TaskNotifyPolicy)
	}
	if cfg.TaskNotifySuccessPolicy != "origin" {
		t.Fatalf("expected overridden task notify success policy origin, got %s", cfg.TaskNotifySuccessPolicy)
	}
	if cfg.TaskNotifyFailurePolicy != "admin" {
		t.Fatalf("expected overridden task notify failure policy admin, got %s", cfg.TaskNotifyFailurePolicy)
	}
	if cfg.CommandSyncEnabled {
		t.Fatal("expected command sync enabled false")
	}
	if cfg.DiscordAPI != "https://discord.test/api/v10" {
		t.Fatalf("expected overridden discord api base, got %s", cfg.DiscordAPI)
	}
	if cfg.DiscordWSURL != "wss://discord.test/gateway" {
		t.Fatalf("expected overridden discord gateway url, got %s", cfg.DiscordWSURL)
	}
	if cfg.DiscordApplicationID != "1234567890" {
		t.Fatalf("expected overridden discord application id, got %s", cfg.DiscordApplicationID)
	}
	if cfg.DiscordCommandGuildIDsCSV != "111,222" {
		t.Fatalf("expected overridden discord command guild ids, got %s", cfg.DiscordCommandGuildIDsCSV)
	}
	if cfg.TelegramAPI != "https://telegram.test" {
		t.Fatalf("expected overridden telegram api base, got %s", cfg.TelegramAPI)
	}
	if cfg.TelegramPoll != 12 {
		t.Fatalf("expected overridden telegram poll seconds, got %d", cfg.TelegramPoll)
	}
	if cfg.IMAPHost != "imap.example.com" {
		t.Fatalf("expected overridden imap host, got %s", cfg.IMAPHost)
	}
	if cfg.IMAPPort != 1993 {
		t.Fatalf("expected overridden imap port, got %d", cfg.IMAPPort)
	}
	if cfg.IMAPUsername != "inbox@example.com" {
		t.Fatalf("expected overridden imap username, got %s", cfg.IMAPUsername)
	}
	if cfg.IMAPPassword != "imap-secret" {
		t.Fatalf("expected overridden imap password, got %s", cfg.IMAPPassword)
	}
	if cfg.IMAPMailbox != "Support" {
		t.Fatalf("expected overridden imap mailbox, got %s", cfg.IMAPMailbox)
	}
	if cfg.IMAPPollSeconds != 33 {
		t.Fatalf("expected overridden imap poll seconds, got %d", cfg.IMAPPollSeconds)
	}
	if !cfg.IMAPTLSSkipVerify {
		t.Fatal("expected overridden imap tls skip verify true")
	}
	if cfg.LLMProvider != "anthropic" {
		t.Fatalf("expected overridden llm provider anthropic, got %s", cfg.LLMProvider)
	}
	if cfg.LLMBaseURL != "https://api.anthropic.com" {
		t.Fatalf("expected overridden llm base url, got %s", cfg.LLMBaseURL)
	}
	if cfg.LLMAPIKey != "anthropic-key" {
		t.Fatalf("expected overridden llm api key, got %s", cfg.LLMAPIKey)
	}
	if cfg.LLMModel != "claude-3.5-sonic" {
		t.Fatalf("expected overridden llm model, got %s", cfg.LLMModel)
	}
	if cfg.LLMTimeoutSec != 90 {
		t.Fatalf("expected overridden llm timeout, got %d", cfg.LLMTimeoutSec)
	}
	if cfg.SMTPHost != "smtp.example.com" {
		t.Fatalf("expected overridden smtp host, got %s", cfg.SMTPHost)
	}
	if cfg.SMTPPort != 2525 {
		t.Fatalf("expected overridden smtp port, got %d", cfg.SMTPPort)
	}
	if cfg.SMTPUsername != "bot@example.com" {
		t.Fatalf("expected overridden smtp username, got %s", cfg.SMTPUsername)
	}
	if cfg.SMTPPassword != "secret" {
		t.Fatalf("expected overridden smtp password, got %s", cfg.SMTPPassword)
	}
	if cfg.SMTPFrom != "Spinner Bot <bot@example.com>" {
		t.Fatalf("expected overridden smtp from, got %s", cfg.SMTPFrom)
	}
	if cfg.SandboxEnabled {
		t.Fatal("expected sandbox enabled false")
	}
	if cfg.SandboxAllowedCommandsCSV != "curl,git,rg" {
		t.Fatalf("expected overridden sandbox commands, got %s", cfg.SandboxAllowedCommandsCSV)
	}
	if cfg.SandboxRunnerCommand != "just-bash" {
		t.Fatalf("expected overridden sandbox runner command, got %s", cfg.SandboxRunnerCommand)
	}
	if cfg.SandboxRunnerArgs != "--network=off --readonly" {
		t.Fatalf("expected overridden sandbox runner args, got %s", cfg.SandboxRunnerArgs)
	}
	if cfg.SandboxTimeoutSec != 45 {
		t.Fatalf("expected overridden sandbox timeout, got %d", cfg.SandboxTimeoutSec)
	}
	if !cfg.LLMEnabled {
		t.Fatal("expected llm enabled true")
	}
	if cfg.LLMAllowDM {
		t.Fatal("expected llm allow dm false")
	}
	if cfg.LLMRequireMentionInGroups {
		t.Fatal("expected llm mention requirement false")
	}
	if cfg.LLMAllowedRolesCSV != "admin,overlord" {
		t.Fatalf("expected overridden llm roles, got %s", cfg.LLMAllowedRolesCSV)
	}
	if cfg.LLMAllowedContextIDsCSV != "ctx-1,ctx-2" {
		t.Fatalf("expected overridden llm contexts, got %s", cfg.LLMAllowedContextIDsCSV)
	}
	if cfg.LLMRateLimitPerWindow != 3 {
		t.Fatalf("expected overridden llm rate limit, got %d", cfg.LLMRateLimitPerWindow)
	}
	if cfg.LLMRateLimitWindowSec != 120 {
		t.Fatalf("expected overridden llm rate limit window, got %d", cfg.LLMRateLimitWindowSec)
	}
	if cfg.LLMAdminSystemPrompt != "admin prompt" {
		t.Fatalf("expected overridden admin system prompt, got %s", cfg.LLMAdminSystemPrompt)
	}
	if cfg.LLMPublicSystemPrompt != "public prompt" {
		t.Fatalf("expected overridden public system prompt, got %s", cfg.LLMPublicSystemPrompt)
	}
	if cfg.SoulGlobalFile != "/context/GLOBAL_SOUL.md" {
		t.Fatalf("expected overridden soul global file, got %s", cfg.SoulGlobalFile)
	}
	if cfg.SoulWorkspaceRelPath != "persona/SOUL.md" {
		t.Fatalf("expected overridden soul workspace rel path, got %s", cfg.SoulWorkspaceRelPath)
	}
	if cfg.SoulContextRelPath != "persona/agents/{context_id}.md" {
		t.Fatalf("expected overridden soul context rel path, got %s", cfg.SoulContextRelPath)
	}
	if cfg.SystemPromptGlobalFile != "/context/GLOBAL_SYSTEM_PROMPT.md" {
		t.Fatalf("expected overridden global system prompt file, got %s", cfg.SystemPromptGlobalFile)
	}
	if cfg.SystemPromptWorkspacePath != "persona/SYSTEM_PROMPT.md" {
		t.Fatalf("expected overridden workspace system prompt path, got %s", cfg.SystemPromptWorkspacePath)
	}
	if cfg.SystemPromptContextPath != "persona/agents/{context_id}/SYSTEM_PROMPT.md" {
		t.Fatalf("expected overridden context system prompt path, got %s", cfg.SystemPromptContextPath)
	}
	if cfg.SkillsGlobalRoot != "/context/skill-packs" {
		t.Fatalf("expected overridden skills global root, got %s", cfg.SkillsGlobalRoot)
	}
	if cfg.PublicHost != "chat.example.com" {
		t.Fatalf("expected overridden public host, got %s", cfg.PublicHost)
	}
	if cfg.AdminHost != "admin.example.com" {
		t.Fatalf("expected overridden admin host, got %s", cfg.AdminHost)
	}
	if cfg.AdminAPIURL != "https://admin.example.com" {
		t.Fatalf("expected overridden admin api url, got %s", cfg.AdminAPIURL)
	}
	if cfg.AdminTLSSkipVerify {
		t.Fatal("expected admin tls skip verify false")
	}
	if cfg.TUIApproverUserID != "overlord-1" {
		t.Fatalf("expected overridden approver user id, got %s", cfg.TUIApproverUserID)
	}
	if cfg.TUIApprovalRole != "overlord" {
		t.Fatalf("expected overridden approver role, got %s", cfg.TUIApprovalRole)
	}
}
