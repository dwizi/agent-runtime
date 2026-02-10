# Discord Bot Token Guide (Overlord/Admin)

This guide covers creating a Discord bot token and validating admin/task command flow.

## 1. Create a Discord application and bot

1. Open the Discord Developer Portal.
2. Create a **New Application**.
3. Open **Bot** in the left menu.
4. Click **Add Bot**.

## 2. Copy token and set runtime env

1. In the **Bot** page, find **Token**.
2. Click **Reset Token** (or **Copy**) and store it securely.
3. Set it in your local `.env`:

```env
SPINNER_DISCORD_TOKEN=your_discord_bot_token
SPINNER_DISCORD_API_BASE=https://discord.com/api/v10
SPINNER_DISCORD_GATEWAY_URL=wss://gateway.discord.gg/?v=10&encoding=json
SPINNER_COMMAND_SYNC_ENABLED=true
# Optional (recommended for immediate slash-command updates):
SPINNER_DISCORD_APPLICATION_ID=
SPINNER_DISCORD_COMMAND_GUILD_IDS=123456789012345678
```

## 3. Enable required intents

In the **Bot** page, enable:

- **Server Members Intent** (optional now, useful later)
- **Message Content Intent** (required for message-based commands)

Spinner supports both:
- message commands parsed from `MESSAGE_CREATE`
- Discord slash commands from `INTERACTION_CREATE`

`Message Content Intent` is still required for message-based command parsing and natural language messages.

## 4. Set OAuth2 scopes and invite the bot

1. Open **OAuth2** -> **URL Generator**.
2. Select scopes:
   - `bot`
   - `applications.commands` (recommended)
3. Select bot permissions:
   - `View Channels`
   - `Send Messages`
   - `Read Message History`
   - `Manage Messages` (optional)
4. Open the generated URL and invite the bot to your server.

## 5. Validate with Spinner

1. Start Spinner:
   - `make run`
2. In Discord:
   - DM the bot: `pair`
   - In a server channel: `/task write a short test summary`
   - Verify slash menu shows Spinner commands after startup sync
   - In your admin channel: `/admin-channel enable`

If the bot does not respond:

- Verify token is correct in `.env`
- Verify `Message Content Intent` is enabled
- Confirm bot has permission to read/send messages in that channel
- Confirm identity was paired and approved in TUI

If slash commands do not show up:

- Confirm `applications.commands` scope was included during bot invite.
- Restart Spinner to trigger command sync at connector bootstrap.
- Prefer guild-scoped sync for faster propagation:
  - set `SPINNER_DISCORD_COMMAND_GUILD_IDS=<your-guild-id-csv>`
- If app lookup fails in your environment, set `SPINNER_DISCORD_APPLICATION_ID` explicitly.

## Production hardening

1. Keep tokens out of git
   - Never commit `.env`.
   - Store `SPINNER_DISCORD_TOKEN` in a secret manager (for example: cloud secret store, Vault, or CI/CD secrets).

2. Rotate tokens regularly
   - Reset the bot token on a defined schedule (for example monthly/quarterly).
   - Re-deploy Spinner immediately after rotation.

3. Use least privilege
   - Only grant required bot permissions:
     - `View Channels`
     - `Send Messages`
     - `Read Message History`
   - Disable optional permissions unless needed.
   - Keep privileged intents limited to what Spinner uses.

4. Restrict bot access scope
   - Invite the bot only to required servers.
   - Remove bot access from unused servers/channels.

5. Incident response
   - If token exposure is suspected:
     - Reset token immediately in Developer Portal.
     - Update secret manager and redeploy.
     - Review recent bot activity and audit logs.
