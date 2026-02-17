# Objectives Flow

Canonical operator reference for how objectives are created, scheduled, triggered, executed, and observed.

## What An Objective Is

An objective is a persisted automation record that creates `objective` tasks over time.

Two trigger types are supported:
- `schedule`: cron-based recurring execution
- `event`: file-change-driven execution (currently `markdown.updated`)

Core fields:
- `workspace_id`, `context_id`, `title`, `prompt`
- `trigger_type`, `cron_expr`, `timezone`, `event_key`
- `active`, `next_run_unix`, `last_run_unix`, `last_error`
- metrics: `run_count`, `success_count`, `failure_count`, streaks, durations

## End-to-End Flow

### Schedule objective flow
1. Objective is created (`POST /api/v1/objectives`) with:
   - `trigger_type: "schedule"`
   - `cron_expr`
   - optional `timezone` (defaults to `UTC`)
2. Store validates cron + timezone and computes `next_run_unix`.
3. Scheduler polls every `AGENT_RUNTIME_OBJECTIVE_POLL_SECONDS` (default `15`).
4. Due objectives enqueue a task of kind `objective`.
5. Run metadata is updated (`last_run_unix`, `next_run_unix`, error/metrics).
6. Task executes via the task worker (same execution path as general tasks).

### Event objective flow
1. Objective is created/updated with:
   - `trigger_type: "event"`
   - `event_key: "markdown.updated"`
2. Runtime file watcher sees a Markdown file change and maps the path to workspace.
3. Event objectives for that workspace are loaded.
4. Each objective enqueues an `objective` task with changed-file context in prompt.
5. Run metadata is updated and metrics are recorded.

## Event Trigger Scope

Event objectives only trigger for `.md` files under workspace root and skip:
- `.qmd/**`
- `logs/**`
- `tasks/**`
- `ops/**`
- files outside workspace root

## Idempotency And Dedupe

Schedule objectives:
- run key format: `objective:<objective-id>:<scheduled-unix>`
- prevents duplicate task rows for the same schedule window

Event objectives:
- run key format: `objective:<objective-id>:event:<time-bucket>:<path-hash>`
- dedupe window: 30 seconds
- prevents save-burst duplicate task rows

## Failure Policy

When a run fails:
- scheduler records `last_error` and increments failure metrics
- schedule objectives apply exponential backoff:
  - min: 1 minute
  - max: 30 minutes
- objective auto-pauses after 5 consecutive failures:
  - `active` set `false`
  - `auto_paused_reason` populated

## API Usage

### Create schedule objective

```bash
curl -sS -X POST http://localhost/api/v1/objectives \
  -H "content-type: application/json" \
  -d '{
    "workspace_id": "ws-1",
    "context_id": "ctx-1",
    "title": "Daily objective check",
    "prompt": "Check objective health and summarize changes.",
    "trigger_type": "schedule",
    "cron_expr": "0 */6 * * *",
    "timezone": "America/Chicago",
    "active": true
  }'
```

### Create event objective

```bash
curl -sS -X POST http://localhost/api/v1/objectives \
  -H "content-type: application/json" \
  -d '{
    "workspace_id": "ws-1",
    "context_id": "ctx-1",
    "title": "React to docs edits",
    "prompt": "Review the changed markdown and propose follow-ups.",
    "trigger_type": "event",
    "event_key": "markdown.updated",
    "active": true
  }'
```

### List objectives

```bash
curl -sS "http://localhost/api/v1/objectives?workspace_id=ws-1&active_only=false&limit=50"
```

### Update objective

```bash
curl -sS -X POST http://localhost/api/v1/objectives/update \
  -H "content-type: application/json" \
  -d '{
    "id": "obj_123",
    "cron_expr": "*/15 * * * *",
    "timezone": "UTC",
    "active": true
  }'
```

### Pause/resume objective

```bash
curl -sS -X POST http://localhost/api/v1/objectives/active \
  -H "content-type: application/json" \
  -d '{"id":"obj_123","active":false}'
```

### Delete objective

```bash
curl -sS -X POST http://localhost/api/v1/objectives/delete \
  -H "content-type: application/json" \
  -d '{"id":"obj_123"}'
```

## API Response Observability Fields

`GET /api/v1/objectives` includes:
- scheduling:
  - `next_run_unix`, `last_run_unix`, `next_runs_unix`
- health:
  - `health_state` (`healthy` | `degraded` | `paused` | `auto_paused`)
  - `last_error`, `auto_paused_reason`
- counters:
  - `run_count`, `success_count`, `failure_count`
  - `consecutive_successes` (`success_streak`)
  - `consecutive_failures` (`failure_streak`)
- timing:
  - `total_run_duration_ms`, `avg_run_duration_ms`
  - `last_success_unix`, `last_failure_unix`
- recent failures:
  - `recent_errors` list (`at_unix`, `error`)

## `/monitor` Command Behavior

`/monitor <goal>` creates a schedule objective automatically:
- trigger: `schedule`
- cron: `0 */6 * * *` (every 6 hours)
- timezone: default `UTC`
- active: `true`

Use objective APIs (or TUI) to tune cadence, timezone, or to pause/delete.

## Verification Checklist

1. Create objective.
2. Confirm row via `GET /api/v1/objectives`.
3. Force immediate due run (set `next_run_unix` to now-5 via update).
4. Wait one poll cycle (`~AGENT_RUNTIME_OBJECTIVE_POLL_SECONDS`).
5. Confirm:
   - objective `last_run_unix` and counters changed
   - a task with kind `objective` exists in tasks API

## Current Limitations

- TUI supports list/pause/delete only (no create/edit forms).
- Only one event key is supported today: `markdown.updated`.
- Event triggers are Markdown-only and path-filtered as listed above.
