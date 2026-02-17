# PRD — agent-runtime (Product Requirements Document)

Operational note:
- This file is a product planning artifact.
- For installation and operations, use:
  - `docs/install.md`
  - `docs/configuration.md`
  - `docs/operations.md`

## 1. Summary

Agent Runtime is a cloud-agnostic, security-first orchestration platform that connects **Discord**, **Telegram**, local **Markdown workspaces**, and **email inboxes** into a unified system. Agent Runtime runs without a web UI: operators work via chat commands and a **Charm TUI**. The system is **Markdown-native** (knowledge + state represented as `.md`) and **context-isolated** (each channel/subchannel has its own system prompt and permissions).

Key attributes:
- **Zero-trust posture**: edge TLS/certs via Caddy, mTLS for admin API, strict RBAC, audit logs.
- **Policy isolation**: per context prompt + tool/model allowlists.
- **Proactivity**: schedules and file-change triggers create tasks.
- **Retrieval**: vector search and document access powered by `tobi/qmd`.

## 2. Goals

### Product goals (v1)
- Run as a secure Docker Compose stack on any VPS/VM (“cloud agnostic”).
- Ingest Discord + Telegram events and produce responses in those channels.
- Provide a Telegram “admin control plane” with approvals and notifications.
- Provide a local/remote Charm TUI for provisioning and privileged operations.
- Maintain multiple workspaces with per-user private memory and shared community knowledge.
- Index all Markdown into vectors via `qmd` and expose Markdown-based search/access in chat.
- Support tasks, task graphs, objectives, schedules, and proactive triggers.
- Integrate with `z.ai` Model API via an adapter with task-based model selection/fallback.
- Handle IMAP ingestion and SMTP drafting with approval gates (and per-template permanent approvals).
- Support “deep research” workflows (competitor research, summaries, scheduled digests) with URL citations, delivered to admin contexts.

### Security goals (v1)
- Default-deny sensitive actions; require explicit approval by admin/operator policy.
- Strong identity linking for staff across connectors (pairing flow).
- Full auditability: who requested what, what was executed, what was approved, what was posted.

## 3. Non-goals (v1)

- Web UI.
- Indexing non-Markdown attachments (beyond `.md`).
- Full “public user account linking” across connectors (initially prioritize staff pairing).
- Perfect sandboxing of arbitrary binaries on day 1 (see phased sandbox strategy).

## 4. Personas and roles

Roles (RBAC):
- **overlord**: bootstrap/provision, manage CA/client certs, global policy, connector secrets.
- **admin**: manage policies, approve high-risk actions, manage operators and skills.
- **operator**: run workflows, approve medium-risk actions, manage tasks/schedules in assigned scopes.
- **member**: interact in public contexts with limited tools and memory.
- **viewer**: read-only interactions and visibility.

## 5. Core concepts

### 5.1 Workspace
A workspace is an isolated unit of storage and policy. It contains:
- Markdown knowledge (docs, memory, logs)
- tasks/schedules/objectives
- qmd index database (per workspace)
- connector bindings (Discord guild, Telegram group, etc.)

Workspaces:
- **Community workspace** (shared): per Discord guild / Telegram group.
- **Private workspace** (per user): one per user for personal memory, drafts, and private tasks.

### 5.2 Context (channel/subchannel isolation)
A **context** is a policy boundary inside a workspace. Examples:
- Discord channel
- Discord thread
- Telegram group
- “Admin hub” channel per connector (special context)

Each context has:
- A **system prompt**
- Tool allowlist / model policy
- Separate memory summary
- Separate task queue and schedules (optionally inheriting from workspace defaults)

### 5.3 Tasks, objectives, schedules
- **Task**: a unit of work assigned to the orchestrator within a context, with explicit inputs/outputs and optional tool calls.
- **Task graph**: a task can spawn follow-up tasks (subtasks).
- **Objective**: a longer-running goal that produces tasks over time.
- **Schedule**: cron-based triggers to create tasks.
- **Proactivity trigger**: non-cron events (e.g., file updates) that create tasks.

### 5.4 Skills (teach agents)
Skills are Markdown-defined workflows the orchestrator can use.

Proposed representation:
- Stored as `.md` in workspace, e.g. `/data/workspaces/<id>/skills/<name>.md`.
- YAML frontmatter defines:
  - id, name, description
  - required/optional inputs
  - allowed tools/commands (policy)
  - model routing hints (preferred model class)
  - approval requirements (needs approval / can be auto-approved)
  - prompt template / steps

Skill lifecycle:
- Create/edit via TUI or as files (file watcher picks up changes).
- Agent can propose a new skill; requires admin approval to activate.

## 6. Functional requirements

### 6.1 Connectors (Gateway)

**Discord connector (v1)**
- Ingest: messages, edits, thread messages.
- Post: replies, embeds, admin notifications.
- Commands:
  - `/task <text>` creates a task in that context.
  - Optional: `/search <query>`, `/open <path>` (or equivalent).

**Telegram connector (v1)**
- Ingest: group messages, admin DMs.
- Post: admin notifications, approval requests, task results.
- Commands:
  - `/task <text>`
  - `/approve <id>` / `/deny <id>`
  - `/admin-channel enable` (admin-only) marks a chat as an admin context.

**Admin DM routing**
- DMs from linked staff identities are mirrored into the connector’s configured admin hub context (“one per connector”).

**Future connectors (v2+)**
- X/Twitter: depends on API availability; design connector interface to support it.

### 6.2 Identity, pairing, and verification

Requirements:
- Support internal users with roles and scopes.
- Link external identities (Discord user ID, Telegram user ID) to internal users.

Admin pairing flow (required):
1) Staff user DMs bot: `pair`.
2) Bot responds with one-time token.
3) Staff enters token in TUI; TUI shows identity details (connector, user id, display name).
4) Admin approves; identity is linked to a chosen internal user; role applies immediately.

Anti-spoofing:
- Tokens are short-lived and single-use.
- Pairing approval requires mTLS-authenticated admin session.

### 6.3 RBAC + scoping

Everything is scoped:
- Workspaces
- Contexts (channels/threads)
- Skills
- Tools and commands
- Models
- Email accounts

RBAC requirements:
- Default deny: members cannot execute risky tools.
- Admin-only approval channel(s) per connector.
- Audit trail for all permission changes and approvals.

### 6.4 Markdown-first storage

Source of truth:
- Filesystem under `/data/workspaces/<workspace>/...`
- Chat events persisted as Markdown logs

Required generated Markdown:
- Context memory summaries (e.g., `memory.md`)
- Append-only chat logs (e.g., `logs/discord/<channel>.md`)
- Tasks and approvals as `.md` records (optionally mirrored from DB)

### 6.5 Indexing and retrieval via `tobi/qmd`

Requirements:
- Index all `.md` content in each workspace into a per-workspace vector index.
- On `.md` change, queue re-index for that workspace (debounced).
- Provide retrieval commands:
  - `/search <query>` returns top-k results with:
    - snippet
    - file path
    - workspace/context scope
  - `/open <path>` returns Markdown content (permission-gated)

Operational constraints (acknowledged):
- qmd downloads and uses an embedding model (large) and stores index in SQLite.
- Agent Runtime must manage indexes **per workspace** and not cross-contaminate data.

Implementation approach (v1 proposal):
- Run `qmd` as a controlled subprocess (or internal sidecar) with:
  - `HOME` set to a workspace-specific directory to isolate `~/.cache/qmd`.
  - Working directory set to workspace root.
- Store qmd artifacts under `/data/workspaces/<id>/.qmd/`.

### 6.6 Task engine and execution

Task capabilities:
- LLM reasoning steps (with policy context)
- Tool invocations (see sandbox)
- Message publishing back to connectors
- Creating subtasks / follow-ups

Concurrency:
- Default max concurrent tasks per workspace: **5** (configurable).
- Per-tool throttles (e.g., email send single-flight).

### 6.7 Schedules and proactivity

Schedule requirements:
- Cron schedules (server timezone).
- Schedule creates tasks in a target context.

Proactivity triggers:
- File watcher emits “file changed” event → creates a “review change” task.
- Agent evaluates whether to:
  - update memory summary
  - create follow-up tasks
  - notify admins (approval-gated if externally visible)

### 6.8 Email (IMAP/SMTP)

Requirements:
- Configurable mailboxes per workspace (IMAP + SMTP).
- Ingest emails into Markdown in the workspace.
- Draft replies can be generated by the agent.
- Sending requires approval by default.
- Admin can “approve forever” per skill/template (e.g., “Weekly competitor digest”) within a workspace.

### 6.9 LLM adapters and routing (z.ai)

Requirements:
- Provider adapter interface supports:
  - model name
  - temperature/max tokens
  - streaming (optional)
  - retries/timeouts
  - tool-use (function calling) (optional, depending on model)
- Policy-driven routing:
  - per task type (research, summarization, triage, drafting)
  - per context (admin vs public)
  - fallback model/provider on failure

v1 provider:
- `z.ai` Model API

Future:
- additional providers behind the same interface.

### 6.10 Tool execution sandboxing

Requirement:
- LLM can request tool calls, but execution is constrained by:
  - per-context allowlist
  - per-skill allowlist
  - OS isolation boundary
  - approval gates (for risky tools)

Phased sandbox strategy:
- **v1**: container-isolated “tool runner” service (separate from orchestrator) that only exposes a fixed tool API. It implements allowlists, timeouts, and network restrictions.
- **v2**: stronger backends (e.g., microVM or gVisor-based runner), plug-compatible via the same tool API.

Suggested v1 tool set (default allowlist; admin can change):
- `http_fetch` (Go-native fetcher): GET with domain allowlist, content-type limits, caching.
- `html_to_markdown` (sanitized conversion).
- `md_read` / `md_write` (scoped file access).
- `qmd_search` (controlled access to qmd index).
- Optional, only in admin contexts: `curl` via sandbox runner.

Note: user mentioned Vercel Labs `just-bash` as an acceptable sandbox component; evaluate whether to use it inside the tool runner for a safe “virtual shell” UX.

### 6.11 Deep research and competitor intelligence

Requirements:
- Provide a first-class “research” task type that can:
  - collect sources from the web (allowlisted fetching)
  - extract and normalize content into Markdown notes
  - produce summaries with **URL citations**
  - publish results to an admin context (Telegram admin control plane by default)

Deliverables (v1):
- `/task research <topic>`: runs a research task and posts a structured report to the admin hub.
- Scheduled digests (cron): e.g., “weekly competitor update” delivered to admins.
- Research artifacts stored in workspace as `.md` (sources, notes, final report) and indexed by qmd.

## 7. System architecture (proposed)

### 7.1 Services (Docker Compose)

- **caddy**
  - TLS certs (ACME HTTP-01 on ports 80/443)
  - reverse proxy to agent-runtime HTTP API
  - optional mTLS enforcement for admin endpoints
  - security headers, rate limits

- **agent-runtime (Go)**
  - connectors (Discord/Telegram)
  - orchestration engine
  - task engine + scheduler + file watcher
  - admin API for TUI
  - SQLite metadata store

Optional (recommended for isolation as system grows):
- **agent-runtime-toolrunner**
  - executes allowlisted tools/commands in a restricted container
- **agent-runtime-qmd**
  - encapsulates qmd runtime and model/index artifacts

### 7.2 Data stores

- SQLite metadata DB (in `/data/agent-runtime/meta.sqlite`)
- Per-workspace directories under `/data/workspaces/<id>/...`
- Per-workspace qmd index (isolated) under `/data/workspaces/<id>/.qmd/`

### 7.3 Networking

Inbound:
- 80/443 → Caddy → agent-runtime

Internal:
- Compose private network for internal services

Admin access:
- TUI connects to admin endpoints over TLS, protected by mTLS.

## 8. Security requirements

### 8.1 Edge security (Caddy)
- Automatic TLS cert provisioning.
- HSTS (configurable).
- Rate limiting for webhook endpoints.
- Separate admin endpoints with mTLS.

### 8.2 Admin API (mTLS)
- Agent Runtime maintains an internal CA.
- TUI enrolls a client cert via an out-of-band bootstrap secret (overlord-only).
- Client cert required for admin endpoints; RBAC checks still apply.

### 8.3 Audit logs
Record:
- message ingests (metadata, not necessarily full content depending on config)
- task creation/execution
- tool invocations
- approvals/denials
- outbound posts/emails
- RBAC changes

## 9. Observability (v1)
- Structured logs (JSON).
- Health endpoints (`/healthz`, `/readyz`).
- Basic metrics (optional): task queue size, connector lag, qmd index status.

## 10. Deployment requirements

### 10.1 Minimum host requirements (initial estimate)
- Disk: enough for workspace content + qmd model/index (qmd model can be large).
- RAM: depends on embedding model; document clearly.

### 10.2 Base images
Constraint:
- Prefer minimal distro compatible with qmd runtime dependencies (glibc).

Recommendation:
- Use a minimal Debian-based image for services that need glibc.
- Keep the Go service small via multi-stage builds; consider distroless for the Go-only parts if qmd is split into its own service.

## 11. Milestones (proposed)

1) Repo + scaffolding: config model, SQLite schema, workspace layout.
2) Telegram connector + TUI pairing + RBAC (end-to-end admin control plane).
3) Discord connector + contexts + `/task`.
4) File watcher + Markdown projections (logs/memory/task docs).
5) qmd integration: per-workspace index + `/search` + `/open`.
6) Task engine + scheduler + proactivity on file change.
7) Email (IMAP ingest + SMTP draft/send w/ approvals).
8) Tool runner sandbox + allowlists + audit.

## 12. Open issues / risks

- qmd runtime/model footprint (size, resource use) impacts “small image” goals.
- X/Twitter integration may require paid API access; design connector interface but defer implementation until constraints are known.
- Sandbox backend portability: container-only isolation in v1; stronger isolation may require host/kernel support in v2.

## 13. References

- qmd: https://github.com/tobi/qmd
- z.ai Model API: https://z.ai/model-api
