package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFromEnvDefaults(t *testing.T) {
	t.Setenv("AGENT_RUNTIME_DATA_DIR", "")
	t.Setenv("AGENT_RUNTIME_WORKSPACE_ROOT", "")
	t.Setenv("AGENT_RUNTIME_DB_PATH", "")
	t.Setenv("AGENT_RUNTIME_DEFAULT_CONCURRENCY", "")
	t.Setenv("AGENT_RUNTIME_QMD_BINARY", "")
	t.Setenv("AGENT_RUNTIME_QMD_SIDECAR_URL", "")
	t.Setenv("AGENT_RUNTIME_QMD_SIDECAR_ADDR", "")
	t.Setenv("AGENT_RUNTIME_QMD_INDEX", "")
	t.Setenv("AGENT_RUNTIME_QMD_COLLECTION", "")
	t.Setenv("AGENT_RUNTIME_QMD_SHARED_MODELS_DIR", "")
	t.Setenv("AGENT_RUNTIME_QMD_EMBED_EXCLUDE_GLOBS", "")
	t.Setenv("AGENT_RUNTIME_QMD_SEARCH_LIMIT", "")
	t.Setenv("AGENT_RUNTIME_QMD_OPEN_MAX_BYTES", "")
	t.Setenv("AGENT_RUNTIME_QMD_DEBOUNCE_SECONDS", "")
	t.Setenv("AGENT_RUNTIME_QMD_INDEX_TIMEOUT_SECONDS", "")
	t.Setenv("AGENT_RUNTIME_QMD_QUERY_TIMEOUT_SECONDS", "")
	t.Setenv("AGENT_RUNTIME_QMD_AUTO_EMBED", "")
	t.Setenv("AGENT_RUNTIME_OBJECTIVE_POLL_SECONDS", "")
	t.Setenv("AGENT_RUNTIME_TASK_RECOVERY_RUNNING_STALE_SECONDS", "")
	t.Setenv("AGENT_RUNTIME_HEARTBEAT_ENABLED", "")
	t.Setenv("AGENT_RUNTIME_HEARTBEAT_INTERVAL_SECONDS", "")
	t.Setenv("AGENT_RUNTIME_HEARTBEAT_STALE_SECONDS", "")
	t.Setenv("AGENT_RUNTIME_HEARTBEAT_NOTIFY_ADMIN", "")
	t.Setenv("AGENT_RUNTIME_TRIAGE_ENABLED", "")
	t.Setenv("AGENT_RUNTIME_TRIAGE_NOTIFY_ADMIN", "")
	t.Setenv("AGENT_RUNTIME_TASK_NOTIFY_POLICY", "")
	t.Setenv("AGENT_RUNTIME_TASK_NOTIFY_SUCCESS_POLICY", "")
	t.Setenv("AGENT_RUNTIME_TASK_NOTIFY_FAILURE_POLICY", "")
	t.Setenv("AGENT_RUNTIME_AGENT_SENSITIVE_APPROVAL_TTL_SECONDS", "")
	t.Setenv("AGENT_RUNTIME_COMMAND_SYNC_ENABLED", "")
	t.Setenv("AGENT_RUNTIME_ADMIN_TLS_SKIP_VERIFY", "")
	t.Setenv("AGENT_RUNTIME_DISCORD_API_BASE", "")
	t.Setenv("AGENT_RUNTIME_DISCORD_GATEWAY_URL", "")
	t.Setenv("AGENT_RUNTIME_DISCORD_APPLICATION_ID", "")
	t.Setenv("AGENT_RUNTIME_DISCORD_COMMAND_GUILD_IDS", "")
	t.Setenv("AGENT_RUNTIME_TELEGRAM_API_BASE", "")
	t.Setenv("AGENT_RUNTIME_TELEGRAM_POLL_SECONDS", "")
	t.Setenv("AGENT_RUNTIME_CODEX_PUBLISH_URL", "")
	t.Setenv("AGENT_RUNTIME_CODEX_PUBLISH_BEARER_TOKEN", "")
	t.Setenv("AGENT_RUNTIME_CODEX_PUBLISH_TIMEOUT_SECONDS", "")
	t.Setenv("AGENT_RUNTIME_IMAP_HOST", "")
	t.Setenv("AGENT_RUNTIME_IMAP_PORT", "")
	t.Setenv("AGENT_RUNTIME_IMAP_USERNAME", "")
	t.Setenv("AGENT_RUNTIME_IMAP_PASSWORD", "")
	t.Setenv("AGENT_RUNTIME_IMAP_MAILBOX", "")
	t.Setenv("AGENT_RUNTIME_IMAP_POLL_SECONDS", "")
	t.Setenv("AGENT_RUNTIME_IMAP_TLS_SKIP_VERIFY", "")
	t.Setenv("AGENT_RUNTIME_LLM_PROVIDER", "")
	t.Setenv("AGENT_RUNTIME_LLM_BASE_URL", "")
	t.Setenv("AGENT_RUNTIME_LLM_API_KEY", "")
	t.Setenv("AGENT_RUNTIME_LLM_MODEL", "")
	t.Setenv("AGENT_RUNTIME_LLM_TIMEOUT_SECONDS", "")
	t.Setenv("AGENT_RUNTIME_SMTP_HOST", "")
	t.Setenv("AGENT_RUNTIME_SMTP_PORT", "")
	t.Setenv("AGENT_RUNTIME_SMTP_USERNAME", "")
	t.Setenv("AGENT_RUNTIME_SMTP_PASSWORD", "")
	t.Setenv("AGENT_RUNTIME_SMTP_FROM", "")
	t.Setenv("AGENT_RUNTIME_EXT_PLUGINS_CONFIG", "")
	t.Setenv("AGENT_RUNTIME_EXT_PLUGIN_CACHE_DIR", "")
	t.Setenv("AGENT_RUNTIME_EXT_PLUGIN_WARM_ON_BOOTSTRAP", "")
	t.Setenv("AGENT_RUNTIME_SANDBOX_ENABLED", "")
	t.Setenv("AGENT_RUNTIME_SANDBOX_ALLOWED_COMMANDS", "")
	t.Setenv("AGENT_RUNTIME_SANDBOX_RUNNER_COMMAND", "")
	t.Setenv("AGENT_RUNTIME_SANDBOX_RUNNER_ARGS", "")
	t.Setenv("AGENT_RUNTIME_SANDBOX_TIMEOUT_SECONDS", "")
	t.Setenv("AGENT_RUNTIME_LLM_ENABLED", "")
	t.Setenv("AGENT_RUNTIME_LLM_ALLOW_DM", "")
	t.Setenv("AGENT_RUNTIME_LLM_REQUIRE_MENTION_IN_GROUPS", "")
	t.Setenv("AGENT_RUNTIME_LLM_ALLOWED_ROLES", "")
	t.Setenv("AGENT_RUNTIME_LLM_ALLOWED_CONTEXT_IDS", "")
	t.Setenv("AGENT_RUNTIME_LLM_RATE_LIMIT_PER_WINDOW", "")
	t.Setenv("AGENT_RUNTIME_LLM_RATE_LIMIT_WINDOW_SECONDS", "")
	t.Setenv("AGENT_RUNTIME_LLM_GROUNDING_TOP_K", "")
	t.Setenv("AGENT_RUNTIME_LLM_GROUNDING_MAX_DOC_EXCERPT_BYTES", "")
	t.Setenv("AGENT_RUNTIME_LLM_GROUNDING_MAX_PROMPT_BYTES", "")
	t.Setenv("AGENT_RUNTIME_LLM_GROUNDING_CHAT_TAIL_LINES", "")
	t.Setenv("AGENT_RUNTIME_LLM_GROUNDING_CHAT_TAIL_BYTES", "")
	t.Setenv("AGENT_RUNTIME_LLM_ADMIN_SYSTEM_PROMPT", "")
	t.Setenv("AGENT_RUNTIME_LLM_PUBLIC_SYSTEM_PROMPT", "")
	t.Setenv("AGENT_RUNTIME_AGENT_GROUNDING_FIRST_STEP", "")
	t.Setenv("AGENT_RUNTIME_AGENT_GROUNDING_EVERY_STEP", "")
	t.Setenv("AGENT_RUNTIME_SOUL_GLOBAL_FILE", "")
	t.Setenv("AGENT_RUNTIME_SOUL_WORKSPACE_REL_PATH", "")
	t.Setenv("AGENT_RUNTIME_SOUL_CONTEXT_REL_PATH", "")
	t.Setenv("AGENT_RUNTIME_SYSTEM_PROMPT_GLOBAL_FILE", "")
	t.Setenv("AGENT_RUNTIME_SYSTEM_PROMPT_WORKSPACE_REL_PATH", "")
	t.Setenv("AGENT_RUNTIME_SYSTEM_PROMPT_CONTEXT_REL_PATH", "")
	t.Setenv("AGENT_RUNTIME_SKILLS_GLOBAL_ROOT", "")

	cfg := FromEnv()
	if cfg.DataDir != "/data" {
		t.Fatalf("expected default data dir /data, got %s", cfg.DataDir)
	}
	if cfg.WorkspaceRoot != filepath.Join("/data", "workspaces") {
		t.Fatalf("unexpected default workspace root: %s", cfg.WorkspaceRoot)
	}
	if cfg.DBPath != filepath.Join("/data", "agent-runtime", "meta.sqlite") {
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
	if cfg.QMDIndexName != "agent-runtime" {
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
	if cfg.TaskRecoveryRunningStaleSec != 600 {
		t.Fatalf("expected default task recovery running stale seconds 600, got %d", cfg.TaskRecoveryRunningStaleSec)
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
	if cfg.AgentSensitiveApprovalTTLSeconds != 600 {
		t.Fatalf("expected default sensitive approval ttl seconds 600, got %d", cfg.AgentSensitiveApprovalTTLSeconds)
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
	if cfg.CodexPublishURL != "" {
		t.Fatalf("expected default codex publish url empty, got %s", cfg.CodexPublishURL)
	}
	if cfg.CodexPublishBearerToken != "" {
		t.Fatal("expected default codex publish bearer token empty")
	}
	if cfg.CodexPublishTimeoutSec != 8 {
		t.Fatalf("expected default codex publish timeout 8, got %d", cfg.CodexPublishTimeoutSec)
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
	if cfg.ExtPluginsConfigPath != "ext/plugins/plugins.json" {
		t.Fatalf("expected default ext plugin config path ext/plugins/plugins.json, got %s", cfg.ExtPluginsConfigPath)
	}
	if cfg.ExtPluginCacheDir != filepath.Join("/data", "agent-runtime", "ext-plugin-cache") {
		t.Fatalf("expected default ext plugin cache dir /data/agent-runtime/ext-plugin-cache, got %s", cfg.ExtPluginCacheDir)
	}
	if !cfg.ExtPluginWarmOnBootstrap {
		t.Fatal("expected default ext plugin warm_on_bootstrap true")
	}
	if cfg.MCPConfigPath != "ext/mcp/servers.json" {
		t.Fatalf("expected default mcp config path ext/mcp/servers.json, got %s", cfg.MCPConfigPath)
	}
	if cfg.MCPWorkspaceConfigRelPath != "context/mcp/servers.json" {
		t.Fatalf("expected default mcp workspace config rel path context/mcp/servers.json, got %s", cfg.MCPWorkspaceConfigRelPath)
	}
	if cfg.MCPRefreshSeconds != 120 {
		t.Fatalf("expected default mcp refresh seconds 120, got %d", cfg.MCPRefreshSeconds)
	}
	if cfg.MCPHTTPTimeoutSec != 30 {
		t.Fatalf("expected default mcp http timeout seconds 30, got %d", cfg.MCPHTTPTimeoutSec)
	}
	if !cfg.SandboxEnabled {
		t.Fatal("expected sandbox enabled by default")
	}
	if cfg.SandboxAllowedCommandsCSV == "" {
		t.Fatal("expected default sandbox allowlist")
	}
	if !strings.Contains(","+cfg.SandboxAllowedCommandsCSV+",", ",rg,") {
		t.Fatalf("expected default sandbox allowlist to include rg, got %s", cfg.SandboxAllowedCommandsCSV)
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
	if cfg.LLMGroundingTopK != 3 {
		t.Fatalf("expected default llm grounding top k 3, got %d", cfg.LLMGroundingTopK)
	}
	if cfg.LLMGroundingMaxDocExcerpt != 1200 {
		t.Fatalf("expected default llm grounding max doc excerpt 1200, got %d", cfg.LLMGroundingMaxDocExcerpt)
	}
	if cfg.LLMGroundingMaxPromptBytes != 8000 {
		t.Fatalf("expected default llm grounding max prompt bytes 8000, got %d", cfg.LLMGroundingMaxPromptBytes)
	}
	if cfg.LLMGroundingMaxPromptTokens != 2000 {
		t.Fatalf("expected default llm grounding max prompt tokens 2000, got %d", cfg.LLMGroundingMaxPromptTokens)
	}
	if cfg.LLMGroundingUserMaxTokens != 650 {
		t.Fatalf("expected default llm grounding user max tokens 650, got %d", cfg.LLMGroundingUserMaxTokens)
	}
	if cfg.LLMGroundingSummaryMaxTokens != 380 {
		t.Fatalf("expected default llm grounding summary max tokens 380, got %d", cfg.LLMGroundingSummaryMaxTokens)
	}
	if cfg.LLMGroundingChatTailMaxTokens != 300 {
		t.Fatalf("expected default llm grounding chat tail max tokens 300, got %d", cfg.LLMGroundingChatTailMaxTokens)
	}
	if cfg.LLMGroundingQMDMaxTokens != 900 {
		t.Fatalf("expected default llm grounding qmd max tokens 900, got %d", cfg.LLMGroundingQMDMaxTokens)
	}
	if cfg.LLMGroundingChatTailLines != 24 {
		t.Fatalf("expected default llm grounding chat tail lines 24, got %d", cfg.LLMGroundingChatTailLines)
	}
	if cfg.LLMGroundingChatTailBytes != 1800 {
		t.Fatalf("expected default llm grounding chat tail bytes 1800, got %d", cfg.LLMGroundingChatTailBytes)
	}
	if cfg.LLMGroundingSummaryRefreshTurns != 6 {
		t.Fatalf("expected default llm grounding memory summary refresh turns 6, got %d", cfg.LLMGroundingSummaryRefreshTurns)
	}
	if cfg.LLMGroundingSummaryMaxItems != 7 {
		t.Fatalf("expected default llm grounding memory summary max items 7, got %d", cfg.LLMGroundingSummaryMaxItems)
	}
	if cfg.LLMGroundingSummarySourceMaxLines != 120 {
		t.Fatalf("expected default llm grounding memory summary source max lines 120, got %d", cfg.LLMGroundingSummarySourceMaxLines)
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
	if cfg.SkillsGlobalRoot != "/data/.agents/skills" {
		t.Fatalf("expected default skills global root /data/.agents/skills, got %s", cfg.SkillsGlobalRoot)
	}
	if !cfg.AgentGroundingFirstStep {
		t.Fatal("expected agent grounding first step enabled by default")
	}
	if cfg.AgentGroundingEveryStep {
		t.Fatal("expected agent grounding every step disabled by default")
	}
	if !cfg.AdminTLSSkipVerify {
		t.Fatal("expected admin tls skip verify to default to true")
	}
	if cfg.AdminAPIURL != "https://admin.localhost" {
		t.Fatalf("expected default admin api url, got %s", cfg.AdminAPIURL)
	}
	if cfg.AdminHTTPTimeoutSec != 120 {
		t.Fatalf("expected default admin http timeout 120, got %d", cfg.AdminHTTPTimeoutSec)
	}
}

func TestFromEnvOverrides(t *testing.T) {
	t.Setenv("AGENT_RUNTIME_DATA_DIR", "/var/agent-runtime")
	t.Setenv("AGENT_RUNTIME_WORKSPACE_ROOT", "/var/agent-runtime/ws")
	t.Setenv("AGENT_RUNTIME_DB_PATH", "/var/agent-runtime/db.sqlite")
	t.Setenv("AGENT_RUNTIME_DEFAULT_CONCURRENCY", "9")
	t.Setenv("AGENT_RUNTIME_QMD_BINARY", "/usr/local/bin/qmd")
	t.Setenv("AGENT_RUNTIME_QMD_SIDECAR_URL", "http://agent-runtime-qmd:8091")
	t.Setenv("AGENT_RUNTIME_QMD_SIDECAR_ADDR", ":19091")
	t.Setenv("AGENT_RUNTIME_QMD_INDEX", "community")
	t.Setenv("AGENT_RUNTIME_QMD_COLLECTION", "docs")
	t.Setenv("AGENT_RUNTIME_QMD_SHARED_MODELS_DIR", "/var/agent-runtime/qmd-models")
	t.Setenv("AGENT_RUNTIME_QMD_EMBED_EXCLUDE_GLOBS", "logs/chats/**,tasks/**")
	t.Setenv("AGENT_RUNTIME_QMD_SEARCH_LIMIT", "9")
	t.Setenv("AGENT_RUNTIME_QMD_OPEN_MAX_BYTES", "2400")
	t.Setenv("AGENT_RUNTIME_QMD_DEBOUNCE_SECONDS", "7")
	t.Setenv("AGENT_RUNTIME_QMD_INDEX_TIMEOUT_SECONDS", "420")
	t.Setenv("AGENT_RUNTIME_QMD_QUERY_TIMEOUT_SECONDS", "44")
	t.Setenv("AGENT_RUNTIME_QMD_AUTO_EMBED", "false")
	t.Setenv("AGENT_RUNTIME_OBJECTIVE_POLL_SECONDS", "11")
	t.Setenv("AGENT_RUNTIME_TASK_RECOVERY_RUNNING_STALE_SECONDS", "240")
	t.Setenv("AGENT_RUNTIME_HEARTBEAT_ENABLED", "true")
	t.Setenv("AGENT_RUNTIME_HEARTBEAT_INTERVAL_SECONDS", "20")
	t.Setenv("AGENT_RUNTIME_HEARTBEAT_STALE_SECONDS", "75")
	t.Setenv("AGENT_RUNTIME_HEARTBEAT_NOTIFY_ADMIN", "false")
	t.Setenv("AGENT_RUNTIME_TRIAGE_ENABLED", "false")
	t.Setenv("AGENT_RUNTIME_TRIAGE_NOTIFY_ADMIN", "false")
	t.Setenv("AGENT_RUNTIME_TASK_NOTIFY_POLICY", "admin")
	t.Setenv("AGENT_RUNTIME_TASK_NOTIFY_SUCCESS_POLICY", "origin")
	t.Setenv("AGENT_RUNTIME_TASK_NOTIFY_FAILURE_POLICY", "admin")
	t.Setenv("AGENT_RUNTIME_AGENT_SENSITIVE_APPROVAL_TTL_SECONDS", "120")
	t.Setenv("AGENT_RUNTIME_COMMAND_SYNC_ENABLED", "false")
	t.Setenv("AGENT_RUNTIME_DISCORD_API_BASE", "https://discord.test/api/v10")
	t.Setenv("AGENT_RUNTIME_DISCORD_GATEWAY_URL", "wss://discord.test/gateway")
	t.Setenv("AGENT_RUNTIME_DISCORD_APPLICATION_ID", "1234567890")
	t.Setenv("AGENT_RUNTIME_DISCORD_COMMAND_GUILD_IDS", "111,222")
	t.Setenv("AGENT_RUNTIME_TELEGRAM_API_BASE", "https://telegram.test")
	t.Setenv("AGENT_RUNTIME_TELEGRAM_POLL_SECONDS", "12")
	t.Setenv("AGENT_RUNTIME_CODEX_PUBLISH_URL", "https://codex.example.com/publish")
	t.Setenv("AGENT_RUNTIME_CODEX_PUBLISH_BEARER_TOKEN", "codex-secret")
	t.Setenv("AGENT_RUNTIME_CODEX_PUBLISH_TIMEOUT_SECONDS", "21")
	t.Setenv("AGENT_RUNTIME_IMAP_HOST", "imap.example.com")
	t.Setenv("AGENT_RUNTIME_IMAP_PORT", "1993")
	t.Setenv("AGENT_RUNTIME_IMAP_USERNAME", "inbox@example.com")
	t.Setenv("AGENT_RUNTIME_IMAP_PASSWORD", "imap-secret")
	t.Setenv("AGENT_RUNTIME_IMAP_MAILBOX", "Support")
	t.Setenv("AGENT_RUNTIME_IMAP_POLL_SECONDS", "33")
	t.Setenv("AGENT_RUNTIME_IMAP_TLS_SKIP_VERIFY", "true")
	t.Setenv("AGENT_RUNTIME_LLM_PROVIDER", "anthropic")
	t.Setenv("AGENT_RUNTIME_LLM_BASE_URL", "https://api.anthropic.com")
	t.Setenv("AGENT_RUNTIME_LLM_API_KEY", "anthropic-key")
	t.Setenv("AGENT_RUNTIME_LLM_MODEL", "claude-3.5-sonic")
	t.Setenv("AGENT_RUNTIME_LLM_TIMEOUT_SECONDS", "90")
	t.Setenv("AGENT_RUNTIME_SMTP_HOST", "smtp.example.com")
	t.Setenv("AGENT_RUNTIME_SMTP_PORT", "2525")
	t.Setenv("AGENT_RUNTIME_SMTP_USERNAME", "bot@example.com")
	t.Setenv("AGENT_RUNTIME_SMTP_PASSWORD", "secret")
	t.Setenv("AGENT_RUNTIME_SMTP_FROM", "Agent Runtime Bot <bot@example.com>")
	t.Setenv("AGENT_RUNTIME_EXT_PLUGINS_CONFIG", "/etc/agent-runtime/plugins.json")
	t.Setenv("AGENT_RUNTIME_EXT_PLUGIN_CACHE_DIR", "/var/agent-runtime/ext-plugin-cache")
	t.Setenv("AGENT_RUNTIME_EXT_PLUGIN_WARM_ON_BOOTSTRAP", "false")
	t.Setenv("AGENT_RUNTIME_MCP_CONFIG", "/etc/agent-runtime/mcp-servers.json")
	t.Setenv("AGENT_RUNTIME_MCP_WORKSPACE_CONFIG_REL_PATH", "runtime/mcp/workspace.json")
	t.Setenv("AGENT_RUNTIME_MCP_REFRESH_SECONDS", "33")
	t.Setenv("AGENT_RUNTIME_MCP_HTTP_TIMEOUT_SECONDS", "44")
	t.Setenv("AGENT_RUNTIME_SANDBOX_ENABLED", "false")
	t.Setenv("AGENT_RUNTIME_SANDBOX_ALLOWED_COMMANDS", "curl,git,rg")
	t.Setenv("AGENT_RUNTIME_SANDBOX_RUNNER_COMMAND", "just-bash")
	t.Setenv("AGENT_RUNTIME_SANDBOX_RUNNER_ARGS", "--network=off --readonly")
	t.Setenv("AGENT_RUNTIME_SANDBOX_TIMEOUT_SECONDS", "45")
	t.Setenv("AGENT_RUNTIME_LLM_ENABLED", "true")
	t.Setenv("AGENT_RUNTIME_LLM_ALLOW_DM", "false")
	t.Setenv("AGENT_RUNTIME_LLM_REQUIRE_MENTION_IN_GROUPS", "false")
	t.Setenv("AGENT_RUNTIME_LLM_ALLOWED_ROLES", "admin,overlord")
	t.Setenv("AGENT_RUNTIME_LLM_ALLOWED_CONTEXT_IDS", "ctx-1,ctx-2")
	t.Setenv("AGENT_RUNTIME_LLM_RATE_LIMIT_PER_WINDOW", "3")
	t.Setenv("AGENT_RUNTIME_LLM_RATE_LIMIT_WINDOW_SECONDS", "120")
	t.Setenv("AGENT_RUNTIME_LLM_GROUNDING_TOP_K", "7")
	t.Setenv("AGENT_RUNTIME_LLM_GROUNDING_MAX_DOC_EXCERPT_BYTES", "2200")
	t.Setenv("AGENT_RUNTIME_LLM_GROUNDING_MAX_PROMPT_BYTES", "12000")
	t.Setenv("AGENT_RUNTIME_LLM_GROUNDING_MAX_PROMPT_TOKENS", "2600")
	t.Setenv("AGENT_RUNTIME_LLM_GROUNDING_USER_MAX_TOKENS", "700")
	t.Setenv("AGENT_RUNTIME_LLM_GROUNDING_MEMORY_SUMMARY_MAX_TOKENS", "420")
	t.Setenv("AGENT_RUNTIME_LLM_GROUNDING_CHAT_TAIL_MAX_TOKENS", "330")
	t.Setenv("AGENT_RUNTIME_LLM_GROUNDING_QMD_MAX_TOKENS", "1150")
	t.Setenv("AGENT_RUNTIME_LLM_GROUNDING_CHAT_TAIL_LINES", "40")
	t.Setenv("AGENT_RUNTIME_LLM_GROUNDING_CHAT_TAIL_BYTES", "3200")
	t.Setenv("AGENT_RUNTIME_LLM_GROUNDING_MEMORY_SUMMARY_REFRESH_TURNS", "4")
	t.Setenv("AGENT_RUNTIME_LLM_GROUNDING_MEMORY_SUMMARY_MAX_ITEMS", "9")
	t.Setenv("AGENT_RUNTIME_LLM_GROUNDING_MEMORY_SUMMARY_SOURCE_MAX_LINES", "180")
	t.Setenv("AGENT_RUNTIME_LLM_ADMIN_SYSTEM_PROMPT", "admin prompt")
	t.Setenv("AGENT_RUNTIME_LLM_PUBLIC_SYSTEM_PROMPT", "public prompt")
	t.Setenv("AGENT_RUNTIME_AGENT_GROUNDING_FIRST_STEP", "false")
	t.Setenv("AGENT_RUNTIME_AGENT_GROUNDING_EVERY_STEP", "true")
	t.Setenv("AGENT_RUNTIME_SOUL_GLOBAL_FILE", "/context/GLOBAL_SOUL.md")
	t.Setenv("AGENT_RUNTIME_SOUL_WORKSPACE_REL_PATH", "persona/SOUL.md")
	t.Setenv("AGENT_RUNTIME_SOUL_CONTEXT_REL_PATH", "persona/agents/{context_id}.md")
	t.Setenv("AGENT_RUNTIME_SYSTEM_PROMPT_GLOBAL_FILE", "/context/GLOBAL_SYSTEM_PROMPT.md")
	t.Setenv("AGENT_RUNTIME_SYSTEM_PROMPT_WORKSPACE_REL_PATH", "persona/SYSTEM_PROMPT.md")
	t.Setenv("AGENT_RUNTIME_SYSTEM_PROMPT_CONTEXT_REL_PATH", "persona/agents/{context_id}/SYSTEM_PROMPT.md")
	t.Setenv("AGENT_RUNTIME_SKILLS_GLOBAL_ROOT", "/context/skill-packs")
	t.Setenv("PUBLIC_HOST", "chat.example.com")
	t.Setenv("ADMIN_HOST", "admin.example.com")
	t.Setenv("AGENT_RUNTIME_ADMIN_API_URL", "https://admin.example.com")
	t.Setenv("AGENT_RUNTIME_ADMIN_HTTP_TIMEOUT_SECONDS", "45")
	t.Setenv("AGENT_RUNTIME_ADMIN_TLS_SKIP_VERIFY", "false")
	t.Setenv("AGENT_RUNTIME_TUI_APPROVER_USER_ID", "overlord-1")
	t.Setenv("AGENT_RUNTIME_TUI_APPROVAL_ROLE", "overlord")

	cfg := FromEnv()
	if cfg.DataDir != "/var/agent-runtime" {
		t.Fatalf("expected overridden data dir, got %s", cfg.DataDir)
	}
	if cfg.WorkspaceRoot != "/var/agent-runtime/ws" {
		t.Fatalf("expected overridden workspace root, got %s", cfg.WorkspaceRoot)
	}
	if cfg.DBPath != "/var/agent-runtime/db.sqlite" {
		t.Fatalf("expected overridden db path, got %s", cfg.DBPath)
	}
	if cfg.DefaultConcurrency != 9 {
		t.Fatalf("expected overridden concurrency, got %d", cfg.DefaultConcurrency)
	}
	if cfg.QMDBinary != "/usr/local/bin/qmd" {
		t.Fatalf("expected overridden qmd binary, got %s", cfg.QMDBinary)
	}
	if cfg.QMDSidecarURL != "http://agent-runtime-qmd:8091" {
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
	if cfg.QMDSharedModelsDir != "/var/agent-runtime/qmd-models" {
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
	if cfg.TaskRecoveryRunningStaleSec != 240 {
		t.Fatalf("expected overridden task recovery running stale seconds, got %d", cfg.TaskRecoveryRunningStaleSec)
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
	if cfg.AgentSensitiveApprovalTTLSeconds != 120 {
		t.Fatalf("expected overridden sensitive approval ttl seconds 120, got %d", cfg.AgentSensitiveApprovalTTLSeconds)
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
	if cfg.CodexPublishURL != "https://codex.example.com/publish" {
		t.Fatalf("expected overridden codex publish url, got %s", cfg.CodexPublishURL)
	}
	if cfg.CodexPublishBearerToken != "codex-secret" {
		t.Fatalf("expected overridden codex publish bearer token, got %s", cfg.CodexPublishBearerToken)
	}
	if cfg.CodexPublishTimeoutSec != 21 {
		t.Fatalf("expected overridden codex publish timeout, got %d", cfg.CodexPublishTimeoutSec)
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
	if cfg.SMTPFrom != "Agent Runtime Bot <bot@example.com>" {
		t.Fatalf("expected overridden smtp from, got %s", cfg.SMTPFrom)
	}
	if cfg.ExtPluginsConfigPath != "/etc/agent-runtime/plugins.json" {
		t.Fatalf("expected overridden ext plugins config path, got %s", cfg.ExtPluginsConfigPath)
	}
	if cfg.ExtPluginCacheDir != "/var/agent-runtime/ext-plugin-cache" {
		t.Fatalf("expected overridden ext plugin cache dir, got %s", cfg.ExtPluginCacheDir)
	}
	if cfg.ExtPluginWarmOnBootstrap {
		t.Fatal("expected ext plugin warm_on_bootstrap false")
	}
	if cfg.MCPConfigPath != "/etc/agent-runtime/mcp-servers.json" {
		t.Fatalf("expected overridden mcp config path, got %s", cfg.MCPConfigPath)
	}
	if cfg.MCPWorkspaceConfigRelPath != "runtime/mcp/workspace.json" {
		t.Fatalf("expected overridden mcp workspace config rel path, got %s", cfg.MCPWorkspaceConfigRelPath)
	}
	if cfg.MCPRefreshSeconds != 33 {
		t.Fatalf("expected overridden mcp refresh seconds, got %d", cfg.MCPRefreshSeconds)
	}
	if cfg.MCPHTTPTimeoutSec != 44 {
		t.Fatalf("expected overridden mcp http timeout seconds, got %d", cfg.MCPHTTPTimeoutSec)
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
	if cfg.LLMGroundingTopK != 7 {
		t.Fatalf("expected overridden llm grounding top k 7, got %d", cfg.LLMGroundingTopK)
	}
	if cfg.LLMGroundingMaxDocExcerpt != 2200 {
		t.Fatalf("expected overridden llm grounding max doc excerpt 2200, got %d", cfg.LLMGroundingMaxDocExcerpt)
	}
	if cfg.LLMGroundingMaxPromptBytes != 12000 {
		t.Fatalf("expected overridden llm grounding max prompt bytes 12000, got %d", cfg.LLMGroundingMaxPromptBytes)
	}
	if cfg.LLMGroundingMaxPromptTokens != 2600 {
		t.Fatalf("expected overridden llm grounding max prompt tokens 2600, got %d", cfg.LLMGroundingMaxPromptTokens)
	}
	if cfg.LLMGroundingUserMaxTokens != 700 {
		t.Fatalf("expected overridden llm grounding user max tokens 700, got %d", cfg.LLMGroundingUserMaxTokens)
	}
	if cfg.LLMGroundingSummaryMaxTokens != 420 {
		t.Fatalf("expected overridden llm grounding summary max tokens 420, got %d", cfg.LLMGroundingSummaryMaxTokens)
	}
	if cfg.LLMGroundingChatTailMaxTokens != 330 {
		t.Fatalf("expected overridden llm grounding chat tail max tokens 330, got %d", cfg.LLMGroundingChatTailMaxTokens)
	}
	if cfg.LLMGroundingQMDMaxTokens != 1150 {
		t.Fatalf("expected overridden llm grounding qmd max tokens 1150, got %d", cfg.LLMGroundingQMDMaxTokens)
	}
	if cfg.LLMGroundingChatTailLines != 40 {
		t.Fatalf("expected overridden llm grounding chat tail lines 40, got %d", cfg.LLMGroundingChatTailLines)
	}
	if cfg.LLMGroundingChatTailBytes != 3200 {
		t.Fatalf("expected overridden llm grounding chat tail bytes 3200, got %d", cfg.LLMGroundingChatTailBytes)
	}
	if cfg.LLMGroundingSummaryRefreshTurns != 4 {
		t.Fatalf("expected overridden llm grounding memory summary refresh turns 4, got %d", cfg.LLMGroundingSummaryRefreshTurns)
	}
	if cfg.LLMGroundingSummaryMaxItems != 9 {
		t.Fatalf("expected overridden llm grounding memory summary max items 9, got %d", cfg.LLMGroundingSummaryMaxItems)
	}
	if cfg.LLMGroundingSummarySourceMaxLines != 180 {
		t.Fatalf("expected overridden llm grounding memory summary source max lines 180, got %d", cfg.LLMGroundingSummarySourceMaxLines)
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
	if cfg.AgentGroundingFirstStep {
		t.Fatal("expected overridden agent grounding first step false")
	}
	if !cfg.AgentGroundingEveryStep {
		t.Fatal("expected overridden agent grounding every step true")
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
	if cfg.AdminHTTPTimeoutSec != 45 {
		t.Fatalf("expected overridden admin http timeout 45, got %d", cfg.AdminHTTPTimeoutSec)
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
