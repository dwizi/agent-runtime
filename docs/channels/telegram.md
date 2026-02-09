# Telegram Bot Token Guide

This guide covers creating a Telegram bot token and wiring it to Spinner.

## 1. Create a bot with BotFather

1. Open Telegram and search for `@BotFather`.
2. Start chat and run:
   - `/newbot`
3. Follow prompts:
   - bot display name
   - bot username (must end in `bot`)

BotFather returns an HTTP API token after creation.

## 2. Set the token in `.env`

```env
SPINNER_TELEGRAM_TOKEN=your_telegram_bot_token
```

## 3. Optional environment overrides

Defaults are usually correct:

```env
SPINNER_TELEGRAM_API_BASE=https://api.telegram.org
SPINNER_TELEGRAM_POLL_SECONDS=25
```

## 4. Start Spinner and test

1. Start Spinner:
   - `make run`
2. Open DM with your bot and send:
   - `pair`
3. Spinner should reply with a one-time pairing token.
4. Open:
   - `make tui`
5. Paste token, then approve with `a`.

## 5. Test channel commands

After pairing, use commands in Telegram chats where the bot is present:

- `/task <prompt>`
- `/admin-channel enable` (admin role required)
- `/approve <token>`
- `/deny <token> [reason]`

If commands fail:

- Check token is valid in `.env`
- Ensure bot is in the chat
- Ensure your Telegram identity is linked (via `pair` + TUI approval)

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
