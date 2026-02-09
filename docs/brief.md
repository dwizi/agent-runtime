# Project Brief — spinner

## One-liner

Spinner is a security-first orchestration system that connects community channels (Discord, Telegram; later X), files, and email inboxes into policy-isolated “contexts” where agents can research, triage, and act — with RBAC and admin approvals — entirely through chat and a TUI.

## Why now

Communities increasingly need always-on operations: competitive research, inbox triage, knowledge capture, and proactive follow-ups. Existing “agent” products often centralize data, mix contexts, or require a web UI. Spinner is designed to be **self-hosted**, **zero-trust**, and **Markdown-native**.

## Primary users

- **Overlord/Admin**: provisions deployment, approves capabilities, manages policies and skills.
- **Operator**: runs day-to-day workflows (research, summaries, inbox triage), approves actions.
- **Member/Viewer**: interacts in public channels with tightly limited capabilities.

## Core principles

- **Cloud-agnostic Docker-first**: run on any VPS/VM.
- **Zero-trust by default**: mTLS for admin API; strict RBAC and audit logs.
- **Context isolation**: every channel/subchannel has a separate policy prompt + permissions.
- **Markdown as the interface**: knowledge and state are stored as `.md` and surfaced in chat.
- **Human-in-the-loop actions**: anything risky requires explicit approval (or pre-approved templates).

## MVP (v1) scope

1) **Connectors**
   - Discord: ingest messages/threads + post responses + accept `/task`.
   - Telegram: admin control plane (approvals, tasks, pairing, notifications).

2) **Identity and RBAC**
   - Roles: `overlord`, `admin`, `operator`, `member`, `viewer`.
   - Admin pairing flow: admin DMs the bot → bot issues one-time token → admin approves in TUI → identities are linked.

3) **Workspaces + contexts**
   - Multiple workspaces.
   - Per-user private workspace (including non-admin users) + shared “community workspace”.
   - Per-channel and per-thread contexts (policy + memory + tasks isolated).

4) **Knowledge + retrieval**
   - File watcher for `.md` under `/data/workspaces/*`.
   - Vector indexing via `tobi/qmd`.
   - Chat commands for search and retrieval with citations to Markdown file paths.

5) **Tasks + schedules + proactivity**
   - Task graph (tasks can spawn tasks).
   - Cron schedules (server timezone).
   - Proactive triggers: `.md` changes can create “review/refresh” tasks.

6) **Email (IMAP/SMTP)**
   - Ingest emails into workspace as Markdown.
   - Draft replies require approval; templates can be “approved forever” per-skill.

7) **LLM integration**
   - Provider: `z.ai` Model API (pluggable adapter).
   - Policy-driven model selection and fallback per task type.

## Non-goals (v1)

- Full web UI.
- Non-Markdown attachments beyond `.md`.
- Hard multi-tenant SaaS separation (this is self-hosted; “workspaces” provide isolation inside an instance).

## Differentiators

- Policy-isolated agent contexts per channel/thread.
- Markdown-first storage and UX.
- Strong, practical security posture for self-hosting (mTLS + approvals + least privilege).

## Roadmap (high-level)

- **v1**: Discord + Telegram, RBAC/pairing, `.md` indexing (qmd), tasks/schedules, email triage/drafting, admin approvals.
- **v2**: X/Twitter connector, richer attachments (PDF/images), stronger sandbox backends, better observability.

## Open questions to resolve during PRD finalization

- X/Twitter integration strategy (official API vs alternative ingestion).
- Long-term sandbox backend (containerized runner vs microVM).

