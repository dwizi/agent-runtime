# Channel Setup (Overlord/Admin)

Use these guides when provisioning connector credentials.

1. Telegram:
   - `docs/channels/telegram.md`
2. Discord:
   - `docs/channels/discord.md`

After configuring tokens:

1. restart runtime (`make compose-up` or service restart)
2. pair at least one admin identity (`pair` in DM, then approve in TUI)
3. enable an admin context:
   - `/admin-channel enable`
4. verify task command path:
   - `/task smoke test`
