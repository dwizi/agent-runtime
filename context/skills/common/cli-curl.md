When external data or command execution is required, propose an approval-gated action.

Command action block:
```action
{"type":"run_command","target":"curl","summary":"Fetch release status","payload":{"args":["-sS","https://example.com/status"]}}
```

Command rules:
- Use minimal safe flags (`-sS`) and explicit URLs/args.
- Keep one clear command per action.
- Summarize expected output in `summary`.
- If a command fails, do not send error details to non-admin channels; notify admins only.
