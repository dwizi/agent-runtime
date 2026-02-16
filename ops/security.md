# Agent Runtime Security Checklist

This checklist is for running Agent Runtime in production with a strong default posture.

## 1. Secrets management

- Keep `.env` out of git.
- Store secrets in a secret manager (not plaintext on developer laptops):
  - `AGENT_RUNTIME_DISCORD_TOKEN`
  - `AGENT_RUNTIME_TELEGRAM_TOKEN`
  - `AGENT_RUNTIME_LLM_API_KEY` (and keep `AGENT_RUNTIME_LLM_BASE_URL`/`AGENT_RUNTIME_LLM_PROVIDER` guarded as needed)
- Use separate credentials per environment (`dev`, `staging`, `prod`).
- Rotate all connector/API tokens on a schedule and after personnel changes.

## 2. Channel token hardening

- Discord:
  - Enable only required intents.
  - Grant only required bot permissions in target channels.
  - Remove bot from unused servers/channels.
- Telegram:
  - Use dedicated bot per environment.
  - Avoid unnecessary admin privileges in groups/channels.
  - Revoke and regenerate token immediately on exposure suspicion.

## 3. Admin mTLS and local PKI

- Keep admin API protected by mTLS (`ADMIN_HOST` endpoint).
- Store generated client key material securely:
  - `ops/caddy/pki/admin-client.key`
  - `ops/caddy/pki/admin-client.crt`
  - `ops/caddy/pki/clients-ca.crt`
- Rotate admin client certs periodically.
- Never share `.p12` bundles over insecure channels.

## 4. RBAC and approval controls

- Assign least-privilege roles (`viewer`, `member`, `operator`, `admin`, `overlord`).
- Restrict `/admin-channel enable`, `/approve`, `/deny` to `admin`/`overlord`.
- Require explicit approvals for risky actions (email send, external posting, shell-like tools).
- Periodically audit linked identities and remove stale access.

## 5. Runtime and container hardening

- Pin image tags and update base images regularly.
- Run only required ports:
  - `80/443` on Caddy
  - no direct public access to internal service ports
- Keep host OS patched.
- Limit Docker host access to trusted operators only.
- Back up persistent volumes (`agent_runtime_data`, Caddy data/config, and `ops/caddy/pki`).

## 6. Network and edge security

- Enforce TLS for all external access.
- Keep strict host routing for public vs admin domains.
- Add rate limiting/WAF controls in front of Caddy where available.
- Restrict inbound IPs for admin surfaces where possible.

## 7. Logging and monitoring

- Keep structured logs and centralize them.
- Alert on:
  - repeated auth/pairing failures
  - unusual token usage patterns
  - connector reconnect loops
  - task execution spikes/failures
- Retain security-relevant logs according to policy.

## 8. Incident response

If compromise is suspected:

1. Revoke all exposed tokens/certs immediately.
2. Rotate secrets and redeploy.
3. Disable connector integrations until validated.
4. Review recent logs for abuse scope.
5. Re-link/re-approve identities where required.

## 9. Recommended recurring checks

- Weekly: review admin identities and connected channels.
- Monthly: rotate connector/API tokens; validate cert expiration windows.
- Quarterly: disaster-recovery restore test from backups.
