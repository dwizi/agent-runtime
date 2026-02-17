# HTTP API Reference

This runtime exposes public health endpoints and an admin API namespace under
`/api/v1/*`.

Note: in production, admin API access is expected to be protected by mTLS and
reverse-proxy policy.

## Health and Info

### `GET /healthz`

Response:

```json
{"status":"ok"}
```

### `GET /readyz`

Response when ready:

```json
{"status":"ready"}
```

### `GET /api/v1/heartbeat`

Returns heartbeat snapshot for runtime components.

### `GET /api/v1/info`

Response:

```json
{
  "name": "agent-runtime",
  "environment": "production",
  "public_host": "example.com",
  "admin_host": "admin.example.com"
}
```

## Chat

### `POST /api/v1/chat`

Request:

```json
{
  "connector": "codex",
  "external_id": "session-123",
  "display_name": "Tool Client",
  "from_user_id": "session-123",
  "text": "hello"
}
```

Response:

```json
{
  "handled": true,
  "reply": "..."
}
```

## Tasks

### `POST /api/v1/tasks`

Creates a queued task.

Required fields: `workspace_id`, `context_id`, `title`, `prompt`

Request:

```json
{
  "workspace_id": "ws-1",
  "context_id": "ctx-1",
  "title": "Investigate webhook latency",
  "prompt": "Collect latency by endpoint and summarize",
  "kind": "general",
  "route_class": "issue",
  "priority": "p2",
  "assigned_lane": "operations",
  "due_at_unix": 1760000000
}
```

Response (`202 Accepted`):

```json
{
  "id": "task_xxx",
  "workspace_id": "ws-1",
  "context_id": "ctx-1",
  "kind": "general",
  "status": "queued"
}
```

### `GET /api/v1/tasks?id=<task-id>`

Returns one task record.

### `GET /api/v1/tasks?workspace_id=<id>&status=<optional>&kind=<optional>&limit=<optional>`

Returns task list:

```json
{
  "items": [
    {
      "id": "task_xxx",
      "workspace_id": "ws-1",
      "context_id": "ctx-1",
      "status": "queued"
    }
  ],
  "count": 1
}
```

### `POST /api/v1/tasks/retry`

Request:

```json
{"task_id":"task_failed_id"}
```

Only failed tasks are retryable.

## Pairings

### `POST /api/v1/pairings/start`

Request:

```json
{
  "connector": "telegram",
  "connector_user_id": "123456",
  "display_name": "Alice",
  "expires_in_sec": 600
}
```

Response (`201 Created`) includes pairing token.

### `GET /api/v1/pairings/lookup?token=<token>`

Returns pairing status and metadata.

### `POST /api/v1/pairings/approve`

Request:

```json
{
  "token": "ABCDEF1234",
  "approver_user_id": "bootstrap-admin",
  "role": "admin",
  "target_user_id": "optional-user-id"
}
```

### `POST /api/v1/pairings/deny`

Request:

```json
{
  "token": "ABCDEF1234",
  "approver_user_id": "bootstrap-admin",
  "reason": "duplicate request"
}
```

## Objectives

### `POST /api/v1/objectives`

Request:

```json
{
  "workspace_id": "ws-1",
  "context_id": "ctx-1",
  "title": "Daily status summary",
  "prompt": "Summarize key updates from markdown",
  "trigger_type": "schedule",
  "cron_expr": "0 */6 * * *",
  "timezone": "UTC",
  "active": true
}
```

### `GET /api/v1/objectives?workspace_id=<id>&active_only=<optional>&limit=<optional>`

Returns:

```json
{
  "items": [
    {
      "id": "obj_xxx",
      "workspace_id": "ws-1",
      "active": true,
      "trigger_type": "schedule"
    }
  ],
  "count": 1
}
```

### `POST /api/v1/objectives/update`

Request:

```json
{
  "id": "obj_xxx",
  "title": "Updated title",
  "active": true
}
```

### `POST /api/v1/objectives/active`

Request:

```json
{"id":"obj_xxx","active":false}
```

### `POST /api/v1/objectives/delete`

Request:

```json
{"id":"obj_xxx"}
```

## Error Conventions

- Validation and business-rule failures typically return `400` with:

```json
{"error":"..."}
```

- Not found cases return `404` when explicitly mapped (for example pairing/task
  lookup paths).
- Method mismatch returns `405`.
- Runtime/internal failures return `500`.
