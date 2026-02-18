# Agent Runtime Getting Started

This guide defines Agent Runtime as a general-purpose runtime for running and orchestrating agents across chat channels and coding tools.

## What Agent Runtime Is

Agent Runtime is a channel-first control plane that:

- receives inbound messages/events from connectors
- routes and triages requests
- runs background tasks via an orchestrator/worker engine
- applies approval gates for sensitive external actions
- persists chat/task/memory artifacts per workspace

It can be used for ops, product, engineering, support, research, and any workflow that benefits from tool-using agents.

## 1) Bootstrap Runtime

```bash
cp .env.example .env
set -a; source .env; set +a
make run
```

If you use Docker:

```bash
docker compose up -d --build
```

## 2) Verify Control Plane

```bash
curl -fsS http://localhost:8080/healthz
curl -fsS http://localhost:8080/readyz
```

## 3) Connect Channels

### Telegram

1. Create bot token with `@BotFather`.
2. Set `AGENT_RUNTIME_TELEGRAM_TOKEN` in `.env`.
3. Restart runtime.
4. In Telegram DM: send `pair`, approve in TUI, then run `/admin-channel enable`.

Detailed guide: `docs/channels/telegram.md`

### Discord

1. Create app + bot in Discord Developer Portal.
2. Set `AGENT_RUNTIME_DISCORD_TOKEN` in `.env`.
3. Enable Message Content intent.
4. Invite bot with `applications.commands`.
5. Restart runtime, pair identity, then `/admin-channel enable`.

Detailed guide: `docs/channels/discord.md`

### Coding Tools (Codex, Cline, Gemini)

All coding tools connect through the same `codex` connector identity pattern using the admin API + mTLS.

1. Ensure admin API env is loaded:

```bash
set -a; source .env; set +a
```

2. Create a unique session and pair as admin:

```bash
SESSION_ID="tool-$(date +%H%M%S)"
go run ./cmd/agent-runtime chat pairing pair-admin \
  --connector codex \
  --external-id "$SESSION_ID" \
  --from-user-id "$SESSION_ID" \
  --display-name "Tool Client" \
  --role admin \
  --approver-user-id bootstrap-admin
```

3. Send a hello-world message:

```bash
go run ./cmd/agent-runtime chat \
  --connector codex \
  --external-id "$SESSION_ID" \
  --from-user-id "$SESSION_ID" \
  --display-name "Tool Client" \
  -m "hello world from tool"
```

Detailed guides:

- `docs/channels/codex.md`
- `docs/channels/cline.md`
- `docs/channels/gemini.md`

## 4) Run a Minimal End-to-End Test

Use any connected channel/tool and run:

```text
/status
```

Then:

```text
/task run a small hello-world test and summarize the result
```

Check artifacts:

- chat logs: `data/workspaces/<workspace-id>/logs/chats/...`
- task results: `data/workspaces/<workspace-id>/tasks/YYYY/MM/DD/...`
- memory summaries: `data/workspaces/<workspace-id>/memory/contexts/...`

## 5) Recommended Next Steps

- Configure sandbox command allowlist for your environment.
- Configure MCP servers in `ext/mcp/servers.json` for native MCP tool access.
- Example:
  ```json
  {
    "schema_version": "v1",
    "servers": [
      {
        "id": "github",
        "enabled": true,
        "transport": {
          "type": "streamable_http",
          "endpoint": "https://mcp.example.com/mcp"
        },
        "http": {
          "headers": {
            "Authorization": "Bearer ${AGENT_RUNTIME_GITHUB_MCP_TOKEN}"
          },
          "timeout_seconds": 30
        },
        "refresh_seconds": 120
      }
    ]
  }
  ```
- Configure official external plugins under `ext/plugins/plugins.json` (TinyFish + Resend).
- Set external plugin secrets in `.env` (`AGENT_RUNTIME_TINYFISH_API_KEY`, `AGENT_RUNTIME_RESEND_API_KEY`, `AGENT_RUNTIME_RESEND_FROM`).
- Keep uv plugin cache persistent (`AGENT_RUNTIME_EXT_PLUGIN_CACHE_DIR`) for faster warm restarts.
- Install Vercel skills globally for Codex (optional): `make skills-bootstrap` (defaults to `vercel-labs/agent-skills`) or `./scripts/bootstrap-skills.sh pipe-rack/skills`.
  - In containers, bootstrap defaults `HOME=/data`, so skills persist under `/data/.agents/skills`.
- Configure objective/scheduler settings for recurring workloads.
- Configure Codex publish callback if you want proactive messages back into coding tools.
- Define system prompts/skills for your domain-specific workflows.
