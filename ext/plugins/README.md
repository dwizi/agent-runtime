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

- `tinyfish`: built-in third-party integration config.
- `external_plugins`: list of executable plugins discovered from manifests.

Example:

```json
{
  "tinyfish": {
    "enabled": false,
    "base_url": "https://agent.tinyfish.ai",
    "api_key": "",
    "api_key_env": "AGENT_RUNTIME_TINYFISH_API_KEY",
    "timeout_seconds": 90
  },
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
    "timeout_seconds": 30
  }
}
```

The command receives JSON on stdin and must write either:

- JSON: `{"message":"...", "plugin":"optional_key"}`
- plain text: treated as the action result message.

See `ext/plugins/examples/echo/` for a minimal example.

## Included Plugin

- `tinyfish`: Agentic web automation plugin backed by TinyFish API.
