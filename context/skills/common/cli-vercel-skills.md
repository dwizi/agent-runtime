Install Vercel agent skills with `skills` CLI (`npx` or `bunx`).

Primary intent:
- Install skills globally for Codex (`~/.codex/skills`).
- Keep commands non-interactive for automation (`-y`).

Preferred flow:
1. List available skills first.
2. Install selected skills globally for `codex`.
3. Verify installed global skills.

Use `bunx` when available:
```action
{"type":"run_command","target":"bunx","summary":"List skills from Vercel agent-skills repository","payload":{"args":["skills","add","vercel-labs/agent-skills","--list"]}}
```

```action
{"type":"run_command","target":"bunx","summary":"Install selected Vercel skills globally for Codex","payload":{"args":["skills","add","vercel-labs/agent-skills","-g","-a","codex","--skill","find-skills","--skill","skill-installer","-y"]}}
```

Fallback to `npx`:
```action
{"type":"run_command","target":"npx","summary":"Install selected Vercel skills globally for Codex","payload":{"args":["skills","add","vercel-labs/agent-skills","-g","-a","codex","--skill","find-skills","--skill","skill-installer","-y"]}}
```

Verify:
```action
{"type":"run_command","target":"npx","summary":"List globally installed Codex skills","payload":{"args":["skills","list","-g","-a","codex"]}}
```

Rules:
- Prefer specific `--skill` names over `--all`.
- Use `--agent codex` when targeting Codex skill directories.
- Use global installs (`-g`) only when explicitly requested.
