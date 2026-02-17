# Priority Roadmap (Started 2026-02-17)

This roadmap is scoped to the current platform gaps and is ordered by operator impact.

## P0 (In Progress)

1. Codex proactive outbound delivery
   - Status: `IN PROGRESS`
   - Outcome: task/objective/heartbeat notifications can be pushed to Codex sessions, not only logged.
   - Acceptance:
     - configurable Codex publish endpoint
     - publisher sends structured payload with timeout/auth support
     - fallback behavior remains safe when unset

2. Route command parity
   - Status: `IN PROGRESS`
   - Outcome: `/route` available as first-class command in connector command sync surfaces.
   - Acceptance:
     - command appears in Discord slash registration payload
     - command appears in Telegram command menu sync payload

## P1

1. Objective scheduling upgrade
   - Status: `PLANNED`
   - Outcome: add cron expressions while keeping interval schedules backward-compatible.

2. Research workflow as first-class task
   - Status: `PLANNED`
   - Outcome: dedicated research task type with source tracking and citation-ready artifacts.

3. Per-task model routing and fallback
   - Status: `PLANNED`
   - Outcome: route model/provider by task class (triage, research, drafting) with retries/fallback.

## P2

1. Task graph lineage
   - Status: `PLANNED`
   - Outcome: parent/child task relationships and traceability in storage/API.

2. Skills lifecycle hardening
   - Status: `PLANNED`
   - Outcome: consistent skill paths + approval/activation lifecycle.

3. Approval templates ("approve forever")
   - Status: `PLANNED`
   - Outcome: reusable approval policies per workspace/skill/template.
