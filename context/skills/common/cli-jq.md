Use `jq` to read JSON files already available in the workspace.

Command action block:
```action
{"type":"run_command","target":"jq","summary":"Read task status counts","payload":{"args":["-r",".status_counts","data/reports/tasks.json"]}}
```

Rules:
- Keep filters explicit and deterministic (`-r` for plain text output when useful).
- Prefer file inputs over shell pipelines.
- If JSON is missing or invalid, report the exact file and next step.
