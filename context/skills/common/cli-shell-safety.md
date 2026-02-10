Sandbox command safety policy:

- Prefer direct, single-purpose executables from the allowlist.
- Keep args explicit; avoid wildcard-heavy or destructive operations.
- Include `cwd` only when needed and keep it inside the workspace.
- State expected effect in `summary` so admins can approve quickly.

Command action block:
```action
{"type":"run_command","target":"find","summary":"List markdown files in workspace docs","payload":{"args":["docs","-type","f","-name","*.md"]}}
```

Failure handling:
- Report concise impact and a concrete remediation step.
- Avoid exposing raw error dumps in non-admin channels.
