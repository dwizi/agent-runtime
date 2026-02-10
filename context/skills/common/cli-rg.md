Use `rg` for fast workspace search.

Command action block:
```action
{"type":"run_command","target":"rg","summary":"Find TODO markers in docs","payload":{"args":["-n","TODO","docs"]}}
```

Rules:
- Prefer `rg --files` for file discovery and `rg -n` for content search.
- Scope searches to a directory when possible.
- Use fixed strings first; only use complex regex when needed.
