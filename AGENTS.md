# Agent Runtime Development Guide

This document helps AI agents and developers work effectively with the agent-runtime codebase.

## Project Overview

`agent-runtime` is a channel-first orchestration runtime that connects chat and tool channels to a shared control plane. It provides routing, approvals, background task execution, workspace memory, and markdown retrieval.

**Key components:**
- **Gateway**: Message routing, command parsing, tool dispatch, approval handling
- **Orchestrator**: Background task queue and worker pool
- **Store**: SQLite persistence for tasks, contexts, objectives, pairings, approvals
- **Connectors**: External channel adapters (Telegram, Discord, IMAP, Codex/Cline/Gemini)
- **QMD**: Workspace markdown indexing and retrieval
- **Scheduler**: Recurring objectives and event-based triggers
- **HTTP API**: Programmatic admin interface
- **Admin TUI**: Fullscreen administrative interface

## Essential Commands

### Build and Run
```bash
# Build binary
make build

# Run runtime locally (uses default config)
make run

# Run admin TUI
make tui
```

### Testing
```bash
# Run all tests
make test

# Run specific package tests
go test ./internal/store/...
go test ./internal/gateway/...
```

### Docker Operations
```bash
# Start production stack
make compose-up

# Start dev stack
make compose-dev-up

# Stop production stack
make compose-down

# Stop dev stack
make compose-dev-down
```

### Documentation
```bash
# Check markdown links and linting
make docs-check
```

### Environment Setup
```bash
# Initialize env from example
cp .env.example .env

# Sync mTLS certificates (for local dev)
make sync-env
```

## Project Structure

```
cmd/agent-runtime/          # CLI entrypoint (main.go)
internal/
├── app/                   # Runtime bootstrap and orchestration wiring
├── cli/                   # Cobra CLI commands (serve, tui, qmd-sidecar, chat, version)
├── gateway/               # Message routing, commands, tools, approvals
├── orchestrator/          # Task queue and worker pool
├── store/                 # SQLite persistence layer
├── connectors/            # External channel adapters
├── httpapi/               # HTTP API handlers
├── qmd/                   # Markdown retrieval/index integration
├── scheduler/             # Objective scheduling service
├── watcher/               # Markdown file watcher
├── heartbeat/             # Health monitoring registry
├── config/                # Environment-based configuration
├── agent/                 # Agent execution (history, policy, tools)
├── llm/                   # LLM integration (OpenAI, Anthropic)
├── tui/                   # Admin terminal UI (Bubble Tea)
├── envsync/               # PKI/certificate synchronization
├── adminclient/           # HTTP API client for TUI
└── agenterr/              # Centralized error definitions
docs/                      # User/operator/developer documentation
context/                   # Agent system prompts and skills
scripts/                   # Utility scripts (docs-check, sync-env)
ops/                       # Operational configs (Caddy, security)
```

## Configuration

Configuration is environment-based via `.env` file. All config vars are prefixed with `AGENT_RUNTIME_`.

### Key Configuration Areas
- **Data**: `DATA_DIR`, `DB_PATH`, `WORKSPACE_ROOT`
- **Concurrency**: `DEFAULT_CONCURRENCY` (default: 5)
- **QMD**: Binary path, sidecar URL, indexing settings, timeouts
- **Objectives**: Poll interval, recovery settings
- **Heartbeat**: Enabled status, interval, stale threshold
- **Triage**: Enabled status, notification policy
- **Connectors**: Telegram/Discord/IMAP tokens, API endpoints
- **LLM**: Provider, model, API key, base URL, timeout
- **Sandbox**: Commands allowlist, runner, timeout, output limits
- **SMTP**: Email notification settings

### Loading Configuration
```go
cfg := config.FromEnv()
```

All environment variables are automatically converted to appropriate types (string, int, bool, CSV arrays).

## Database and Persistence

### SQLite Setup
- Driver: `modernc.org/sqlite` (CGO-free)
- Path: Configured via `DB_PATH` (default: `/data/agent-runtime/meta.sqlite`)
- Pragmas: WAL mode, foreign keys enabled, max open connections: 1

### Migration Pattern
The `AutoMigrate()` method uses a sequence of `CREATE TABLE IF NOT EXISTS` and `ALTER TABLE ADD COLUMN IF NOT EXISTS` statements. Alter statements handle duplicate column errors gracefully.

### Test Database Pattern
```go
func newTestStore(t *testing.T) *Store {
    t.Helper()
    dbPath := filepath.Join(t.TempDir(), "agent_runtime_test.sqlite")
    sqlStore, err := New(dbPath)
    if err != nil {
        t.Fatalf("open test store: %v", err)
    }
    t.Cleanup(func() { _ = sqlStore.Close() })
    if err := sqlStore.AutoMigrate(context.Background()); err != nil {
        t.Fatalf("migrate test store: %v", err)
    }
    return sqlStore
}
```

**Key patterns:**
- Use `t.Helper()` for helper functions
- Use `t.TempDir()` for temporary files
- Use `t.Cleanup()` for resource cleanup
- Always call `AutoMigrate()` in test setup

### Schema Conventions
- Primary keys: `id TEXT` (use UUID or deterministic IDs)
- Timestamps: `created_at TEXT`, `updated_at_unix INTEGER`
- Foreign keys: `REFERENCES table(id) ON DELETE CASCADE`
- NULL handling: Use helper functions like `nullIfEmpty()`, `nullIfZeroInt64()`

### Error Handling
Custom errors defined in `internal/agenterr/errors.go`:
- `ErrApprovalRequired`
- `ErrAccessDenied`
- `ErrAdminRole`
- `ErrToolNotAllowed`
- `ErrToolInvalidArgs`
- `ErrToolPreflight`

Store-specific errors include:
- `ErrTaskRunAlreadyExists` (duplicate run_key)
- `ErrTaskNotRunningForWorker` (worker-scoped completion prevention)

## Code Conventions

### General Go Patterns
- Use `context.Context` as first parameter in exported functions
- Use `*slog.Logger` for structured logging (JSON format in production)
- Use `fmt.Errorf()` with `%w` for error wrapping
- Use interface abstractions for testability (e.g., `Store`, `Engine`, `Retriever`)
- Use channels for async communication (see `orchestrator.Engine`)

### Error Wrapping Pattern
```go
if err != nil {
    return fmt.Errorf("operation failed: %w", err)
}
```

### ID Generation
```go
import "github.com/google/uuid"

id := uuid.NewString()
```

### Database Queries
Use parameterized queries with positional arguments:
```go
query := `SELECT id, title, status FROM tasks WHERE workspace_id = ? AND status = ?`
rows, err := s.db.QueryContext(ctx, query, workspaceID, status)
```

### Concurrency Patterns
- Use `sync.Once` for single-execution initialization
- Use `sync.WaitGroup` for worker coordination
- Use `sync.Mutex` for protecting shared state
- Use buffered channels with sensible capacity (e.g., `maxConcurrency * 50`)

## Testing Strategy

### Test Organization
- Test files: `*_test.go` alongside implementation
- Test helper functions: defined in test files, called with `t.Helper()`
- Test coverage: Focus on package-level tests plus end-to-end smoke checks

### Common Test Patterns

**Lifecycle testing:**
```go
func TestTaskLifecycle(t *testing.T) {
    sqlStore := newTestStore(t)
    ctx := context.Background()

    // Create
    if err := sqlStore.CreateTask(ctx, input); err != nil {
        t.Fatalf("create task: %v", err)
    }

    // Update
    if err := sqlStore.MarkTaskRunning(ctx, id, workerID, time.Now()); err != nil {
        t.Fatalf("mark running: %v", err)
    }

    // Verify
    loaded, err := sqlStore.LookupTask(ctx, id)
    if err != nil {
        t.Fatalf("lookup: %v", err)
    }
    if loaded.Status != expected {
        t.Fatalf("expected %s, got %s", expected, loaded.Status)
    }
}
```

**Error testing:**
```go
func TestCreateTaskRejectsDuplicate(t *testing.T) {
    sqlStore := newTestStore(t)
    ctx := context.Background()

    // First insert should succeed
    if err := sqlStore.CreateTask(ctx, firstInput); err != nil {
        t.Fatalf("first insert: %v", err)
    }

    // Second insert with duplicate run_key should fail
    err := sqlStore.CreateTask(ctx, secondInput)
    if !errors.Is(err, ErrTaskRunAlreadyExists) {
        t.Fatalf("expected ErrTaskRunAlreadyExists, got %v", err)
    }
}
```

### Integration Testing
After package tests:
1. Run runtime: `make run` or `make compose-up`
2. Exercise one connector path: `/status`, `/task ...`
3. Verify artifacts: Check `/data/workspaces/<workspace-id>/`

## TUI Development

### Architecture
- Framework: Bubble Tea (`github.com/charmbracelet/bubbletea`)
- Components: `bubbles` (table, textinput, viewport, spinner, help)
- Styling: `lipgloss` for rich terminal formatting
- Layout: Three persistent zones (sidebar/workbench/inspector)

### Key Files
- `internal/tui/model.go`: Main TUI model and state management
- `internal/tui/layout.go`: Layout rendering logic
- `internal/tui/theme.go`: Color and styling definitions
- `internal/tui/keymap.go`: Keyboard bindings
- `internal/tui/view_*.go`: Individual view implementations

### TUI Patterns
**Model updates:**
```go
func (m *model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyMsg:
        switch {
        case key.Matches(msg, m.keys.Quit):
            m.quitting = true
            return m, tea.Quit
        }
    case fetchMsg:
        // Handle async results
    }
    return m, nil
}
```

**Async operations:**
```go
// Return a command to initiate async operation
return m, fetchCmd()

// Handle result in update
case fetchResult:
    // Update model with result
```

**Focus management:**
- Zones: `focusSidebar`, `focusWorkbench`, `focusInspector`, `focusHelp`
- Cycle with `tab`/`shift+tab`
- Direct navigation with `1..5` keys

## Gateway and Tool System

### Message Flow
1. Connector receives message → `gateway.Service.HandleMessage()`
2. Command parsing → `/task`, `/search`, `/status`, `/route`, `/approve-action`, etc.
3. Triage/Reasoning → Classify intent and determine routing
4. Tool execution → Call appropriate tool with parameters
5. Response → Return formatted reply to channel

### Tool Registration
Tools are registered in `internal/gateway/tool_defs.go` and organized by class:
- `content`: Search, open, knowledge retrieval
- `code`: Code analysis and execution tools
- `filesystem`: File operations
- `network`: Web search, network operations
- `admin`: Administrative operations

### Tool Interface
```go
type Tool interface {
    Name() string
    Description() string
    Execute(ctx context.Context, args string) (string, error)
}
```

### Command Pattern
Commands are slash-prefixed and handled in `gateway/service.go`:
- `/task <prompt>`: Create background task
- `/search <query>`: Search workspace markdown
- `/open <path-or-docid>`: Open markdown document
- `/status`: Show system status
- `/monitor <goal>`: Create monitoring objective
- `/route <task-id> <class> [priority] [due-window]`: Override routing
- `/approve-action <id>`: Approve sensitive action
- `/deny-action <id> [reason]`: Deny sensitive action
- `/pending-actions`: List pending approvals

## Connectors

### Connector Interface
```go
type Connector interface {
    Name() string
    Start(ctx context.Context) error
}

type Publisher interface {
    Publish(ctx context.Context, externalID, text string) error
}
```

### Available Connectors
- **Telegram**: Poll-based bot integration
- **Discord**: Websocket-based bot with slash commands
- **Codex/Cline/Gemini**: Pattern-based pairing flow for CLI tools
- **IMAP**: Email ingestion with message tracking

### Connector Implementation Pattern
Each connector:
1. Connects to external service (using config from environment)
2. Receives messages and converts to `gateway.MessageInput`
3. Calls `gateway.Service.HandleMessage()` with input
4. Receives `MessageOutput` and publishes reply via `Publisher`

## HTTP API

### API Structure
- Router: `internal/httpapi/router_*.go` files
- Client: `internal/adminclient/client.go` (for TUI)
- Base path: `/api/v1/`

### Key Endpoints
- `GET /healthz`: Health check
- `GET /readyz`: Readiness check
- `GET /api/v1/heartbeat`: Heartbeat metrics
- `GET /api/v1/info`: Runtime information
- `POST /api/v1/chat`: Chat endpoint for testing
- `GET/POST /api/v1/tasks`: Task CRUD
- `POST /api/v1/tasks/retry`: Retry failed task
- `GET/POST /api/v1/pairings/start|lookup|approve|deny`: Pairing workflow
- `GET/POST /api/v1/objectives`: Objective CRUD and operations

## Scheduling and Objectives

### Objective Types
- **Cron-based**: Scheduled with cron expression
- **Event-based**: Triggered by specific events (e.g., markdown changes)
- **One-time**: Single execution

### Objective Lifecycle
1. Create objective with `trigger_type`, `cron_expr`, or `event_key`
2. Scheduler polls (`AGENT_RUNTIME_OBJECTIVE_POLL_SECONDS`)
3. Creates task when trigger fires
4. Tracks run statistics (count, success/failure, consecutive streaks)
5. Auto-pauses on repeated failures

### Run Key Pattern
Objective tasks use run keys to prevent duplicate execution:
```go
runKey := fmt.Sprintf("objective:%s:%d", objectiveID, scheduledUnix)
```

## Security Model

### Approval Flow
1. LLM agent proposes sensitive action
2. Action approval record created with status `pending`
3. Admin reviews via `/pending-actions` or TUI
4. Admin approves or denies with optional reason
5. If approved: Action executor runs, execution status updated
6. If denied: Record status, no execution

### Access Control
- Contexts have `is_admin` flag for admin channels
- Tools respect role-based access control
- Sensitive actions require explicit approval
- Command execution is sandboxed and allowlist-based

### mTLS
- Admin/API access intended to be protected by mTLS
- Certificate sync via `scripts/local-sync-pki-env.sh`
- Configured via environment variables for cert paths

## Development Workflow

### Making Changes
1. Read existing code in the affected package
2. Follow existing patterns (interfaces, error handling, logging)
3. Write tests alongside implementation
4. Run `make test` to verify
5. Run `make docs-check` if documentation affected
6. Test end-to-end with a connector

### Adding New Connectors
1. Implement `Connector` and `Publisher` interfaces
2. Add config to `internal/config/config.go`
3. Register connector in `internal/app/runtime_bootstrap.go`
4. Add connector-specific settings to `.env.example`
5. Document connector setup in `docs/channels/`

### Adding New Tools
1. Implement `Tool` interface
2. Register in `internal/gateway/tool_defs.go`
3. Add to appropriate class (content, code, filesystem, network, admin)
4. Add allowlist check if tool requires approval
5. Test with `/task` command

### Adding New API Endpoints
1. Add handler function in `internal/httpapi/router_*.go`
2. Register route in router setup
3. Add to API documentation in `docs/api.md`
4. Test with curl or TUI

## Known Issues and Gotchas

### SQLite Concurrency
- Max open connections set to 1 due to SQLite limitations
- WAL mode enabled for better concurrency
- Worker-scoped task completion prevents stale overwrites

### Time Handling
- Store uses `INTEGER` for Unix timestamps (`_unix` suffix)
- Store uses `TEXT` for datetime strings (e.g., `created_at`)
- Always use UTC: `time.Now().UTC().Unix()`

### Task Run Keys
- Run keys prevent duplicate task execution
- Unique index on `tasks.run_key`
- Used for objective tasks and idempotent operations

### Objectives Auto-Pause
- Objectives auto-pause on consecutive failures
- Check `auto_paused_reason` for pause cause
- Review `recent_errors_json` for failure patterns

### TUI Performance
- Limit data fetches (use pagination/filters)
- Debounce refresh operations
- Background loads use `pendingLoads` counter

### Configuration Loading
- All config loaded at startup via `config.FromEnv()`
- No runtime config reloading (requires restart)
- Missing environment vars use zero values (check for validation)

## Documentation Maintenance

When making changes:
1. Update `README.md` if user-facing behavior changes
2. Update `docs/api.md` if API changes
3. Update relevant docs under `docs/`
4. Update `CHANGELOG.md` for user-visible changes
5. Run `make docs-check` before committing

### Running Docs Checks
```bash
make docs-check
```

This runs:
- Markdown linting (markdownlint)
- Link checking (lychee)

## Key Dependencies

- `github.com/spf13/cobra`: CLI framework
- `github.com/charmbracelet/bubbletea`: TUI framework
- `modernc.org/sqlite`: CGO-free SQLite driver
- `github.com/google/uuid`: UUID generation
- `github.com/robfig/cron/v3`: Cron scheduling
- `github.com/gorilla/websocket`: Websocket support (Discord)

## Version Information

Current version: `0.1.0` (defined in `internal/cli/root.go`)

This is an early OSS release (pre-1.0), so breaking changes may occur between versions.
