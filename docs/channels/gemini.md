# Gemini CLI - Agent Runtime Chat Guide

This guide explains how to use the Gemini CLI (or any terminal-based agent) to interact with the `agent-runtime` via the Codex connector.

## Prerequisites

Ensure you have the following environment variables set or sourced from your `.env` file. The runtime uses mTLS for admin API access.

```bash
# Sourcing from .env
set -a; source .env; set +a

# Required variables for the chat tool:
# AGENT_RUNTIME_ADMIN_API_URL (e.g., https://admin.localhost)
# AGENT_RUNTIME_ADMIN_TLS_CA_FILE
# AGENT_RUNTIME_ADMIN_TLS_CERT_FILE
# AGENT_RUNTIME_ADMIN_TLS_KEY_FILE
# AGENT_RUNTIME_ADMIN_TLS_SKIP_VERIFY (usually true for local dev)
```

## 1. Pairing (First Time Only)

Before you can chat, you must pair your session identity. Use a unique `SESSION_ID` for each distinct conversation you want to track.

```bash
SESSION_ID="gemini-$(date +%H%M%S)"
echo "Your Session ID is: $SESSION_ID"

go run ./cmd/agent-runtime chat pairing pair-admin 
  --connector codex 
  --external-id "$SESSION_ID" 
  --from-user-id "$SESSION_ID" 
  --display-name "Gemini CLI" 
  --role admin 
  --approver-user-id bootstrap-admin
```

## 2. Sending a Single Message

To send a one-off command or message and receive a response immediately:

```bash
SESSION_ID="your-session-id"

go run ./cmd/agent-runtime chat 
  --connector codex 
  --external-id "$SESSION_ID" 
  -m "/status"
```

## 3. Interactive Chat Mode

To enter a persistent chat session where you can send multiple messages:

```bash
SESSION_ID="your-session-id"

go run ./cmd/agent-runtime chat 
  --connector codex 
  --external-id "$SESSION_ID"
```
*Type `/exit` to leave the interactive session.*

## Common Commands

The agent runtime supports several slash commands:

- `/status`: Check the status of the current workspace and indexing.
- `/pending-actions`: List actions (like commands or API calls) awaiting approval.
- `/task run <command>`: Queue a new task to be executed by the runtime.
- `/tasks`: Request a summary of current tasks (behavior depends on agent capabilities).
- `/clear`: Clear the local chat history.

## Example Workflow

1. **Pair**: `go run ./cmd/agent-runtime chat pairing pair-admin ...`
2. **Check Health**: `go run ./cmd/agent-runtime chat ... -m "/status"`
3. **Run Task**: `go run ./cmd/agent-runtime chat ... -m "/task run ls -la"`
4. **Approve Actions**: If a task requires approval, use `/pending-actions` to find the ID, then approve it (usually via the TUI or another admin channel).
