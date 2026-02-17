# Memory and Context Strategy

This document defines how Agent Runtime builds context for each response, why it is designed this way, and how each memory layer works.

## Objectives

Our context strategy is designed to satisfy four requirements at the same time:

1. Keep short-turn responses fast and relevant.
2. Preserve conversational continuity across long threads.
3. Pull workspace knowledge only when needed.
4. Keep prompt construction deterministic and tuneable.

## Design Principles

1. Single context assembly path.
2. Layered memory (summary, tail, retrieval) instead of full transcript replay.
3. Explicit budget control for each context layer.
4. Context isolation per workspace and per external channel/context.
5. Observable behavior through structured metrics.

## End-to-End Prompt Pipeline

At runtime, the response path is:

1. `promptpolicy` builds system-level instructions.
2. `grounded` builds user-context payload (memory + retrieval).
3. LLM client sends one `system` message and one `user` message.

The grounding stage is the single owner of memory assembly.

## Memory Layers

### 1) Raw Chat Logs (Source of Conversational Truth)

Inbound/outbound messages are appended to:

- `data/workspaces/<workspace_id>/logs/chats/<connector>/<external_id>.md`

This is append-only conversation history used by memory processing.

### 2) Rolling Context Summary (Long-Horizon Conversation Memory)

Grounding maintains a rolling summary per context at:

- `data/workspaces/<workspace_id>/memory/contexts/<context_key>.md`

Key behavior:

1. Summary refresh is turn-based, not per message.
2. Turn count is derived from inbound entries in the chat log.
3. Refresh runs when summary is missing or stale by configured turn interval.
4. Summary captures recent user intents, assistant actions, and open questions.

Why this exists:

- Chat tails are good for recency but weak for long arcs.
- Summaries preserve thread continuity without sending full logs.

### 3) Chat Tail (High-Recency Memory)

Grounding extracts a compact tail from the chat log.

Tail behavior:

1. Bounded by line count and byte budget.
2. Tool log metadata is filtered out.
3. Used to preserve the latest local state and tone.

### 4) QMD Retrieval (Workspace Knowledge Memory)

When retrieval is needed, grounding performs:

1. `Search(workspace, query, top_k)`
2. `OpenMarkdown` on selected documents
3. excerpt + snippet packaging under budget

QMD retrieval is for workspace knowledge, docs, policies, runbooks, etc.

## Retrieval Strategy and Decision Logic

Grounding applies a strategy decision to each user input:

- `none`: do not retrieve workspace docs.
- `tail`: prioritize conversation memory cues.
- `qmd`: retrieve workspace docs.

Strategy cues:

1. Small talk -> `none`.
2. Explicit continuity cues (`as we discussed`, `continue from`, etc.) -> `tail`.
3. Strong workspace/document cues (`docs`, `readme`, `which file`, `policy`, `pricing`, domain/url) -> `qmd`.
4. Implicit questions trigger `qmd` only when they contain workspace anchors (`repo`, `file`, `workspace`, `path`, `markdown`, etc.).

This tightened heuristic avoids retrieval for generic questions that do not need workspace memory.

## Unified Context Assembly Order

Grounding builds response input in this order:

1. User text (clipped to user budget)
2. Context memory summary (if available)
3. Recent conversation memory tail (if available)
4. Retrieved workspace context (if strategy is `qmd`)
5. Optional response behavior hint (connector-specific acknowledgement)

This order is intentional:

- summary gives durable state,
- tail gives recency,
- qmd adds factual workspace evidence.

## Budgeting Model

Context is constrained by explicit token budgets per section.

Main budgets:

1. total prompt tokens
2. user segment tokens
3. summary tokens
4. chat-tail tokens
5. qmd context tokens

Each section is clipped independently first, then final prompt clipping applies.

Byte clipping still exists as a final guardrail for transport/runtime safety.

## Isolation and Boundaries

### Workspace Isolation

All memory and retrieval operate on the message's workspace ID.

### Context Isolation

Rolling summary is keyed by context ID (or connector/external fallback key).

### Retrieval Scope Boundary

`logs/**` is excluded from qmd indexing by default, so chat logs are not mixed into vector retrieval memory.

## Why We Removed Gateway-Level History Prefixing

Historically, some gateway paths prefixed `CONVERSATION HISTORY` manually before invoking the agent.

That caused two problems:

1. Duplicated context (gateway history + grounded memory).
2. Inconsistent behavior across execution paths.

Now memory assembly is centralized in grounding, reducing drift and prompt bloat.

## Observability

Grounding emits structured metrics for each assembled prompt, including:

1. selected strategy and reason
2. whether summary/tail/qmd were used
3. qmd result count
4. summary refresh flag and turn count
5. token estimates per section and total

This is the foundation for quality and cost tuning.

## System Prompt and Behavioral Policy Context

Memory context is only one part of response behavior.

`promptpolicy` still layers:

1. role baseline (admin/public)
2. global/workspace/context prompt directives
3. SOUL directives
4. skill templates
5. action policy directives

Grounded memory is appended to the user-side payload and does not replace policy controls.

## Failure Modes and Fallback Behavior

1. If chat log cannot be read, summary/tail memory is skipped.
2. If qmd search fails or is unavailable, retrieval context is skipped.
3. If context is missing, the base user prompt still proceeds.

The system degrades gracefully instead of hard-failing normal responses.

## Configuration Surface (Memory and Context)

Primary env controls:

1. `AGENT_RUNTIME_LLM_GROUNDING_TOP_K`
2. `AGENT_RUNTIME_LLM_GROUNDING_MAX_DOC_EXCERPT_BYTES`
3. `AGENT_RUNTIME_LLM_GROUNDING_MAX_PROMPT_BYTES`
4. `AGENT_RUNTIME_LLM_GROUNDING_MAX_PROMPT_TOKENS`
5. `AGENT_RUNTIME_LLM_GROUNDING_USER_MAX_TOKENS`
6. `AGENT_RUNTIME_LLM_GROUNDING_MEMORY_SUMMARY_MAX_TOKENS`
7. `AGENT_RUNTIME_LLM_GROUNDING_CHAT_TAIL_MAX_TOKENS`
8. `AGENT_RUNTIME_LLM_GROUNDING_QMD_MAX_TOKENS`
9. `AGENT_RUNTIME_LLM_GROUNDING_CHAT_TAIL_LINES`
10. `AGENT_RUNTIME_LLM_GROUNDING_CHAT_TAIL_BYTES`
11. `AGENT_RUNTIME_LLM_GROUNDING_MEMORY_SUMMARY_REFRESH_TURNS`
12. `AGENT_RUNTIME_LLM_GROUNDING_MEMORY_SUMMARY_MAX_ITEMS`
13. `AGENT_RUNTIME_LLM_GROUNDING_MEMORY_SUMMARY_SOURCE_MAX_LINES`

## Operational Outcome

This strategy balances:

1. Response quality (less forgetting, better continuity).
2. Cost and latency (bounded retrieval + budgets).
3. Determinism (single assembly path).
4. Debuggability (metrics + explicit structure).

