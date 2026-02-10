Use `git` in read-only mode for repository inspection.

Command action block:
```action
{"type":"run_command","target":"git","summary":"Show tracked changes","payload":{"args":["status","--short"]}}
```

Allowed intent examples:
- `git status --short`
- `git diff -- <path>`
- `git log --oneline -n 20`
- `git show <commit>`

Rules:
- Do not propose mutating git commands (`commit`, `push`, `reset`, `rebase`, `checkout`, `clean`).
- Keep output focused to the path or range needed for the task.
