# Resend External Plugin

This plugin handles `resend_email` actions using the Resend API.

Runtime isolation:

- uv-managed project (`pyproject.toml` + `uv.lock`)
- bootstrap warm-up enabled by manifest

## Required Env

- `AGENT_RUNTIME_RESEND_API_KEY` (mapped to `RESEND_API_KEY`)
- `AGENT_RUNTIME_RESEND_FROM` (mapped to `RESEND_FROM`) unless `payload.from` is provided

Optional:

- `AGENT_RUNTIME_RESEND_API_BASE` (defaults to `https://api.resend.com`)

## Action Shape

`run_action` example:

```json
{
  "type": "resend_email",
  "target": "user@example.com",
  "summary": "Welcome to Dwizi",
  "payload": {
    "subject": "Welcome to Dwizi",
    "text": "Thanks for joining."
  }
}
```

`payload` fields:

- `to`: string or string[] recipient(s). If omitted, `target` is used.
- `from`: sender address. If omitted, `RESEND_FROM` is used.
- `subject`: email subject. Defaults to `summary`.
- `text`: plain text body.
- `html`: HTML body.
- `cc`, `bcc`, `reply_to`: string or string[].
- `tags`, `headers`: pass-through objects/arrays for Resend.
