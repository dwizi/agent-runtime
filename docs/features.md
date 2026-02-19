# Feature Guide

This page is the feature-level map of `agent-runtime`: what each feature does,
how to enable/configure it, where artifacts live, and where to go for deeper
docs.

## Capability Map

| Feature | What It Does | Primary Config | Deep Dive |
| --- | --- | --- | --- |
| Skills | Injects reusable behavior templates into agent system context | `AGENT_RUNTIME_SKILLS_GLOBAL_ROOT` (default `/data/.agents/skills`) | [Configuration](configuration.md) |
| MCP Integration | Connects remote MCP servers and exposes tools/resources/prompts | `AGENT_RUNTIME_MCP_CONFIG`, `AGENT_RUNTIME_MCP_*` | [MCP Servers](../ext/mcp/README.md), [Architecture](architecture.md) |
| External Plugins | Runs third-party action plugins (TinyFish, Resend, etc.) | `AGENT_RUNTIME_EXT_PLUGINS_CONFIG`, `AGENT_RUNTIME_EXT_PLUGIN_*` | [External Plugins](../ext/plugins/README.md) |
| Action Approvals | Human gate for sensitive actions | action approval commands and policy metadata | [Channels](channels/README.md), [Operations](operations.md) |
| Task Orchestration | Queues and executes background tasks via worker pool | `AGENT_RUNTIME_DEFAULT_CONCURRENCY` | [Objectives Flow](objectives-flow.md), [Architecture](architecture.md) |
| Objectives/Scheduler | Runs recurring or event-driven goals | `AGENT_RUNTIME_OBJECTIVE_*` | [Objectives Flow](objectives-flow.md) |
| Markdown Retrieval (QMD) | Workspace indexing/search + grounding context | `AGENT_RUNTIME_QMD_*` | [Configuration](configuration.md), [Memory Strategy](memory-context-strategy.md) |
| Connectors | Inbound/outbound channels (Telegram, Discord, Codex/Cline/Gemini, IMAP) | connector-specific env vars | [Channel Setup](channels/README.md) |
| Admin API | Programmatic runtime control and automation endpoints | `AGENT_RUNTIME_ADMIN_*` | [API Reference](api.md) |
| Admin TUI | Fullscreen operational console | TUI env vars + API access | [Development](development.md), [Operations](operations.md) |
| Sandbox & Isolation | Restricts command execution and wraps plugin execution | `AGENT_RUNTIME_SANDBOX_*` | [Configuration](configuration.md), [External Plugins](../ext/plugins/README.md) |

## Skills

Skills are markdown directives summarized into system prompt context. They let
you give reusable behavior instructions without hardcoding logic.

Current loading order:

1. Workspace context skills
2. Workspace role skills (`admin` or `public`)
3. Workspace common skills
4. Global context skills
5. Global role skills
6. Global common skills

Global default root:

- `/data/.agents/skills`

When to use:

- Stable operating procedures
- Team conventions
- Repeated execution patterns

Related docs:

- [Configuration](configuration.md) (loading order and envs)
- [Getting Started](getting-started.md) (bootstrap install patterns)

## MCP Integration

MCP adds external tool catalogs over HTTP/SSE and exposes them in runtime as
callable tools plus generic resource/prompt operations.

Key behavior:

- Config-driven server registry (`ext/mcp/servers.json`)
- Workspace override file support (`/data/workspaces/<id>/context/mcp/servers.json`)
- Periodic refresh with degraded-server retry
- Startup is non-fatal if a server is down

Primary use cases:

- External APIs exposed as MCP tools
- Dynamic resource reads (`resources/list`, `resources/read`)
- Prompt retrieval from MCP servers

Related docs:

- [MCP Servers](../ext/mcp/README.md)
- [Configuration](configuration.md)
- [Architecture](architecture.md)

## External Plugins

External plugins provide third-party action execution without embedding plugin
business logic in app internals.

Key behavior:

- Plugin manifests under `ext/plugins/**/plugin.json`
- Explicit enablement in `ext/plugins/plugins.json`
- Shared sandbox runner strategy
- Optional uv isolation with cached environments under `/data`

Use cases:

- Agentic web automation (`tinyfish`)
- Transactional email delivery (`resend_email`)
- Custom third-party actions from separate repos

Related docs:

- [External Plugins](../ext/plugins/README.md)
- [Configuration](configuration.md)

## Action Approvals and Safety

Sensitive actions require human approval before execution. This keeps autonomy
bounded by operator policy.

Operational commands:

- `/pending-actions`
- `/approve-action <id>`
- `/deny-action <id> [reason]`

Safety primitives:

- Tool class metadata (`general`, `knowledge`, `tasking`, `sensitive`, etc.)
- Approval-required flags
- Sandbox command allowlist

Related docs:

- [Channel Setup](channels/README.md)
- [Operations](operations.md)
- [Configuration](configuration.md)

## Task Orchestration

All meaningful work is represented as tasks in the control plane.

Lifecycle:

1. Message/command enters gateway
2. Task is persisted and queued
3. Worker executes with tool/action support
4. Result summary + artifact persisted to workspace

Artifacts:

- Chat logs under `logs/chats/...`
- Task outputs under `tasks/YYYY/MM/DD/...`
- Metadata in SQLite (`/data/agent-runtime/meta.sqlite`)

Related docs:

- [Architecture](architecture.md)
- [Objectives Flow](objectives-flow.md)
- [API Reference](api.md)

## Objectives and Proactivity

Objectives let runtime run recurring or trigger-based workflows.

Patterns:

- Cron-like recurring objectives
- Event-triggered objectives from markdown changes
- Failure-aware auto-pause and retry paths

Related docs:

- [Objectives Flow](objectives-flow.md)
- [Operations](operations.md)

## Markdown Retrieval and Memory

QMD indexing + memory summarization provide grounded responses and operational
continuity across long-running threads.

Core behavior:

- Workspace markdown indexing/search
- Chat-tail + summary memory extraction
- Prompt grounding budget controls

Related docs:

- [Configuration](configuration.md)
- [Memory Context Strategy](memory-context-strategy.md)
- [Memory Context Playbook](memory-context-playbook.md)

## Connectors and Channels

Connectors map external systems into a shared runtime message model.

Supported connector families:

- Telegram
- Discord
- Codex/Cline/Gemini channel pattern
- IMAP

Related docs:

- [Channel Setup](channels/README.md)
- [Install](install.md)

## Admin API and TUI

Two operational surfaces share the same backend semantics:

- HTTP API for automation and integrations
- Fullscreen TUI for human operations

Use API for:

- Chat and task automation
- Objective management
- Pairing/identity workflows

Use TUI for:

- Operational triage
- Pending approvals
- Objective and task visibility

Related docs:

- [API Reference](api.md)
- [Development](development.md)
- [Operations](operations.md)

## Feature Selection Guidelines

When deciding feature boundaries:

- Use **skills** for instruction/policy templates.
- Use **MCP** for remote tools/resources/prompt ecosystems.
- Use **external plugins** for executable third-party actions with explicit
  manifests and runtime contracts.
- Use **objectives** for proactive or recurring workflows.
- Use **sandbox + approvals** for risk containment.

If a capability needs both dynamic discovery and executable side effects, pair
MCP discovery with plugin/action execution, and keep approval boundaries clear.
