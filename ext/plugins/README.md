# External Plugins

`ext/plugins` is reserved for third-party action plugins.

This folder should not contain application runtime code.

Plugins must be explicitly enabled in `ext/plugins/plugins.json` (or the path set by
`AGENT_RUNTIME_EXT_PLUGINS_CONFIG`) before runtime bootstraps them.

The runtime/plugin loader code lives under `internal/`.

The intent is to allow installing plugins from other repositories into this
folder in the future.

## Config File

`ext/plugins/plugins.json` supports:

- `external_plugins`: list of executable plugins discovered from manifests.

Example:

```json
{
  "external_plugins": [
    {
      "id": "echo-example",
      "enabled": true,
      "manifest": "examples/echo/plugin.json"
    }
  ]
}
```

## Manifest Contract

Each external plugin repo/folder should provide a `plugin.json`:

```json
{
  "schema_version": "v1",
  "name": "Plugin Name",
  "plugin_key": "my_external_plugin",
  "action_types": ["my_action_type"],
  "runtime": {
    "command": "./run.sh",
    "args": [],
    "env": {},
    "timeout_seconds": 30,
    "isolation": {
      "mode": "none",
      "project": ".",
      "warm_on_bootstrap": true,
      "locked": true
    }
  }
}
```

`runtime.isolation` supports:

- `mode`: `none` (default) or `uv`
- `project`: relative project path for uv (default `.`)
- `warm_on_bootstrap`: prebuild env at startup (default `true`)
- `locked`: use `uv sync --locked` (default `true`)

## Execution Model

- External plugins run through the same sandbox runner wrapper used by command actions when
  `AGENT_RUNTIME_SANDBOX_RUNNER_COMMAND` and `AGENT_RUNTIME_SANDBOX_RUNNER_ARGS` are set.
- For `mode=uv`:
  - startup warmup uses `uv sync --project <project-path> --no-dev` (with `--locked` when enabled)
  - execution uses `uv run --project <project-path> --no-sync -- <runtime.command> <runtime.args...>`
  - cache defaults to `/data/agent-runtime/ext-plugin-cache`
- Warmup failures log warnings and do not abort runtime startup.
- First execution attempts lazy uv sync if warmup was skipped or failed.

The command receives JSON on stdin and must write either:

- JSON: `{"message":"...", "plugin":"optional_key"}`
- plain text: treated as the action result message.

See `ext/plugins/examples/echo/` for a minimal example.

## Included Plugin

- `tinyfish`: Agentic web automation via TinyFish API.
- `resend` (`resend_email`): Sends emails through Resend.

Official plugin locations:

- `ext/plugins/tinyfish`
- `ext/plugins/resend`
