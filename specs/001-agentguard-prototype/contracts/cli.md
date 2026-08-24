# Contract: `agentguard` CLI

Single binary. Exit codes: `0` success, `1` error, `2` daemon unreachable (for commands that need it), `3` setup/uninstall step failed. Global flags: `--json` (machine output on list/show/status commands), `--data-dir`, `--config` (overrides), `-v/--verbose`.

| Command | Purpose | Notes |
|---|---|---|
| `agentguard version` | Print version, engine_version, protocol_version, schema version | `--json` |
| `agentguard setup claude [--dry-run] [--settings <path>] [--no-service]` | Detect Claude, back up settings, install hooks, init DB, install+start service, inventory rules, self-test, report | Prints ✓/✗ per step; exit 3 on first required failure; idempotent |
| `agentguard uninstall claude [--keep-daemon] [--purge]` | Remove AgentGuard-owned hooks (backup first), stop/uninstall service, optionally purge data | Never touches unrelated settings |
| `agentguard hook claude` | Hook entry point (stdin→stdout) | See `claude-hooks.md`; always exit 0 |
| `agentguard daemon [run]` | Foreground daemon | bare `daemon` = `run` + hint |
| `agentguard daemon start\|stop\|restart\|status` | Lifecycle via service manager or unmanaged mode | `status --json` |
| `agentguard daemon install\|uninstall` | Register/unregister per-user service | Used by setup/uninstall |
| `agentguard approvals [--project <path>] [--all] [--inactive] [--json]` | List approvals (default: current project from cwd) | Table: `ID KIND ACTION TARGETS/SCOPE PROJECT USES LAST USED STATE ORIGIN` |
| `agentguard approval show <id> [--json]` | Full approval detail | semantic ops, envelope, targets, fingerprints (key + hash prefix + description), origin, provenance event, state, usage, last 10 events |
| `agentguard approval disable <id>` / `enable <id>` / `revoke <id>` | State transitions | Prints confirmation line; revoke is terminal |
| `agentguard approve <event-id> [--semantic] [--note <text>] [--json]` | Create approval from an evaluated audit event | Rejected with reason if event not approvable |
| `agentguard history [--blocked] [--asked] [--allowed] [--project <path>] [--session <id>] [--since <duration>] [--limit N] [--json]` | List audit events (default: last 50, all decisions) | Table: `ID TIME DECISION CLASS COMMAND RESOLVED REASON APPROVAL` |
| `agentguard history show <event-id> [--json]` | Full explanation of one event | raw → resolved chain, targets+scopes, effects+flags, fingerprints, decision, rule/approval, mismatch report, prompt/execution info |
| `agentguard status [--json]` | Daemon + integration + counters | |
| `agentguard doctor [--json]` | Health checks with suggested fixes | Binary path stable, service, daemon reachable, DB integrity, schema, Claude detected/version/hooks/backup, Git Bash (Windows), endpoint permissions, config parse |

## `--json` shapes
- `approvals` → array of `ApprovalSummary` (see ipc `list_approvals`).
- `approval show` / `approve` → `Approval` object (data-model §2.2 + `conditions` + `recent_events`).
- `history` → array of `AuditEventSummary`; `history show` → full `AuditEvent`.
- `status`, `daemon status`, `doctor`, `version` → objects with stable keys (`ok`, `checks[]{name, ok, detail, fix}` for doctor).

## Human output priorities
Always show: what is trusted (semantic op + envelope), scope, project, last used, use count, and *why* something was allowed/blocked (rule id or approval id, mismatch differences).

## Environment variables
`AGENTGUARD_DATA_DIR`, `AGENTGUARD_CONFIG_DIR`, `AGENTGUARD_RUNTIME_DIR`, `AGENTGUARD_ENDPOINT`, `AGENTGUARD_SELFTEST=1` (hook dry-run), `AGENTGUARD_TEST_MODE=1` + `AGENTGUARD_TEST_HOME` (tests only).
