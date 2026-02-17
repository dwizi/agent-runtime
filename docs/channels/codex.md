# Codex CLI Channel

Use this channel when you want to message `agent-runtime` directly from Codex/terminal and run admin pairing without Telegram/Discord DM flow.

## Prerequisites

- runtime is up
- admin API env is loaded (`AGENT_RUNTIME_ADMIN_API_URL` and mTLS vars)

```bash
set -a; source .env; set +a
```

## Pairing (CLI Admin Bootstrap)

One-shot bootstrap for a Codex session identity:

```bash
go run ./cmd/agent-runtime chat pairing pair-admin \
  --connector codex \
  --external-id codex-cli \
  --from-user-id codex-cli \
  --display-name "Codex CLI" \
  --role admin \
  --approver-user-id bootstrap-admin
```

For a brand-new chat session, prefer a unique `external-id` each time:

```bash
SESSION_ID="codex-$(date +%H%M%S)"
echo "$SESSION_ID"
go run ./cmd/agent-runtime chat pairing pair-admin \
  --connector codex \
  --external-id "$SESSION_ID" \
  --from-user-id "$SESSION_ID" \
  --display-name "Codex CLI" \
  --role admin \
  --approver-user-id bootstrap-admin
```

## Optional Proactive Delivery

To enable proactive outbound notifications into Codex sessions (task/objective/heartbeat notices), configure:

```bash
AGENT_RUNTIME_CODEX_PUBLISH_URL=https://your-codex-callback.example/publish
AGENT_RUNTIME_CODEX_PUBLISH_BEARER_TOKEN=optional-token
AGENT_RUNTIME_CODEX_PUBLISH_TIMEOUT_SECONDS=8
```

Publisher payload format:

```json
{"connector":"codex","external_id":"codex-cli","text":"..."}
```

Manual lifecycle (optional):

```bash
go run ./cmd/agent-runtime chat pairing start --connector codex --external-id codex-cli --from-user-id codex-cli
go run ./cmd/agent-runtime chat pairing lookup <token>
go run ./cmd/agent-runtime chat pairing approve <token> --approver-user-id <admin-user-id> --role admin
go run ./cmd/agent-runtime chat pairing deny <token> --approver-user-id <admin-user-id> --reason "<reason>"
```

## Realtime Chat

Single message:

```bash
go run ./cmd/agent-runtime chat \
  --connector codex \
  --external-id "$SESSION_ID" \
  --from-user-id "$SESSION_ID" \
  --display-name "Codex CLI" \
  -m "/pending-actions"
```

Interactive:

```bash
go run ./cmd/agent-runtime chat \
  --connector codex \
  --external-id "$SESSION_ID" \
  --from-user-id "$SESSION_ID" \
  --display-name "Codex CLI"
```

Then type messages and `/exit` to quit.

## Quick Health Check (Recommended)

After pairing and before long testing, run (reuse the same `SESSION_ID` from pairing):

```bash
go run ./cmd/agent-runtime chat \
  --connector codex \
  --external-id "$SESSION_ID" \
  --from-user-id "$SESSION_ID" \
  --display-name "Codex CLI" \
  -m "/status"
```

Then verify monitoring tools quickly:

```bash
go run ./cmd/agent-runtime chat \
  --connector codex \
  --external-id "$SESSION_ID" \
  --from-user-id "$SESSION_ID" \
  --display-name "Codex CLI" \
  -m "/task run curl --version and rg --version, then summarize what they show"
```
