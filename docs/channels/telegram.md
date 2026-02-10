# Telegram Bot Token Guide (Overlord/Admin)

This guide covers creating a Telegram bot token and validating admin control-plane access.

## 1. Create a bot with BotFather

1. Open Telegram and search for `@BotFather`.
2. Start chat and run:
   - `/newbot`
3. Follow prompts:
   - bot display name
   - bot username (must end in `bot`)

BotFather returns an HTTP API token after creation.

## 2. Set token and poll settings in `.env`

```env
SPINNER_TELEGRAM_TOKEN=your_telegram_bot_token
SPINNER_TELEGRAM_POLL_SECONDS=25
SPINNER_COMMAND_SYNC_ENABLED=true
```

## 3. Optional environment overrides

Defaults are usually correct:

```env
SPINNER_TELEGRAM_API_BASE=https://api.telegram.org
SPINNER_TELEGRAM_POLL_SECONDS=25
```

Startup behavior:
- Spinner calls Telegram `setMyCommands` when the connector starts.
- Telegram menu commands are generated from Spinner's shared command catalog.
- command naming follows Telegram constraints (for example `admin-channel` appears as `admin_channel`).

## 4. Start Spinner and bootstrap admin pairing

1. Start Spinner:
   - `make run`
2. Open DM with your bot and send:
   - `pair`
3. Spinner should reply with a one-time pairing token.
4. Open:
   - `make tui`
5. Paste token, then approve with `a`.
6. In your admin Telegram chat, run:
   - `/admin-channel enable`

## 5. Test admin and task commands

After pairing, use commands in Telegram chats where the bot is present:

- `/task <prompt>`
- `/admin-channel enable` (admin role required)
- `/approve <token>`
- `/deny <token> [reason]`
- `/pending-actions`
- `/approve-action <id>`

If commands fail:

- Check token is valid in `.env`
- Ensure bot is in the chat
- Ensure your Telegram identity is linked (via `pair` + TUI approval)

If Telegram command menu does not refresh:

- Restart Spinner to run command sync at connector bootstrap.
- Ensure `SPINNER_COMMAND_SYNC_ENABLED=true`.

## Production hardening

1. Keep tokens out of git
   - Never commit `.env`.
   - Store `SPINNER_TELEGRAM_TOKEN` in a secret manager (for example: cloud secret store, Vault, or CI/CD secrets).

2. Rotate tokens regularly
   - Use `@BotFather` to revoke/refresh token on a schedule.
   - Update runtime secret and redeploy Spinner immediately.

3. Use least privilege in chats
   - Add bot only to chats/channels where automation is required.
   - Avoid granting Telegram admin permissions unless explicitly needed.
   - Restrict bot privacy mode based on your command model.

4. Minimize blast radius
   - Use separate bots for production and staging.
   - Use separate tokens per environment.

5. Incident response
   - If token exposure is suspected:
     - Revoke/regenerate token via `@BotFather`.
     - Update secret manager and redeploy.
     - Audit recent bot interactions for abuse.
