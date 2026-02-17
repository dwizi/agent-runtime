# Channel Setup (Overlord/Admin)

Use these guides when provisioning connector credentials.

1. Telegram:
   - `docs/channels/telegram.md`
2. Discord:
   - `docs/channels/discord.md`
3. Codex CLI:
   - `docs/channels/codex.md`

After configuring tokens:

1. restart runtime (`make compose-up` or service restart)
   - this also re-runs Telegram/Discord command sync at connector bootstrap
2. pair at least one admin identity (`pair` in DM, then approve in TUI)
   - for Codex CLI channel, use `chat pairing pair-admin` (see `docs/channels/codex.md`)
3. enable an admin context:
   - `/admin-channel enable`
4. verify task command path:
   - `/task smoke test`

## Command Surface Quick Reference

| Command | Telegram menu | Discord slash | Text command parsing |
| --- | --- | --- | --- |
| `task`, `search`, `open`, `status`, `monitor` | yes | yes | yes |
| `admin-channel`, `prompt`, `approve`, `deny` | yes | yes | yes |
| `pending-actions`, `approve-action`, `deny-action` | yes | yes | yes |
| `pair` | yes (DM) | no | yes (DM) |
| `route` | yes | yes | yes (admin) |

Notes:
- Telegram menu names use underscores (example: `/admin_channel`).
- `route` is available in text and synced command surfaces.
