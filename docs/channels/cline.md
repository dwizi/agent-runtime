# Cline AI Channel

Use this channel when you want Cline AI Assistant to access agent-runtime directly from the Cline CLI interface.

## Prerequisites

- runtime is up (docker containers running)
- admin API env is loaded (`AGENT_RUNTIME_ADMIN_API_URL` and mTLS vars)

```bash
set -a; source .env; set +a
```

## Pairing (CLI Admin Bootstrap)

Create a one-shot bootstrap for a Cline session identity. Use a unique `external-id` each time for a brand-new session:

```bash
SESSION_ID="cline-$(date +%H%M%S)"
echo "$SESSION_ID"

AGENT_RUNTIME_ADMIN_TLS_CA_FILE="$PWD/ops/caddy/pki/clients-ca.crt" \
AGENT_RUNTIME_ADMIN_TLS_CERT_FILE="$PWD/ops/caddy/pki/admin-client.crt" \
AGENT_RUNTIME_ADMIN_TLS_KEY_FILE="$PWD/ops/caddy/pki/admin-client.key" \
go run ./cmd/agent-runtime chat pairing pair-admin \
  --connector codex \
  --external-id "$SESSION_ID" \
  --from-user-id "$SESSION_ID" \
  --display-name "Cline AI Assistant" \
  --role admin \
  --approver-user-id bootstrap-admin
```

## Realtime Chat

Send a single message to the agent runtime:

```bash
SESSION_ID="cline-133543"

AGENT_RUNTIME_ADMIN_TLS_CA_FILE="$PWD/ops/caddy/pki/clients-ca.crt" \
AGENT_RUNTIME_ADMIN_TLS_CERT_FILE="$PWD/ops/caddy/pki/admin-client.crt" \
AGENT_RUNTIME_ADMIN_TLS_KEY_FILE="$PWD/ops/caddy/pki/admin-client.key" \
go run ./cmd/agent-runtime chat \
  --connector codex \
  --external-id "$SESSION_ID" \
  --from-user-id "$SESSION_ID" \
  --display-name "Cline AI Assistant" \
  -m "Your message here"
```

Interactive mode (send multiple messages):

```bash
SESSION_ID="cline-133543"

AGENT_RUNTIME_ADMIN_TLS_CA_FILE="$PWD/ops/caddy/pki/clients-ca.crt" \
AGENT_RUNTIME_ADMIN_TLS_CERT_FILE="$PWD/ops/caddy/pki/admin-client.crt" \
AGENT_RUNTIME_ADMIN_TLS_KEY_FILE="$PWD/ops/caddy/pki/admin-client.key" \
go run ./cmd/agent-runtime chat \
  --connector codex \
  --external-id "$SESSION_ID" \
  --from-user-id "$SESSION_ID" \
  --display-name "Cline AI Assistant"
```

Then type messages and `/exit` to quit.

## Example Conversation

After pairing, you can have a natural conversation with the agent:

```bash
# Send greeting
go run ./cmd/agent-runtime chat \
  --connector codex \
  --external-id "$SESSION_ID" \
  --from-user-id "$SESSION_ID" \
  --display-name "Cline AI Assistant" \
  -m "Hello! I'm Cline, an AI coding assistant. Can you introduce yourself?"

# Check status
go run ./cmd/agent-runtime chat \
  --connector codex \
  --external-id "$SESSION_ID" \
  --from-user-id "$SESSION_ID" \
  --display-name "Cline AI Assistant" \
  -m "Can you show me the current status of the workspace?"

# Queue a task
go run ./cmd/agent-runtime chat \
  --connector codex \
  --external-id "$SESSION_ID" \
  --from-user-id "$SESSION_ID" \
  --display-name "Cline AI Assistant" \
  -m "/task run ls -la /data/workspaces"
```

## Quick Health Check

After pairing, verify the connection is working:

```bash
go run ./cmd/agent-runtime chat \
  --connector codex \
  --external-id "$SESSION_ID" \
  --from-user-id "$SESSION_ID" \
  --display-name "Cline AI Assistant" \
  -m "/status"
```

## Notes

- Agent Runtime is a general-purpose orchestrator for tool-using agents
- It respects sandbox restrictions and will inform you when commands aren't available
- Task execution requires approval and follows the same workflow as other channels
- Session IDs should be unique per conversation to maintain proper context separation
- All TLS certificate paths are absolute and must match your local environment
