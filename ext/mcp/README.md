# MCP Servers

`ext/mcp/servers.json` defines global MCP server connections.

Schema (`v1`):

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
      "refresh_seconds": 120,
      "policy": {
        "default_tool_class": "general",
        "default_requires_approval": false,
        "tool_overrides": {
          "dangerous_tool": {
            "tool_class": "sensitive",
            "requires_approval": true
          }
        }
      }
    }
  ]
}
```

## Workspace Overrides

Workspace-level overrides are read from:

`/data/workspaces/<workspace-id>/context/mcp/servers.json`

Rules:

- Same schema version (`v1`).
- Only global server IDs can be overridden.
- Unknown server IDs are ignored with warning logs.
- Override values win over global values for provided fields.

## Notes

- Transport types: `streamable_http`, `sse`.
- Header values support env templates like `${AGENT_RUNTIME_*}`.
- Missing env vars in templates fail that server config.
- Runtime starts even if one or more MCP servers are unreachable; those servers remain degraded and are retried.
