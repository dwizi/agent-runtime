# TinyFish External Plugin

This plugin handles:

- `agentic_web`
- `tinyfish_sync`
- `tinyfish_async`

using the TinyFish Automation API.

## Required Env

- `AGENT_RUNTIME_TINYFISH_API_KEY` (mapped to `TINYFISH_API_KEY`)

Optional:

- `AGENT_RUNTIME_TINYFISH_BASE_URL` (defaults to `https://agent.tinyfish.ai`)

## Action Shape

`run_action` example:

```json
{
  "type": "agentic_web",
  "target": "https://informador.mx/",
  "summary": "Fetch latest 3 headlines with URLs",
  "payload": {}
}
```

Required values:

- goal via `summary` or `payload.goal` or `payload.task`
- url via `target` or `payload.url` or `payload.request.url`
