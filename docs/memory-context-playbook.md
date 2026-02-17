# Memory and Context Playbook

This playbook explains how to tune, validate, and troubleshoot the memory/context system in production.

## What to Tune First

Tune in this order:

1. Retrieval trigger strictness (quality and latency impact).
2. Token budgets by memory layer (cost and answer quality).
3. Summary refresh cadence (continuity vs. write churn).
4. QMD `top_k` and excerpt size (precision vs. noise).

## Baseline Configuration Profile

Recommended baseline for most deployments:

1. `AGENT_RUNTIME_LLM_GROUNDING_TOP_K=3`
2. `AGENT_RUNTIME_LLM_GROUNDING_MAX_PROMPT_TOKENS=2000`
3. `AGENT_RUNTIME_LLM_GROUNDING_USER_MAX_TOKENS=650`
4. `AGENT_RUNTIME_LLM_GROUNDING_MEMORY_SUMMARY_MAX_TOKENS=380`
5. `AGENT_RUNTIME_LLM_GROUNDING_CHAT_TAIL_MAX_TOKENS=300`
6. `AGENT_RUNTIME_LLM_GROUNDING_QMD_MAX_TOKENS=900`
7. `AGENT_RUNTIME_LLM_GROUNDING_MEMORY_SUMMARY_REFRESH_TURNS=6`

This profile favors stable continuity and moderate retrieval cost.

## Tuning Profiles

### Low-Latency Profile

Use when response speed is more important than deep recall.

Suggested changes:

1. reduce `TOP_K` to `2`
2. reduce `QMD_MAX_TOKENS` to `600`
3. reduce `MEMORY_SUMMARY_MAX_TOKENS` to `250`
4. increase retrieval strictness in prompt practices (ask users to reference files directly)

### High-Recall Profile

Use for complex ops/research threads with long context.

Suggested changes:

1. keep `TOP_K=3` or increase to `4`
2. increase `MAX_PROMPT_TOKENS` to `2400-3000`
3. increase `MEMORY_SUMMARY_MAX_TOKENS` to `500`
4. decrease summary refresh interval to `4` turns

### Cost-Control Profile

Use when token spend must be tightly constrained.

Suggested changes:

1. reduce `MAX_PROMPT_TOKENS` to `1400-1600`
2. keep `USER_MAX_TOKENS` stable to preserve instruction clarity
3. reduce summary and qmd budgets proportionally
4. keep `TOP_K <= 2`

## Diagnostics and Verification

## Quick Checks

1. Confirm chat logs are being written under `logs/chats/...`.
2. Confirm rolling summaries exist under `memory/contexts/...`.
3. Confirm qmd index excludes `logs/**` and is healthy.
4. Inspect runtime logs for grounding metrics fields.

## Behavior Checks

Use these prompt categories to verify strategy routing:

1. Small talk: should avoid qmd retrieval.
2. Continuity cue: should use summary/tail.
3. Workspace lookup request: should use qmd retrieval.
4. Generic non-workspace question: should not trigger qmd.

## Metrics to Track

Track at least these grounding metrics over time:

1. strategy distribution (`none`, `tail`, `qmd`)
2. qmd usage rate and average result count
3. summary refresh rate and average turns between refreshes
4. token share per section (`user`, `summary`, `tail`, `qmd`)
5. total prompt token estimate distribution

Target outcomes:

1. low qmd usage for generic conversation
2. high summary/tail usage for long-running threads
3. bounded total prompt size with low clipping loss

## Regression Test Set (Recommended)

Maintain a small fixed test set with expected behavior labels:

1. continuity prompts (expect `tail` / memory usage)
2. explicit doc lookup prompts (expect `qmd`)
3. generic questions (expect no qmd)
4. long thread continuation prompts (expect summary used)
5. edge prompts with URLs/domains (expect `qmd`)

Run this set after changes to:

1. retrieval cues
2. budget defaults
3. summary refresh logic
4. qmd integration behavior

## Common Failure Patterns

### 1) Over-Retrieval (Noisy Answers)

Symptoms:

1. unrelated doc snippets in answers
2. increased latency and token usage

Actions:

1. reduce `TOP_K`
2. reduce `QMD_MAX_TOKENS`
3. tighten user prompting for explicit file/context references

### 2) Under-Retrieval (Missed Facts)

Symptoms:

1. assistant misses obvious workspace facts
2. asks unnecessary clarifying questions despite available docs

Actions:

1. increase `TOP_K` or qmd budget
2. validate qmd index freshness
3. verify retrieval cues are present in user prompt style

### 3) Conversation Forgetfulness

Symptoms:

1. assistant loses thread objectives across long sessions
2. repeated follow-up questions for already-decided context

Actions:

1. increase summary budget
2. decrease summary refresh turn interval
3. increase summary source line budget

### 4) Prompt Bloat and Truncation

Symptoms:

1. large context sections clipped frequently
2. answer quality unstable on long inputs

Actions:

1. rebalance per-section token budgets
2. keep user budget protected
3. lower qmd and tail share first before lowering user share

## Change Management Guidance

When changing memory/context behavior:

1. change one knob group at a time
2. collect baseline metrics before change
3. run regression prompt set
4. compare latency, token usage, and answer quality
5. keep rollback-ready env snapshots

## Runbook: Safe Rollout Procedure

1. Apply config changes in staging.
2. Run targeted behavior checks.
3. Run full `go test ./...`.
4. Deploy to production with log monitoring.
5. Validate metric deltas after at least one normal traffic cycle.
6. Roll back if qmd usage or prompt tokens regress materially.

## Documentation Discipline

Whenever strategy logic changes:

1. update `docs/memory-context-strategy.md`
2. update this playbook's recommended profiles
3. update `.env.example` and `docs/configuration.md`
4. add/update tests covering the new decision path

