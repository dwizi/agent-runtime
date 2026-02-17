# Operations Runbook (Overlord/Admin)

Day-2 tasks for maintaining Agent Runtime safely.

## Daily Checks

1. Runtime health:
   - `curl -fsS http://localhost/healthz`
   - `curl -fsS http://localhost/readyz`
   - `curl -fsS http://localhost/api/v1/heartbeat`
   - verify `overall` is not `degraded`
2. Review pending approvals in admin channels:
   - `/pending-actions`
3. Review objective health:
   - `GET /api/v1/objectives?workspace_id=<id>&active_only=true`
4. Review IMAP ingestion paths:
   - `/data/workspaces/<ws>/inbox/imap/...`
5. Review task execution lifecycle:
   - `sqlite3 /data/agent-runtime/meta.sqlite "SELECT status, count(*) FROM tasks GROUP BY status ORDER BY status;"`
   - task outputs under `/data/workspaces/<ws>/tasks/YYYY/MM/DD/<task-id>.md`

## Admin/TUI Controls

Start TUI:

```bash
make tui
```

Modes:
- `Pairings` tab: approve/deny one-time link tokens
- `Objectives` tab:
  - set workspace id and press `Enter`
  - `j/k` select
  - `p` pause/resume
  - `x` delete
  - `r` refresh
- `Tasks` tab:
  - set workspace id and press `Enter`
  - `j/k` select
  - `[`/`]` cycle status filter
  - `y` retry selected failed task
  - `r` refresh

## Approvals Workflow

When LLM proposes external actions:
- list: `/pending-actions`
- approve: `/approve-action <action-id>`
- deny: `/deny-action <action-id> [reason]`

Guideline:
- approve only actions aligned with workspace policy and role scope
- deny with reason for audit clarity

## Message Routing Overrides

When the Agent (Reasoning Engine) creates routed tasks from channel traffic:
- inspect task metadata (`route_class`, `priority`, `due_at_unix`, `assigned_lane`)
- override from admin channels with:
  - `/route <task-id> <question|issue|task|moderation|noise> [p1|p2|p3] [due-window]`
- example:
  - `/route task-123 moderation p1 2h`

Use this when the Agent misclassifies intent (e.g., treating a question as a task).

## Objective Lifecycle

Detailed objective lifecycle/run-policy/API reference:
- `docs/objectives-flow.md`

Create objective:
- `POST /api/v1/objectives`

Pause/resume:
- `POST /api/v1/objectives/active`

Update trigger/prompt:
- `POST /api/v1/objectives/update`

Delete:
- `POST /api/v1/objectives/delete`

## Task Operations

List tasks:
- `GET /api/v1/tasks?workspace_id=<id>&status=<optional>&limit=<optional>`

Task detail:
- `GET /api/v1/tasks?id=<task-id>`

Retry failed task:
- `POST /api/v1/tasks/retry`

## Incident Response

If token/cert compromise is suspected:

1. Rotate connector tokens (Discord/Telegram).
2. Rotate mTLS material (`ops/caddy/pki`) and restart Caddy.
3. Set `AGENT_RUNTIME_SANDBOX_ENABLED=false` temporarily if command actions are risky.
4. Review recent action approvals and chat logs in:
   - `/data/workspaces/<ws>/logs/chats/...`
5. Re-pair impacted admin identities if necessary.

## Backup and Recovery

Back up:
- `/data/agent-runtime/meta.sqlite`
- `/data/workspaces/`
- `.env` (secure secret storage, never public)
- `ops/caddy/pki` (if you manage cert continuity there)

Restore:
1. restore volumes/files
2. verify `.env`
3. run `make compose-up`
4. validate health endpoints and admin pairing access
