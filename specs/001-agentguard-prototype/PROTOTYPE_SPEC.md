# AgentGuard — Prototype Specification

**Document**: `PROTOTYPE_SPEC.md` — source of truth for the prototype implementation
**Feature**: `specs/001-agentguard-prototype`
**Status**: Draft v0.1
**Date**: 2026-08-15

---

## 0. Conventions used in this document

- **MUST / MUST NOT / SHOULD / MAY** follow RFC 2119.
- **INVARIANT** blocks are non-negotiable system properties. Every invariant MUST be covered by at least one automated test.
- Terms: **Agent** = an AI coding agent (Claude Code in the prototype). **Adapter** = the agent-specific integration layer. **Daemon** = the AgentGuard background service. **Workspace (W)** = the project root determined by §16.2. **Action** = one agent tool invocation submitted for evaluation.
- Facts about Claude Code marked **[verified]** were checked against the Claude Code hooks reference and the installed Claude Code build 2.1.233 during authoring (2026-08-15). Facts marked **[assumption]** MUST be verified during implementation; each has an entry in Appendix B.
- This document is a specification, not an implementation plan. It intentionally contains no source code. Names such as `ActionRequest` are canonical concept names; implementers choose the concrete Go representation.

---

## 1. Overview

AgentGuard is a local, cross-platform runtime permission and control layer that sits between an AI coding agent and the operating system. It intercepts the agent's command executions, resolves what the command will actually do (including resolving package-manager scripts and build-tool wrappers), classifies the resulting effects and targets, and decides **ALLOW**, **ASK**, or **BLOCK** deterministically. It remembers user approvals as **semantic approvals** — the resolved effects the user consented to — not as command strings.

The prototype ships as one Go executable, `agentguard`, that acts as CLI, agent hook client, and background daemon. It integrates with exactly one agent, Claude Code, through Claude Code hooks, and stores all state locally in SQLite.

Design principles (referenced throughout as **P1–P14**):

| # | Principle |
|---|-----------|
| P1 | Local-first: no cloud, no account, no telemetry. |
| P2 | Deterministic security decisions: same inputs → same decision. |
| P3 | No runtime LLM dependency. |
| P4 | Fail safe: when uncertain, ASK; never guess ALLOW. |
| P5 | Approve semantic effects, not strings. |
| P6 | Re-resolve mutable wrappers/scripts before reusing an approval. |
| P7 | Cross-platform core (macOS, Linux, Windows) from day one. |
| P8 | Thin agent adapters; the core never sees agent-specific data. |
| P9 | One daemon, one policy engine. |
| P10 | One executable for distribution. |
| P11 | Easy install is a first-class requirement. |
| P12 | No hidden unrestricted fallback. |
| P13 | Every ALLOW and BLOCK is explainable from the audit log. |
| P14 | Prototype only what proves the core hypothesis. |

---

## 2. Problem Statement

AI coding agents ask the user for permission before executing commands. Users approve the same kinds of commands repeatedly and respond either with permission fatigue or by granting broad/bypass permissions. Existing allowlists remember **command strings or prefixes**, so they trust a command whose *underlying behavior* has changed:

1. The user approves `npm run cleanup` while `package.json` contains `"cleanup": "rm -rf ./dist"`.
2. `package.json` later becomes `"cleanup": "rm -rf ~/Documents"`.
3. A string allowlist still allows `npm run cleanup`.

Prefix allowlists are also trivially bypassed by shell composition (`./gradlew test && rm -rf ~`).

---

## 3. Product Goal

Prove one hypothesis: **AgentGuard significantly reduces repeated permission prompts while remaining strictly safer than a string/prefix command allowlist.**

The primary proof is the invalidation scenario (§29, scenario S10, and §30): a semantic approval for `npm run cleanup` (resolved to `rm -rf ./dist`, `DELETE` of `WORKSPACE_GENERATED`) auto-allows the equivalent action in later sessions, and stops matching — yielding BLOCK or ASK with an explanation — as soon as the resolved script, target, or scope changes.

---

## 4. Prototype Scope

In scope:

- One executable `agentguard` (CLI + hook client + daemon) for macOS, Linux, Windows (arm64 and amd64 build targets for each).
- Claude Code adapter using hooks (`PreToolUse`, `PermissionRequest`, `PostToolUse`; `ConfigChange` optional) for the `Bash` tool and, when present, the `PowerShell` tool.
- Automatic setup/uninstall of the Claude Code integration and the per-user daemon service.
- Shell parsing for POSIX (bash/zsh/sh syntax), PowerShell, and cmd.exe, limited to the constructs in §14.
- Command recognizers listed in §15.4 and script/wrapper resolution for npm/pnpm/yarn scripts, Gradle tasks, and Maven goals.
- Path normalization, scope classification, effect model, deterministic policy engine, hard safety baseline.
- Semantic approval memory (EXACT and SEMANTIC kinds), deterministic matching, invalidation, audit log, SQLite persistence with versioned schema.
- CLI management commands (§25).
- Cross-platform automated tests and the end-to-end acceptance scenarios (§28–§29).

Everything in §5 is out of scope.

---

## 5. Non-Goals

The prototype MUST NOT include, and acceptance criteria MUST NOT depend on:

- Adapters for Codex, OpenCode, Cursor, VS Code, JetBrains, or any agent other than Claude Code.
- Desktop GUI, tray/menu-bar UI, browser automation, mobile apps.
- Cloud sync, accounts, teams/RBAC, enterprise policies, remote approval.
- LLM-based security decisions of any kind (P3).
- Automatic semantic generalization of approvals without explicit user confirmation.
- Deep analysis of Docker, Kubernetes, SQL/databases.
- Backups, rollback, filesystem snapshots, Git snapshots/worktrees, BACKUP/SNAPSHOT/ROLLBACK decision states.
- A full shell interpreter; static analysis of arbitrary programs or of arbitrary Python/Node/Java source.
- Sandboxing of processes; gating of agent tools other than shell-command tools (e.g. Claude's `Write`/`Edit` tools are not gated in the prototype — see §27).

These MAY be mentioned as future directions but MUST NOT expand the prototype.

---

## 6. Architecture

### 6.1 Runtime topology

```
Claude Code ──(hook: JSON on stdin/stdout)──▶ agentguard hook claude   (Claude adapter, thin)
                                                    │  local IPC (UDS / named pipe), JSON
                                                    ▼
                                            agentguard daemon
            ┌───────────────────────────────────────────────────────────────┐
            │ Context (workspace/project, home, temp, caches)               │
            │ Parser (posix | powershell | cmd) ─▶ shared command model      │
            │ Recognizers + Resolver (scripts, wrappers, fingerprints)      │
            │ Normalizer (paths, scopes, effects) ─▶ ResolvedAction         │
            │ Policy Engine (hard rules → baseline → approvals → default)   │
            │ Approval Memory (create / match / invalidate)                 │
            │ Audit                                                          │
            └───────────────────────────────────────────────────────────────┘
                                                    │
                                                  SQLite (local file, WAL)
```

The daemon owns policy evaluation, approval matching and persistence, audit history, normalization, resolution, caches, and runtime context. Adapters convert agent-specific input into the generic `ActionRequest` (§13) and agent-specific output from the generic `Decision`.

### 6.2 Package layout (Go)

```
cmd/agentguard/            main: sub-command dispatch only
internal/
  action/                  canonical domain model (§13): ActionRequest, ResolvedAction, Effect, Target, Decision …
  adapter/                 Adapter interface + registry
  adapter/claude/          Claude Code hook I/O, settings discovery/merge, rule matching, setup/uninstall steps
  parser/                  shared command model, Dialect interface, Parser registry
  parser/posix/            POSIX/bash/zsh parser (mvdan.cc/sh AST walk recommended)
  parser/powershell/       minimal PowerShell parser
  parser/cmd/              minimal cmd.exe parser
  resolver/                recognizers (fs, git, npm/pnpm/yarn, gradle, maven, curl, wrappers), fingerprints
  scope/                   path normalization, canonicalization, scope classification, generated-root detection
  policy/                  hard rules, baseline rules, decision assembly, explanations
  approval/                approval model, creation, matching, mismatch reports
  audit/                   audit event model and recording
  storage/                 SQLite access, migrations, repositories
  daemon/                  server loop, request handlers, caches, lifecycle (run/lock/pidfile)
  ipc/                     protocol types, transport abstraction, client, server
  platform/                Platform interface + per-OS implementations (dirs, endpoints, service manager, exec discovery)
  config/                  config file loading, defaults
  version/                 version, engine_version, protocol_version constants
  cli/                     cobra command implementations (root, version, daemon, setup/uninstall, hook, approvals, approve, history, status, doctor)
  logging/                 slog setup (JSON file handler with rotation for daemon/hook logs; text handler for CLI)
test/e2e/                  end-to-end scenario tests (§29)
```

Dependency direction MUST point inward: `adapter/*` → `action`, `ipc`, `config`, `platform`; `cli` → `ipc`, `config`, `platform`, `adapter/claude`; `daemon` → `parser`, `resolver`, `scope`, `policy`, `approval`, `audit`, `storage`, `platform`; nothing in the core imports `adapter/*` or `cli` (enforced by a `depguard` lint rule and an invariant test, I-7). Per-OS platform code lives as build-tagged files inside `internal/platform` (`dirs_<os>.go`, `pathrules_<os>.go`, `service_<os>.go`, `spawn_windows.go`). `runtime.GOOS` checks are confined to `platform/`, `parser/*` dialect selection helpers, and `scope/` path rules; business logic MUST consume the `Platform` interface (§8).

---

## 7. Technology Choices

| Concern | Choice | Notes |
|---|---|---|
| Language | Go (≥ 1.22) | Single static binary, cross-compilation, `slog`. |
| Storage | SQLite via a pure-Go driver (`modernc.org/sqlite` recommended) | CGO MUST NOT be required for any target. WAL mode, `busy_timeout` ≥ 5 s. |
| Logging | `log/slog`, JSON handler to a size-rotated file in the data dir; text handler on stderr for CLI | Never log secrets; raw commands are logged (they are already in the audit). |
| Config | TOML file, optional, at `<config dir>/config.toml` | All defaults work without a file (§12.6). |
| POSIX parsing | `mvdan.cc/sh/v3/syntax` recommended (parse only, never interpret) | The AST walker MUST whitelist supported node types (§14). |
| Windows named pipes | `github.com/Microsoft/go-winio` recommended | Behind the `ipc` transport abstraction. |
| Service management | Platform-specific (§9.4); a cross-platform helper library MAY be used if it supports per-user services | Core logic MUST NOT depend on the library. |
| CLI | Any small sub-command library or the standard library | One binary (P10). |
| Tests | `go test`, table-driven; `t.TempDir()` for all filesystem fixtures | CI matrix: ubuntu-latest, macos-latest, windows-latest. |
| Release | GitHub Actions + GoReleaser (or equivalent) | Targets: darwin/arm64, darwin/amd64, linux/arm64, linux/amd64, windows/arm64, windows/amd64. Package repositories (Homebrew tap, winget manifest, install script) MAY be stubbed. |

New third-party dependencies beyond the ones recommended here MUST be justified in the implementation plan.

---

## 8. Cross-Platform Design

### 8.1 Platform interface

`platform.Platform` MUST provide (one implementation per OS, selected once at startup):

| Method (concept) | Purpose |
|---|---|
| `DataDir()` | AgentGuard state: DB, logs, backups, `daemon.json`. |
| `ConfigDir()` | `config.toml` location. |
| `RuntimeDir()` | Socket/pipe location, pid/lock files. |
| `HomeDir()` | Canonical user home. |
| `TempDir()` | Canonical platform temp dir for the user. |
| `IPCEndpoint()` | Default endpoint (§10.1). |
| `ServiceManager()` | Install/Uninstall/Start/Stop/Restart/Status of the per-user daemon service (§9.4). |
| `FindExecutable(name)` | PATH lookup honoring platform rules (`PATHEXT` on Windows). |
| `DefaultShellDialect()` | Native shell dialect for the OS. |
| `PathRules()` | Case sensitivity, separators, drive/UNC handling, system roots, standard home sub-directories, sensitive paths, tool-cache paths (§16). |
| `SelfExecutablePath()` | The **stable** absolute path of the running binary — the one that will still be correct after an upgrade — used for hook commands, service definitions and lazy start. In order: the `PATH` entry that `os.SameFile`-matches the resolved executable; else the unresolved `os.Executable()` when it is a symlink into a versioned package-manager directory (`Cellar/`, `versions/`, WinGet `Packages/`); else the resolved path. Resolving unconditionally would record the version-pinned path a package manager deletes on upgrade, leaving hooks pointing at nothing (feature 002). |

### 8.2 Default directories

| OS | DataDir | ConfigDir | RuntimeDir |
|---|---|---|---|
| macOS | `~/Library/Application Support/AgentGuard` | same as DataDir | `$TMPDIR/agentguard-<uid>` (fallback `/tmp/agentguard-<uid>`) |
| Linux | `$XDG_DATA_HOME/agentguard` (default `~/.local/share/agentguard`) | `$XDG_CONFIG_HOME/agentguard` (default `~/.config/agentguard`) | `$XDG_RUNTIME_DIR/agentguard` (fallback `/tmp/agentguard-<uid>`) |
| Windows | `%LOCALAPPDATA%\AgentGuard` | `%APPDATA%\AgentGuard` | n/a (named pipe) |

Environment overrides for tests and advanced use: `AGENTGUARD_DATA_DIR`, `AGENTGUARD_CONFIG_DIR`, `AGENTGUARD_RUNTIME_DIR`, `AGENTGUARD_ENDPOINT`. Directories MUST be created with owner-only permissions (0700 on POSIX; default user ACL on Windows).

### 8.3 Platform matrix of behaviors

| Concern | macOS | Linux | Windows |
|---|---|---|---|
| IPC | Unix domain socket | Unix domain socket | Named pipe |
| Service | launchd LaunchAgent (`~/Library/LaunchAgents/com.agentguard.daemon.plist`, `KeepAlive`) | `systemd --user` unit (`~/.config/systemd/user/agentguard.service`); fallback: unmanaged (§9.4) | Per-user logon autostart (§9.4); fallback: unmanaged |
| Agent shell dialect for Claude `Bash` tool | posix | posix | posix (Claude Code runs the Bash tool through Git Bash **[verified]**) |
| Claude `PowerShell` tool | n/a | n/a | powershell dialect (tool is opt-in in Claude Code **[verified]**) |
| npm/pnpm/yarn-classic script shell | `sh` → posix | `sh` → posix | `cmd.exe` → cmd; see §15.5.4 (multi-dialect evaluation) |
| Path case sensitivity | insensitive (default APFS) | sensitive | insensitive |
| Symlinks | yes | yes | symlinks + junctions; test creation may require Developer Mode (§28.5) |

---

## 9. Daemon

### 9.1 Responsibilities

The daemon is the only component that: evaluates policy, matches/creates/updates approvals, writes audit events, resolves scripts, and holds caches. It MUST be a single instance per user (lock file in `RuntimeDir()`; second instance exits with a clear message).

### 9.2 Process modes (one binary, P10)

| Command | Behavior |
|---|---|
| `agentguard daemon run` | Run in the foreground (used by the service manager and for debugging). `agentguard daemon` with no sub-command is an alias and prints a hint about `daemon start`. |
| `agentguard daemon start` | Start via the service manager if installed, else spawn detached (unmanaged mode) — then wait until `ping` succeeds (≤ 5 s). |
| `agentguard daemon stop` / `restart` / `status` | Via service manager when installed; else via pid file + IPC `shutdown`. `status` prints running state, pid, version, endpoint, uptime, DB path, mode (managed/unmanaged). |
| `agentguard daemon install` / `uninstall` | Register/unregister the per-user service (called by `setup claude` / `uninstall claude`). |

### 9.3 Startup sequence

1. Load config (§12.6); initialize `slog`.
2. Acquire single-instance lock; write pid file.
3. Open/create SQLite DB; run migrations (§23.4); fail hard on migration failure (daemon exits non-zero, no partial state).
4. Bind IPC endpoint (§10.1); write `daemon.json` `{endpoint, pid, version, protocol_version, started_at}` to `DataDir()` (atomic write).
5. Serve requests; each request handled in its own goroutine; per-request timeout 5 s.
6. On SIGTERM/SIGINT (or Windows console/service stop): stop accepting, finish in-flight requests (≤ 2 s), remove endpoint/pid/`daemon.json`, exit 0.

### 9.4 Service installation vs. process logic

`daemon run` (process logic) MUST have no knowledge of launchd/systemd/Windows. `platform.ServiceManager` (lifecycle logic) implements:

- **macOS**: LaunchAgent plist; `RunAtLoad`, `KeepAlive` (restart on crash), `ProgramArguments = [SelfExecutablePath, "daemon", "run"]`, stdout/stderr to the log dir. `launchctl bootstrap gui/<uid>` / `bootout`.
- **Linux**: `systemd --user` unit with `Restart=always` and `RestartSec=1`; `systemctl --user enable --now`. Supervision is unconditional rather than on-failure because the daemon also stops itself *deliberately* after an upgrade (see below); an administrative `systemctl --user stop` — what `agentguard daemon stop` runs — is still not restarted. If `systemd --user` is unavailable, use **unmanaged mode**: detached process, pid file, no supervision (`doctor` reports this).
- **Windows**: register a per-user logon autostart via `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` (value `AgentGuard` = `"<exe>" daemon start`); `daemon start` spawns `daemon run` detached with a hidden console window (`CREATE_NO_WINDOW`) and stdio redirected to the log file. No Windows Service (requires elevation) and no Task Scheduler in the prototype (see research R-09); the hook client's lazy start (§9.5) provides crash recovery.
- **All**: `Status()` MUST distinguish `not-installed`, `installed-stopped`, `running`, `unknown`.

**Self-refresh after an upgrade** (feature 002): an upgrade replaces the binary while the daemon is still running the previous one, so the machine keeps being gated by the old engine until something restarts it. Requests carry an optional `client_version` (contracts/ipc-protocol.md); when it names a strictly newer release **and** the daemon's own executable has been replaced on disk since it started, the daemon answers the request, logs `newer client detected; restarting`, and exits with code **75** after in-flight requests. The supervision above brings the new binary up. When the executable is unchanged — a newer install elsewhere on `PATH` — the daemon logs once and keeps serving rather than restarting into the same code forever; `doctor` reports the mismatch.

### 9.5 Lazy start from the hook client

If the hook client cannot connect and config `daemon.lazy_start` is `true` (default), it MAY spawn `SelfExecutablePath daemon run` detached **once** and retry connecting for up to 2 s. Single-instance locking makes concurrent lazy starts harmless. This exists for resilience only; the service manager remains the primary mechanism.

### 9.6 Failure behavior

**INVARIANT I-3**: A daemon failure (not running, unreachable, timeout, protocol error, internal error) MUST never result in ALLOW. The adapter MUST fall back to the agent's native permission flow (deferral, §11.3) and SHOULD surface a one-line warning to the user (rate-limited to once per session per hour).

---

## 10. IPC

### 10.1 Transport

- macOS/Linux: Unix domain socket at `RuntimeDir()/agentguard.sock`, dir 0700, socket 0600. If the path exceeds the platform limit (104 bytes macOS / 108 Linux), the daemon MUST fail with an explicit error suggesting `AGENTGUARD_RUNTIME_DIR`. The server SHOULD verify peer UID == own UID (`LOCAL_PEERCRED`/`SO_PEERCRED`) and reject otherwise.
- Windows: named pipe `\\.\pipe\agentguard-<sha256(username)[:16]>` with a security descriptor granting access to the current user only.
- Discovery order for clients: `AGENTGUARD_ENDPOINT` env → `daemon.json` in `DataDir()` → `Platform.IPCEndpoint()` default.
- No TCP/HTTP listener in the prototype (P1, P12).

The `ipc` package MUST expose a transport-agnostic `Listener`/`Dialer` pair so the protocol code is identical on all platforms.

### 10.2 Framing

One request per connection: client writes exactly one JSON object followed by `\n`, server writes exactly one JSON object followed by `\n`, both close. Max message size 1 MiB. Client-side timeouts: connect 1 s (plus lazy start), request 5 s.

### 10.3 Envelope

Request:

```json
{
  "protocol_version": 1,
  "request_id": "6f1c…",
  "method": "evaluate",
  "params": { }
}
```

Response:

```json
{
  "protocol_version": 1,
  "request_id": "6f1c…",
  "ok": true,
  "result": { }
}
```

Error response: `{"protocol_version":1,"request_id":"…","ok":false,"error":{"code":"UNSUPPORTED_PROTOCOL|BAD_REQUEST|NOT_FOUND|INTERNAL|BUSY","message":"…"}}`.

The daemon MUST reject requests whose `protocol_version` it does not support with `UNSUPPORTED_PROTOCOL`; the client treats that as daemon failure (§9.6). `protocol_version` starts at 1 and is bumped on incompatible changes.

### 10.4 Methods

| Method | Params (summary) | Result (summary) | Used by |
|---|---|---|---|
| `ping` | — | `{version, engine_version, uptime_s}` | CLI, setup, hook |
| `evaluate` | `ActionRequest` (§13.1) + `dry_run:bool` | `EvaluationResult` (§13.7) | hook (PreToolUse, PermissionRequest), self-test |
| `record_prompt` | `{agent, session_id, tool, raw_command, suggestions:[…]}` | `{audit_event_id?}` | hook (PermissionRequest) |
| `report_execution` | `{agent, session_id, tool_use_id, status:"completed|failed|unknown", response_summary, agent_consent?}` | `{imported_approval_id?}` | hook (PostToolUse) |
| `agent_config_changed` | `{agent, source, file_path}` | `{}` | hook (ConfigChange, optional) |
| `list_approvals` | `{project_id?, include_inactive?, limit?}` | `[ApprovalSummary]` | CLI |
| `get_approval` | `{id}` | `Approval` + recent `approval_events` | CLI |
| `set_approval_state` | `{id, state:"DISABLED|ACTIVE|REVOKED"}` | `Approval` | CLI |
| `create_approval` | `{audit_event_id, kind:"EXACT|SEMANTIC", note?}` | `Approval` | CLI `approve` |
| `list_history` | `{filters…, limit}` | `[AuditEventSummary]` | CLI |
| `get_history_event` | `{id}` | `AuditEvent` (full, with explanation) | CLI |
| `status` | — | counters, integration state | CLI `status`, `doctor` |
| `shutdown` | — | `{}` | CLI `daemon stop` (unmanaged mode) |

### 10.5 `evaluate` request example (Claude adapter → daemon)

```json
{
  "protocol_version": 1,
  "request_id": "…",
  "method": "evaluate",
  "params": {
    "dry_run": false,
    "request": {
      "agent": "claude",
      "agent_version": "2.1.233",
      "session_id": "abc123",
      "tool_use_id": "toolu_01…",
      "tool": "Bash",
      "dialect": "posix",
      "raw_command": "npm run cleanup",
      "cwd": "/Users/u/proj",
      "project_hint": "/Users/u/proj",
      "agent_consent": null,
      "adapter_context": { "permission_mode": "default", "hook_event": "PreToolUse" }
    }
  }
}
```

`evaluate` result example:

```json
{
  "protocol_version": 1,
  "request_id": "…",
  "ok": true,
  "result": {
    "audit_event_id": 1207,
    "decision": "allow",
    "class": "APPROVAL_MATCH",
    "reason": "matched approval 42: DELETE WORKSPACE_GENERATED ./dist (npm script cleanup, fingerprint unchanged)",
    "approval_id": 42,
    "hard_rule": null,
    "explanation": [ "resolved: npm run cleanup -> rm -rf ./dist", "targets: ./dist [WORKSPACE_GENERATED]", "effects: DELETE(recursive) WORKSPACE_GENERATED" ],
    "resolution_status": "RESOLVED",
    "user_message": "AgentGuard: auto-allowed (approval 42)"
  }
}
```

---

## 11. Claude Adapter

### 11.1 Hooks used and their roles

| Hook | Role in AgentGuard | Required |
|---|---|---|
| `PreToolUse` (matcher `Bash|PowerShell`) | **The runtime safety gate.** Evaluates every shell command; can `allow`, `deny`, or `ask`, including in `bypassPermissions` mode where only `deny` is enforceable **[verified: `ask` is treated as `deny` in bypassPermissions/non-interactive mode]**. | Yes |
| `PermissionRequest` (matcher `Bash|PowerShell`) | Fires only when Claude is about to show a permission dialog **[verified: not in bypass/non-interactive/dontAsk/auto modes nor when rules already allow]**. AgentGuard (a) records that a prompt was shown together with Claude's `permission_suggestions` for audit/metrics, and (b) acts as a second enforcement point that yields the same deterministic decision (`allow`/`deny`) if `PreToolUse` did not decide (e.g. partial hook installation). It never introduces a decision that `PreToolUse` would not have produced. | Yes |
| `PostToolUse` (matcher `Bash|PowerShell`) | Records execution outcome into the audit event correlated by `tool_use_id`. Cannot block **[verified]**. | Yes |
| `ConfigChange` (matcher `user_settings|project_settings|local_settings`) | Optional: notify daemon that Claude settings changed (audit note + cache invalidation of the adapter's rule snapshot). Not required for any acceptance scenario. | MAY |

Hook input contract used **[verified for 2.1.233]**: common fields `session_id`, `transcript_path`, `cwd`, `permission_mode`, `hook_event_name`; `PreToolUse`/`PostToolUse` add `tool_name`, `tool_input`, `tool_use_id` (`PostToolUse` also `tool_response`); `PermissionRequest` adds `tool_name`, `tool_input`, `permission_suggestions` (no `tool_use_id`). `tool_input.command` holds the command for both `Bash` and `PowerShell` tools. Environment variable `CLAUDE_PROJECT_DIR` is available to hook processes.

Hook exit-code contract **[verified]**: exit 0 with JSON on stdout = decision honored; exit 2 = block; any other non-zero = non-blocking error and the tool call **proceeds**. Consequence: the hook client MUST always exit 0 and express every decision (including BLOCK, as `permissionDecision: "deny"`, documented as equivalent to exit 2) through validated JSON on stdout; it MUST NOT rely on non-zero exit codes for safety and MUST NOT exit non-zero on internal errors (that would let the tool proceed while looking like a failure). Exit 2 is not used in the prototype.

### 11.2 Conversion to `ActionRequest`

The adapter MUST map Claude hook JSON to the generic `ActionRequest` (§13.1) and MUST NOT pass raw hook JSON to the daemon:

- `agent = "claude"`, `agent_version` from `claude --version` (cached at setup; refreshed by `doctor`).
- `tool = tool_name`; `dialect = posix` for `Bash` (all platforms), `powershell` for `PowerShell`; any other tool → the hook exits 0 with no output (not gated).
- `raw_command = tool_input.command`; `cwd = cwd`; `project_hint = $CLAUDE_PROJECT_DIR` (if set); `session_id`, `tool_use_id`.
- `agent_consent` (§11.6): computed by the adapter from Claude permission rules; `null` if none.
- `adapter_context`: `{permission_mode, hook_event, tool_description?}` — opaque to the core, stored in the audit event.

**INVARIANT I-7**: Core packages MUST NOT import or interpret Claude-specific structures; the adapter is the only place that knows about hook JSON, settings files, and permission rules.

### 11.3 Decision mapping for `PreToolUse`

`Decision` outcomes are `ALLOW | ASK | BLOCK` with a `class` (§18.5). The adapter maps them to hook output as follows:

| Daemon outcome | Interactive modes (`default`, `acceptEdits`, `plan`, other) | `bypassPermissions` |
|---|---|---|
| ALLOW | `permissionDecision: "allow"` (Claude still enforces its own deny/ask rules over a hook allow **[verified]**) | same as interactive |
| BLOCK | `permissionDecision: "deny"` + `permissionDecisionReason` (shown to Claude) + `systemMessage` (shown to user) | same |
| ASK, class `NO_MATCHING_APPROVAL` (understood action, never approved, no related approval) | **defer** + `systemMessage` with AgentGuard's resolution summary. Claude's *native* dialog is shown when Claude would prompt anyway; that dialog offers "Yes, and don't ask again", which is the persistent-consent path (§19.5). A hook-forced `ask` dialog offers only Yes/No **[verified: persistent rows require `suggestions`, which hook decisions do not carry]**, so forcing `ask` here would remove the user's ability to persist. | defer (the user's bypass choice stands) |
| ASK, class ∈ {`APPROVAL_MISMATCH`, `POLICY_REQUIRES_CONFIRMATION`} (understood action whose approval no longer matches, or which policy says must be confirmed) | `permissionDecision: "ask"` + reason → forces Claude's native permission dialog even if a Claude string rule would have allowed the command **[verified: a hook `ask` bypasses allow-rule lookup]**. The dialog offers Yes/No; persistence for the *new* behavior requires `agentguard approve <event-id>` (the reason text says so). | **defer**: only BLOCK is enforced in bypass mode |
| ASK, class ∈ {`UNRESOLVED_COMMAND`, `UNSUPPORTED_SYNTAX`, `AMBIGUOUS_PATH`, `CONTEXT_UNAVAILABLE`, `AGENT_RULE_CONFLICT`, `ENGINE_ERROR`} (an action AgentGuard **cannot judge**) | **defer**: Claude's native mechanism (its rules, mode, or dialog) decides, exactly as without AgentGuard | defer |
| daemon failure (§9.6) | defer + `systemMessage` warning | defer |

"Defer" = exit 0 with either no output or JSON without `permissionDecision` (a `systemMessage` MAY still be present). Rationale: forcing a prompt for actions AgentGuard cannot interpret would add prompts without adding information; deferring never-approved understood actions keeps Claude's persistent-consent UI available; forcing a prompt on mismatch/policy is what stops string rules from silently allowing changed behavior (§3). Non-interactive (`claude -p`) sessions cannot be distinguished by the hook; a forced `ask` there becomes a deny **[verified]**, which is fail-safe. Claude's built-in read-only command set (`ls`, `cat`, `grep`, `find`, read-only `git`, …) runs without prompting in every mode **[verified: docs]**; AgentGuard's baseline B1 agrees for in-workspace reads and is stricter for sensitive paths (R5 forces a prompt where Claude alone would not).

The adapter MUST NOT use any `permissionDecision` value other than `allow`, `deny`, `ask` (the build inspected also accepts a `defer` value whose semantics are unverified; do not use it — Appendix B-11).

For `deny`, `permissionDecisionReason` MUST contain the human-readable reason and the audit event id, e.g. `AgentGuard BLOCK [event 1207]: recursive delete of HOME (~/Documents) — hard rule R2. Approval 42 no longer matches: script package.json#scripts.cleanup changed; target ./dist → ~/Documents; scope WORKSPACE_GENERATED → HOME.` For `ask`, the reason SHOULD end with `To approve permanently: agentguard approve 1207`.

Because this table maps one ASK onto both a forced prompt and a deferral, the decision alone does not say what the user saw. After writing its response the adapter MUST report the mapping it applied (`allow | deny | prompt | defer`) with `record_adapter_action` (contracts/ipc-protocol.md), which the daemon stores on the event's `adapter_action` (§23.2). It runs **after** the response so nothing the agent waits on sits behind an audit write, and it is best effort: a failure to record MUST NOT change what the agent was told (INVARIANT I-12). A `dry_run` evaluation has no audit row and MUST NOT be reported.

### 11.4 `PermissionRequest` handling

1. Build the same `ActionRequest` (with `hook_event = "PermissionRequest"`, no `tool_use_id`) and call `evaluate` (the daemon reuses the cached evaluation for `(session_id, raw_command)` from the last 60 s if present, else evaluates fresh — deterministic either way).
2. Call `record_prompt` with `permission_suggestions` (rules only: `toolName`, `ruleContent`, `behavior`, `destination`).
3. Output: ALLOW → `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`; BLOCK → `{"…":{"decision":{"behavior":"deny","message":"<reason>","interrupt":false}}}`; ASK → no decision (dialog is shown). Never `updatedPermissions` (AgentGuard does not create Claude string rules).

### 11.5 `PostToolUse` handling

Call `report_execution` with `tool_use_id`, `status` (`completed` unless `tool_response` indicates interruption/failure, else `failed`; `unknown` if undeterminable), a `response_summary` truncated to 1 KiB, and a freshly computed `agent_consent` (§11.6). The daemon records the execution and, if consent is present and the evaluation for that `tool_use_id` was ASK for an understood action, attempts consent import (§19.5). This is how "Yes, and don't ask again" in Claude's native dialog becomes an AgentGuard approval in the same turn. No output.

### 11.6 Claude permission rules: consent import and conflict avoidance

The adapter reads Claude's `permissions.allow/deny/ask` rules for `Bash`/`PowerShell` from, in order: managed policy settings, `~/.claude/settings.json`, `<git root>/.claude/settings.json`, `<git root>/.claude/settings.local.json` — where the git root is the nearest `.git` ancestor of `CLAUDE_PROJECT_DIR` (Claude writes "don't ask again" rules to `.claude/settings.local.json` at the git repository root, worktrees resolved **[verified: docs]**); files are re-read when mtime/size changes. It implements Claude's Bash rule grammar **[verified: docs]**: exact `Bash(<command>)`; `*` matches any sequence including spaces at any position; a trailing ` *` enforces a word boundary; `:*` suffix ≡ trailing ` *`; bare `Bash`/`Bash(*)` matches everything; the raw command is split on Claude's separators (`&&`, `||`, `;`, `|`, `|&`, `&`, newline) and every subcommand must match; Claude's stripped wrappers (`timeout`, `time`, `nice`, `nohup`, `stdbuf`, `command`, `builtin`, `noglob`, bare `xargs`) are stripped before matching; any leading environment assignment makes the match *uncertain*.

- **Consent signal**: if every subcommand of the raw command is covered by `permissions.allow` rules (in any file), the adapter sets `agent_consent = {kind:"persistent_rule", rule_keys:["<scope>:<toolName>(<ruleContent>)", …], exact:true|false}`. The core uses this only for validated import (§19.5). If matching is uncertain in any way, `agent_consent = null` (no consent).
- **Deny/ask rules**: Claude enforces its own deny and ask rules even when a `PreToolUse` hook returns `allow` **[verified: build 2.1.233]**, so no adapter-side conflict handling is required for safety. The adapter MAY still record class `AGENT_RULE_CONFLICT` and defer when a deny rule visibly matches (defense in depth); this is optional.
- Session-only allowances and `--allowedTools` flags are invisible to hooks; they are not consent signals.

**INVARIANT I-8**: The adapter MUST NOT convert a Claude string rule into an AgentGuard approval by itself; import happens only in the daemon after full resolution and policy validation (§19.5).

### 11.7 Hook installation shape

Setup writes into `~/.claude/settings.json` (user scope) one command hook per event, all invoking the same binary: `agentguard hook claude` (the hook dispatches on `hook_event_name`). Windows MUST use exec form (`"command": "<abs path>\\agentguard.exe", "args": ["hook","claude"]`) to avoid shell-quoting divergence between Git Bash and PowerShell **[verified: exec form requires a real .exe]**; macOS/Linux MAY use shell form with the quoted absolute path. `timeout` = 10 (seconds). AgentGuard-owned entries are identified by their `command` pointing to the AgentGuard executable followed by `hook claude` (no custom marker keys, to stay within Claude's settings schema). Claude Code snapshots hooks at session start; setup MUST tell the user to restart running Claude sessions.

---

## 12. Setup / Installation

### 12.1 Distribution model

Single binary per OS/arch. Target install UX: `brew install agentguard` (macOS), `winget install AgentGuard.AgentGuard` (Windows), download/install-script (Linux), then `agentguard setup claude`. Package repositories are out of prototype scope but MUST NOT be precluded (no post-install steps other than `setup claude`; the binary MUST work from any install path because hooks/service entries embed `SelfExecutablePath()`; `doctor` MUST detect a moved binary).

### 12.2 `agentguard setup claude` — steps

Each step prints `✓`/`✗` and setup exits non-zero on the first failing required step, leaving the system in a consistent state (each step is idempotent and re-runnable).

1. **Detect Claude Code**: `claude` executable via `FindExecutable` (also check `~/.local/bin/claude`, `~/.claude/local/claude`); run `claude --version` (≤ 5 s); locate `~/.claude/` and `~/.claude/settings.json` (create empty `{}` if missing). Warn (not fail) if version < minimum supported version (Appendix B-4).
2. **Back up configuration**: copy `~/.claude/settings.json` to `<DataDir>/backups/claude-settings-<UTC timestamp>.json` (keep the last 10). Fail if the backup cannot be written.
3. **Install hooks**: parse settings JSON as a generic tree; add AgentGuard entries (§11.7) for `PreToolUse`, `PermissionRequest`, `PostToolUse` (and `ConfigChange` if `claude.hook_config_change=true`), preserving every unrelated key, hook, and matcher; replace stale AgentGuard entries (e.g. old binary path); write atomically (temp file + rename). Fail if the file is not valid JSON (do not overwrite).
4. **Initialize storage**: create `DataDir`, DB, run migrations.
5. **Install and start daemon**: `ServiceManager.Install` + `Start`; wait for `ping` (≤ 5 s). Report `managed`/`unmanaged` mode.
6. **Inventory Claude permissions**: read existing `Bash`/`PowerShell` allow rules (§11.6) and print a summary: how many exact/prefix rules exist and that they will be validated and imported on first use (§19.5). No approval is created at setup (P5, §11.6 I-8).
7. **Self-test** (dry-run, no persistence): (a) `ping`; (b) `evaluate` (`dry_run=true`) of `rm -rf ~/Documents` in a temporary workspace → expect BLOCK; (c) `evaluate` of `some-unknown-tool --x` → expect ASK/`UNRESOLVED_COMMAND`; (d) run the exact hook command line written to settings with a synthetic `PreToolUse` payload on stdin and `AGENTGUARD_SELFTEST=1` (forces dry-run) → expect a valid `deny` JSON for the same blocked command; on Windows also verify the exec form spawns.
8. **Report**:

```
AgentGuard setup

✓ Claude Code detected (2.1.233)
✓ Daemon installed (launchd, managed)
✓ Daemon running
✓ Permission hooks installed (~/.claude/settings.json, backup: …/claude-settings-20260815T120000Z.json)
✓ Database initialized (…/agentguard.db, schema v1)
✓ Integration test passed

AgentGuard is ready. Restart any running Claude Code sessions to activate the hooks.
```

Flags: `--dry-run` (print planned changes, modify nothing), `--settings <path>` (override the Claude settings file), `--no-service` (skip service registration; run daemon manually).

### 12.3 `agentguard uninstall claude`

1. Back up settings (as in 12.2 step 2). 2. Remove only AgentGuard-owned hook entries; remove now-empty matcher groups; leave everything else untouched. 3. Stop and uninstall the daemon service unless `--keep-daemon`. 4. Keep the database and config unless `--purge`. 5. Report each step.

**INVARIANT I-9**: Setup and uninstall MUST NOT delete or rewrite any Claude setting that AgentGuard did not create, and MUST back up the file before modifying it.

### 12.4 Idempotency and upgrades

Running `setup claude` again MUST converge (no duplicate hooks; binary path refreshed). Setup records `{agentguard_version, hooks_version, settings_path, backup_path, claude_version}` in the DB `meta` table for `doctor`.

### 12.5 `agentguard doctor`

Checks and prints: binary path stable vs. installed hooks/service; service registered/running; daemon reachable and version match; DB `PRAGMA integrity_check`; schema version; Claude detected + version + hooks present in settings + backup exists; Windows: Git Bash detected; endpoint permissions; config parse; disk space. Suggests the fix for each failure.

### 12.6 Configuration file (`config.toml`, all optional)

| Key | Default | Meaning |
|---|---|---|
| `log.level` | `info` | slog level |
| `daemon.lazy_start` | `true` | §9.5 |
| `daemon.request_timeout_ms` | `5000` | per-request budget |
| `policy.allow_readonly_workspace` | `true` | baseline rule B1 (§18.3) |
| `policy.protected_branches` | `["main","master"]` | in addition to the detected default branch (§18.2 R7) |
| `policy.sensitive_paths_extra` | `[]` | additional credential paths/patterns (§16.6) |
| `scope.generated_dirs_extra` | `[]` | additional generated roots, workspace-relative (§16.4) |
| `claude.settings_path` | auto | override |
| `claude.hook_timeout_seconds` | `10` | written into hooks |
| `claude.hook_config_change` | `false` | install ConfigChange hook |
| `audit.store_response_summary` | `true` | keep truncated tool output |

Unknown keys → warning, not failure. No config file is required for the DoD scenario.

---

## 13. Internal Domain Model

All types are agent- and OS-independent (`internal/action`).

### 13.1 `ActionRequest`

| Field | Type | Notes |
|---|---|---|
| `agent` | string | `"claude"` |
| `agent_version` | string | informational |
| `session_id` | string | agent session |
| `tool_use_id` | string? | correlation id (may be absent) |
| `tool` | string | `"Bash"`, `"PowerShell"` |
| `dialect` | enum `posix|powershell|cmd` | selected by adapter |
| `raw_command` | string | verbatim, ≤ 64 KiB |
| `cwd` | string | as reported by the agent |
| `project_hint` | string? | agent's notion of project dir |
| `agent_consent` | object? | §11.6 |
| `adapter_context` | map | opaque, audit only |
| `received_at` | timestamp | daemon clock |

### 13.2 `Context` (established per request; cached per workspace)

`workspace_root` (canonical), `project_id` (§16.2), `home_dir`, `temp_dir`, `platform`, `generated_roots` (§16.4), `git` (`gitdir`, `default_branch?`, `current_branch?`, `remotes{name→host}`, `hooks_dir`, `hooks_present[]`), `package_manager` (`npm|pnpm|yarn-classic|yarn-berry|unknown`, `script_shell?`), `context_status` (`OK` | `WORKSPACE_UNDEFINED` | `ERROR`).

### 13.3 Parsed command model (parser output, §14)

`ParsedCommand{ dialect, commands: [SimpleCommand], operators: [ ; && || | ], unsupported: [UnsupportedConstruct{kind, position, text}] }`; `SimpleCommand{ argv:[Word], env_assignments:[k=v], redirections:[{op, target}], cwd_after?, raw_text }`. `Word{ text, quoted:bool, contains_glob:bool, contains_unexpanded_var:bool }`.

### 13.4 `Target`

| Field | Notes |
|---|---|
| `raw` | as written |
| `canonical` | absolute, cleaned, symlink-resolved (§16.1) |
| `display` | workspace-relative when under W, else `~`-relative when under HOME, else absolute |
| `scope` | §16.3 |
| `exists`, `is_dir`, `is_symlink` | from stat (best-effort) |
| `flags` | subset of `{wildcard, broad, traversal, symlink_escape, sensitive, tool_cache, network_path, temp}` |
| `status` | `RESOLVED | AMBIGUOUS` (unexpanded variables, unsupported expansion) |

### 13.5 `Effect`

| Field | Notes |
|---|---|
| `type` | `READ | WRITE | CREATE | DELETE | EXECUTE | NETWORK` |
| `target` | `Target?` (filesystem effects) |
| `network` | `NetworkTarget{host, port?, scheme?, method?, declared_kind?}` (NETWORK effects) |
| `program` | for EXECUTE: `{name, resolution: DECLARED|UNRESOLVED, elevated:bool}` |
| `flags` | subset of `{recursive, force, wildcard, discards_changes, elevated, inline_credential, insecure_tls}` |

RESOLVED utilities (rm, cp, git status, …) do not emit `EXECUTE` for themselves — only their modeled effects. `EXECUTE` is emitted for programs whose behavior is declared (DECLARED) or unknown (UNRESOLVED).

### 13.6 `ResolvedCommand` and `ResolvedAction`

`ResolvedCommand{ executable, semantic_op (§17.2), targets, effects, status: RESOLVED|DECLARED|UNRESOLVED, fingerprints:[Fingerprint], resolved_from: [wrapper chain, e.g. "npm run cleanup" → "rm -rf ./dist"], children:[ResolvedCommand] }`.

`ResolvedAction{ request_ref, context_ref, commands:[ResolvedCommand], semantic_ops:[…], effects: union, fingerprints: union, status: min over commands and parse/context (order RESOLVED > DECLARED > UNRESOLVED > PARSE_FAILED/CONTEXT_FAILED), action_key: sha256 of canonical form (§20.2), explanation:[string] }`.

`Fingerprint{ key, value (sha256 hex), description }` — keys are stable strings such as `npm-script:package.json#scripts.cleanup`, `npm-config:.npmrc#script-shell`, `gradle-config`, `maven-config` (§15.6).

### 13.7 `Decision` / `EvaluationResult`

`Decision{ outcome: ALLOW|ASK|BLOCK, class (§18.5), reason, approval_id?, hard_rule?, mismatch_reports:[{approval_id, differences:[…]}], engine_version }`. `EvaluationResult` = `Decision` + `audit_event_id` + `resolution_status` + `explanation` + `user_message`.

---

## 14. Parsing

### 14.1 Architecture

`parser.Dialect` interface with three implementations under `parser/posix`, `parser/powershell`, `parser/cmd`, all producing the shared model (§13.3). Parsers are pure functions of `(raw_command, cwd, env-view)`; they MUST NOT execute anything.

### 14.2 Supported constructs (all dialects unless noted)

| Construct | Behavior |
|---|---|
| Simple command with arguments | argv words |
| Quoting (`'…'`, `"…"`, backslash; PowerShell `'…'`/`"…"`; cmd `"…"` and `^`) | resolved into word text; `quoted=true` |
| Sequencing: `;` (posix, powershell), `&&` and `||` (all dialects; PowerShell ≥ 7), `&` as a separator (cmd only); PowerShell call operator `& "<path>" args` is treated as invoking `<path>`; posix trailing `&` (background) and PowerShell trailing `&` (background job) are unsupported | ordered command list; **all** commands are evaluated regardless of conditional operators (an action's effects are the union over branches) |
| Pipelines `|` | each stage evaluated; a stage that executes downloaded/streamed content (`sh`, `bash`, `zsh`, `pwsh`, `powershell`, `cmd`, `python`, `node`, `perl`, `ruby`, `Invoke-Expression`, `iex`) → that stage is `UNRESOLVED` with `EXECUTE(UNRESOLVED)` |
| Redirections `>`, `>>`, `<`, `2>`, `2>&1`, `&>` (posix); `>`/`>>`/`2>&1` (cmd, powershell) | `>`→ `CREATE|WRITE(truncate)` target; `>>`→ `WRITE`; `<`→ `READ`; `/dev/null`, `/dev/stdout`, `/dev/stderr`, `NUL` are ignored |
| Environment prefix `K=V cmd` (posix); `$env:K="V"; cmd` is unsupported | recorded; `PATH`, `LD_*`, `DYLD_*`, `NODE_OPTIONS`, `GIT_*` overrides → the command becomes `UNRESOLVED` |
| `cd <dir>` (posix builtin, powershell `Set-Location`/`cd`/`chdir`, cmd `cd`/`chdir`) | changes the cwd for subsequent commands in the same request; `cd` with no argument → HOME; `cd -` → unsupported |
| Tilde and variable expansion | `~`, `~/…`, `$HOME`, `${HOME}`, `$PWD`, `$TMPDIR` (posix); `~`, `$HOME`, `$env:USERPROFILE`, `$env:TEMP`, `$PWD` (powershell); `%USERPROFILE%`, `%HOMEDRIVE%%HOMEPATH%`, `%TEMP%`, `%TMP%`, `%CD%` (cmd). Any other variable → the word is `AMBIGUOUS` (§16.1 step 1) |
| Globs `*`, `?`, `[…]`, `**` | word `contains_glob=true`; classified via literal prefix (§16.1 step 7) |
| Comments (`#`, `REM`/`::`) | stripped |
| Grouping `( … )`, `{ …; }` (posix), `( … )` (powershell/cmd) | MAY be supported as plain sequences; else unsupported |
| Heredoc `<<EOF … EOF` feeding a redirection or `cat` | MAY be supported (content is literal input); else unsupported |

### 14.3 Unsupported constructs (→ `UNSUPPORTED_SYNTAX`, decision ASK)

Command substitution `$(…)`/backticks/`@(…)`, process substitution, arithmetic expansion, loops/conditionals/functions/case, `eval`, `source`/`.`, `exec`, `xargs`, `alias`, `export`/`set`/`unset`/`setx`, `pushd`/`popd`, `trap`, background `&` (posix), PowerShell script blocks `{…}`, `Invoke-Expression`, `Start-Process`, `-Command`/`-c` wrappers (`sh -c`, `bash -c`, `pwsh -Command`, `cmd /c`), `sudo`/`doas`/`su`/`runas` (parsed as an elevation wrapper: the inner command IS parsed and hard rules apply to it, but the action carries `elevated` and status `UNRESOLVED`), any syntax error, any node type not whitelisted by the AST walker.

**INVARIANT I-2**: Parser or resolution uncertainty MUST never result in ALLOW. Any `unsupported` entry or parse error makes the action status `PARSE_FAILED` and the decision at most ASK (hard rules still run over whatever was parsed).

**INVARIANT I-10**: The system MUST NOT use raw-string prefix checks (e.g. "starts with `./gradlew test`") anywhere in parsing, recognition, or policy.

### 14.4 Dialect selection

By the adapter (§11.2) for the top-level command; by the resolver (§15.5.4) for scripts resolved from package files. `parser/cmd` and `parser/powershell` MUST be compiled and tested on all OSes (they parse text; the OS only affects path rules).

---

## 15. Resolution

### 15.1 Pipeline

`ActionRequest` → establish `Context` (§16.2) → parse (§14) → for each `SimpleCommand`: **recognize** (§15.4) → for wrappers: **resolve** recursively (§15.5) → **normalize** targets (§16.1) and effects → aggregate `ResolvedAction`.

Limits (exceeding any → status `UNRESOLVED`, decision ASK): resolution depth 4; ≤ 32 simple commands per action; ≤ 500 files / 50 MiB hashed for fingerprints; ≤ 5 s total.

### 15.2 Resolution statuses

| Status | Meaning | Approvable? |
|---|---|---|
| `RESOLVED` | Executable known; effects fully modeled from arguments (rm, cp, mv, mkdir, cat, grep, find, git subcommands listed, curl, npm run → resolved script whose commands are all RESOLVED). | Yes |
| `DECLARED` | Well-known tool invocation whose effects are declared by convention (build/test tools). Workspace code executes inside the declared envelope; mutable inputs are fingerprinted. | Yes |
| `UNRESOLVED` | Unknown executable, unresolvable wrapper, unsupported flags, ambiguous target, `EXECUTE(UNRESOLVED)`, elevation. | **No** (prototype) |
| `PARSE_FAILED` / `CONTEXT_FAILED` | §14.3 / §16.2 | No |

**INVARIANT I-11**: An `UNRESOLVED`, `PARSE_FAILED`, or `CONTEXT_FAILED` action MUST NOT be auto-allowed and MUST NOT be approvable in the prototype (approval creation MUST reject it).

### 15.3 Recognizer contract

Each recognizer declares: executable names/aliases; an argument grammar with three classes — **SAFE** (ignored), **SEMANTIC** (changes op/targets/flags), **UNKNOWN** (default for anything not listed → status `UNRESOLVED`); how targets are extracted; effects produced; mutable inputs to fingerprint. Recognizers MUST default to UNKNOWN for unrecognized options; implementers MAY extend SAFE lists but MUST keep the default.

### 15.4 Recognizers required for the prototype

**Filesystem (posix names; PowerShell cmdlets/aliases; cmd builtins)**

| Command | Semantic op | Effects | Notes |
|---|---|---|---|
| `rm` / `Remove-Item` (`ri`,`rm`,`del`,`erase`,`rd`,`rmdir`) / cmd `del`,`erase`,`rd`,`rmdir` | `FS_DELETE` | `DELETE` per target; flags `recursive` (`-r`,`-R`,`--recursive`,`-Recurse`,`/s`), `force` (`-f`,`--force`,`-Force`,`/f`,`/q`), `wildcard` | `--no-preserve-root` SEMANTIC; `-i`,`-v`,`-d`,`--` SAFE; `-LiteralPath`/`-Path` SEMANTIC (targets); `-WhatIf` → treated as if absent |
| `cp` / `Copy-Item` (`cpi`,`cp`,`copy`) / cmd `copy` | `FS_COPY` | `READ` sources; `CREATE|WRITE` destination; `recursive` (`-r`,`-R`,`-a`,`-Recurse`) | last positional = destination (`-Destination` for PowerShell) |
| `mv` / `Move-Item` (`mi`,`mv`,`move`) / cmd `move` | `FS_MOVE` | `DELETE` source (recursive if dir) + `CREATE` destination | hard rules apply to the source deletion |
| `mkdir` / `New-Item -ItemType Directory` (`ni`,`md`,`mkdir`) / cmd `md`,`mkdir` | `FS_CREATE` | `CREATE` per target | `-p`,`-v`,`-Force` SAFE; `New-Item -ItemType File` → `CREATE` file |
| `cat`, `head`, `tail`, `wc`, `ls`, `pwd`, `echo`, `printf`, `true`, `false`, `test`/`[` / `Get-Content` (`gc`,`cat`,`type`), `Get-ChildItem` (`gci`,`ls`,`dir`) / cmd `type`, `dir` | `FS_READ` (or `NOOP`) | `READ` per file/dir target (none for `pwd`/`echo`/`true`) | `cat`, `grep`, `find` are REQUIRED; the others SHOULD be implemented; unimplemented → UNRESOLVED |
| `grep` / `Select-String` (`sls`) / cmd `findstr` | `FS_READ` | `READ` per target (`-r` recursive); `-f FILE` → `READ FILE` | grep options are read-only by construction: all options SAFE except path-taking ones (SEMANTIC) |
| `find` | `FS_READ` (+ `FS_DELETE` with `-delete`) | `READ` start paths; `-delete` → `DELETE recursive wildcard` on start paths; `-fprint*`/`-fls` → `WRITE file`; `-exec`/`-execdir`/`-ok`/`-okdir` → `EXECUTE(UNRESOLVED)` | SAFE predicates: `-name -iname -path -ipath -regex -type -mtime -mmin -newer -size -maxdepth -mindepth -empty -perm -user -group -print -print0 -ls -prune -not ! -a -and -o -or ( ) -L -H -P`; others UNKNOWN |

**Git** (`git [-C <path>] [--no-pager] <subcommand> …`; `-c key=val` and other global options → UNRESOLVED)

| Subcommand | Semantic op | Effects / flags |
|---|---|---|
| `status`, `diff`, `log`, `show`, `branch` (list only), `rev-parse` | `GIT_STATUS`, `GIT_DIFF`, `GIT_LOG`, `GIT_SHOW`, `GIT_BRANCH`, `GIT_REV_PARSE` (one op per subcommand so EXACT approvals do not generalize across subcommands) | `READ` W (incl. gitdir); `diff`/`show`/`log` path arguments → `READ` targets; `-o/--output` (`git diff --output=<file>`) → `WRITE file`; `branch` with `-d/-D/-m/-M/-c/-C/--set-upstream-to` → UNRESOLVED |
| `add` | `GIT_ADD` | `READ` targets, `WRITE` gitdir |
| `commit` | `GIT_COMMIT` | `WRITE` gitdir; if a client-side hook for the operation exists (§15.7) → `EXECUTE(UNRESOLVED)`; `--no-verify` skips hook check; `-m/-am/--amend/--no-edit/-a` SEMANTIC/SAFE |
| `checkout <ref>`, `switch <ref>`, `checkout -b`, `switch -c` | `GIT_CHECKOUT` | `WRITE` W (source tree); `-f/--force/--discard-changes` → flag `discards_changes`; `checkout -- <paths>`, `checkout <ref> -- <paths>`, `restore` → `discards_changes` |
| `reset` | `GIT_RESET` | `WRITE` gitdir; `--hard`/`--merge` → `WRITE` W + `discards_changes` |
| `push` | `GIT_PUSH` | `NETWORK{host from remote URL}`; flags `force` (`-f`,`--force`,`--force-with-lease`,`+refspec`), `delete` (`--delete`, `:branch`), `broad` (`--all`,`--mirror`,`--tags`); target branch = explicit refspec or current branch (`.git/HEAD`); remote defaults to `origin` / branch upstream; unknown remote or branch → UNRESOLVED |
| any other subcommand | — | UNRESOLVED |

Remote hosts are read from `.git/config` (`remote.<name>.url`; ssh `git@host:` and `ssh://`, `https://`, `git://` forms). No git process is executed.

**Package managers and build tools**

| Command | Handling |
|---|---|
| `npm run <s>`, `npm run-script <s>`, `npm test|t|tst`, `npm start`, `npm stop`, `npm restart` | `RUN_SCRIPT` wrapper → resolve script (§15.5.1) |
| `pnpm run <s>`, `pnpm <s>` (non-builtin), `pnpm test|start` | same |
| `yarn run <s>`, `yarn <s>` (non-builtin), `yarn test|start` | same |
| `npm install|i|ci|add|update|uninstall|remove` (and pnpm/yarn equivalents) | `INSTALL_DEPENDENCIES`: `NETWORK{declared_kind: dependency-registry}`, `WRITE` `node_modules` (WORKSPACE_GENERATED), `WRITE` lockfile/`package.json` (WORKSPACE), `EXECUTE(UNRESOLVED)` (lifecycle scripts) → status UNRESOLVED unless `--ignore-scripts` (then DECLARED) |
| `npx <pkg>`, `pnpm dlx`, `yarn dlx`, `npm exec` | if `node_modules/.bin/<pkg>` exists → treat as invocation of `<pkg>`; else UNRESOLVED (may download and execute) |
| `gradle`, `./gradlew`, `gradlew.bat`, `gradlew` | `GRADLE_TASK` (§15.5.2) |
| `mvn`, `./mvnw`, `mvnw.cmd` | `MAVEN_GOAL` (§15.5.3) |
| JS test runners `jest`, `vitest`, `mocha`, `node --test` | DECLARED `RUN_TESTS` (SAFE flags: `--coverage`, `--watch=false`, `--run`, `--ci`, `--silent`, `--verbose`, `--reporter*`, test path filters; path-valued or unknown flags → UNRESOLVED) — required so `npm test` can resolve |
| `rimraf <paths>` | SHOULD: DECLARED `FS_DELETE` recursive on targets (common cross-platform cleanup binary) |

**Network**

| Command | Handling |
|---|---|
| `curl [opts] <url>…` | `HTTP_REQUEST`: `NETWORK{host, port, scheme, method}` per URL; `-X`/`--request` sets method; `-d/--data*`, `-F/--form`, `-T/--upload-file`, `--json` → method POST/PUT semantics (`-T`/`@file` → `READ file`); `-o/--output <file>` → `CREATE|WRITE file`; `-O/--remote-name` → `CREATE` in cwd; `-K/--config` → UNRESOLVED; `-u/--user`, `--oauth2-bearer` → flag `inline_credential`; `-k/--insecure` → flag `insecure_tls`; SAFE: `-s -S -L -f -v -i -I -H --header -A --user-agent --retry* --max-time --connect-timeout -w --compressed --http1.1 --http2 -4 -6 --fail-with-body`; URLs with unexpanded variables → AMBIGUOUS; unknown options → UNRESOLVED |

Anything not listed (including `chmod`, `chown`, `wget`, `python`, `docker`, `make`, `go`, `cargo`, `./script.sh`) → `UNKNOWN` semantic op, status `UNRESOLVED`.

### 15.5 Script and wrapper resolution

#### 15.5.1 npm / pnpm / yarn scripts

1. Locate the nearest `package.json` from the command's cwd upward, stopping at W (never above W). Missing → UNRESOLVED.
2. Read `scripts.<name>`; missing → UNRESOLVED (except `npm start` default `node server.js` → UNRESOLVED anyway; `npm test` without a script → UNRESOLVED).
3. Compose the executed command list: `pre<name>` (if defined) → `<name>` (+ passthrough args after `--` appended verbatim) → `post<name>` (if defined). Pre/post are included for all three managers (over-approximation is safe).
4. Parse each script text with the dialect(s) from §15.5.4 and recognize/resolve recursively (depth counts each wrapper hop). Scripts invoking `npm run other` are resolved recursively with cycle detection.
5. Record fingerprints: `npm-script:<rel path>#scripts.<name>` = sha256(script text) for each executed script (pre/post included), `npm-config:.npmrc#script-shell` = sha256(value or `"<unset>"`) considering `<W>/.npmrc` and `~/.npmrc`, `npm-config:package.json#packageManager` = sha256(value or `"<unset>"`).
6. The `RUN_SCRIPT` wrapper itself contributes `READ` of `package.json` and no `EXECUTE` (the resolved children carry the effects). Workspace flags (`-w`, `--workspace`, `-ws`, `--workspaces`, `--filter`, `-r`) → UNRESOLVED in the prototype.

#### 15.5.2 Gradle tasks (DECLARED)

`gradle`/`gradlew` invocations map task names to ops: `test`, `check`, `integrationTest`, `*Test` → `RUN_TESTS`; `build`, `assemble`, `compile*`, `classes`, `jar`, `bootJar`, `war` → `BUILD`; `clean` → `CLEAN` (declared `DELETE recursive` on `build/` of the project and of subprojects, WORKSPACE_GENERATED); `publish*`, `upload*`, `deploy*` → `NETWORK{declared_kind: publish}` and status UNRESOLVED (never auto-approvable in prototype); `wrapper`, `init`, `dependencies`, `tasks`, `help`, `projects` → `BUILD_TOOL_INFO` (`READ` W, DECLARED); any other task → UNRESOLVED. Task qualifiers `:module:test` are SEMANTIC (kept as target qualifiers). SAFE flags: `--tests`, `--info`, `--debug`, `--stacktrace`, `--no-daemon`, `--daemon`, `--offline`, `--continue`, `--rerun-tasks`, `--warning-mode`, `--console`, `-q`, `--quiet`, `--parallel`, `--build-cache`, `--no-build-cache`, `--configuration-cache`, `--refresh-dependencies`, `-x <task>`, `-P<k>=<v>`, `-D<k>=<v>` (except `-Dorg.gradle.jvmargs`/`-Duser.home` → UNRESOLVED). Path/execution flags `-p`, `-b`, `-c`, `-g`, `--gradle-user-home`, `-I`, `--init-script`, `--project-cache-dir`, `--include-build` → UNRESOLVED. Declared envelope: `READ` W and WORKSPACE_GENERATED, `WRITE|CREATE` WORKSPACE_GENERATED (`build/`, `.gradle/`), `READ|WRITE` HOME tool caches (`~/.gradle/**`, flag `tool_cache`), `NETWORK{declared_kind: dependency-registry}`, `EXECUTE(DECLARED)`. Fingerprint `gradle-config` = sha256 over the sorted list of `(rel path, sha256)` for: `gradlew`, `gradlew.bat`, `gradle/wrapper/gradle-wrapper.properties`, `gradle/wrapper/gradle-wrapper.jar`, `settings.gradle[.kts]`, `gradle.properties`, every `*.gradle`/`*.gradle.kts` under W (excluding generated roots and `node_modules`), and every file under `buildSrc/` (excluding `buildSrc/build/`, `buildSrc/.gradle/`).

#### 15.5.3 Maven goals (DECLARED)

`mvn`/`mvnw` phases/goals: `test`, `verify`, `integration-test`, `surefire:test`, `failsafe:*` → `RUN_TESTS`; `compile`, `test-compile`, `package`, `install` (local repo) → `BUILD` (`install` additionally `WRITE` `~/.m2/**` tool cache); `clean` → `CLEAN` (declared `DELETE recursive` on `target/` per module); `deploy`, `release:*`, `site-deploy` → `NETWORK{publish}` + UNRESOLVED; `dependency:tree`, `help:*`, `versions:display*` → `BUILD_TOOL_INFO`; others UNRESOLVED. SAFE flags: `-q`, `-B`, `-e`, `-X`, `-o`, `-U`, `-DskipTests`, `-Dtest=…`, `-Dmaven.test.skip=…`, `-D<k>=<v>` generic (except `-Dmaven.repo.local`, `-Duser.home` → UNRESOLVED), `-pl`, `-am`, `-amd`, `-T`, `-P<profile>`, `-ntp`. Path/execution flags `-f`, `--file`, `-s`, `-gs`, `--settings`, `-t`, `--toolchains` → UNRESOLVED. Envelope as Gradle with `target/` and `~/.m2/**`. Fingerprint `maven-config` = sha256 over `mvnw`, `mvnw.cmd`, `.mvn/**`, and every `pom.xml` under W (excluding generated roots).

#### 15.5.4 Effective shell dialect for resolved scripts

npm, pnpm and yarn-classic run scripts through `sh` on macOS/Linux (posix) and `cmd.exe` on Windows (cmd) unless `script-shell` is configured; yarn-berry (`.yarnrc.yml` present or `packageManager: yarn@2+`) uses its own POSIX-like shell on all OSes (posix). Rules:

- If `script-shell` is set to a recognizable shell (`sh|bash|zsh|dash` → posix; `pwsh|powershell` → powershell; `cmd` → cmd) use it; if set to anything else → UNRESOLVED.
- On Windows with no `script-shell`, the script MUST be evaluated under **both** cmd and posix dialects (Git Bash may supply the utilities the script calls); the resulting effects are the **union**, and the status is the weaker of the two. On macOS/Linux, posix only.
- Environment-variable overrides of the script shell are invisible to AgentGuard (documented limitation §27).

**INVARIANT I-13**: When the effective interpretation of a script is uncertain, AgentGuard MUST evaluate every plausible interpretation and combine effects conservatively (union); it MUST NOT pick the most permissive interpretation.

### 15.6 Mutable inputs and fingerprints

Every file read to *decide behavior* (package.json scripts, `.npmrc` script-shell, gradle/maven build files, wrappers) is a mutable input and MUST be recorded as a fingerprint on the `ResolvedCommand`. Fingerprint inputs MUST be re-read and hashed on every evaluation (no mtime-based caching for inputs < 1 MiB; larger sets MAY use `(path, size, mtime, ctime)` cache keys but MUST rehash on any change).

### 15.7 Git client-side hooks

For `commit` (`pre-commit`, `prepare-commit-msg`, `commit-msg`, `post-commit`), `push` (`pre-push`), `checkout`/`switch` (`post-checkout`), determine the hooks dir: `core.hooksPath` from `<gitdir>/config`, else from `~/.gitconfig` / `~/.config/git/config` (`[core] hooksPath`), else `<gitdir>/hooks`. `.git` may be a file (`gitdir: <path>` — worktrees/submodules). If any applicable hook file exists (ignoring `*.sample`), the command gets `EXECUTE(UNRESOLVED)` (status UNRESOLVED) unless `--no-verify` is present (which skips only the hooks git actually skips). Prototype limitation: hooks make these git operations non-auto-approvable (deferral to Claude's native flow keeps behavior equal to today).

---

## 16. Scope Classification

### 16.1 Path normalization algorithm (per target word)

1. **Expansion**: apply the dialect's supported expansions (§14.2). A word with an unsupported variable → `Target.status = AMBIGUOUS` (decision at most ASK, class `AMBIGUOUS_PATH`).
2. **Separator/drive normalization**: on Windows accept `\` and `/`, drive letters, MSYS-style `/c/Users/…` → `C:\Users\…`, UNC `\\server\share\…` (flag `network_path`).
3. **Absolutize** against the command's effective cwd (after `cd` tracking).
4. **Lexical clean** (`.`, `..`). If the raw word was relative or lexically inside W and the cleaned path leaves W → flag `traversal`.
5. **Canonicalize**: resolve symlinks (and Windows junctions) on the longest existing prefix; append the non-existing remainder lexically. If the lexical path is under W but the canonical path is not (or a resolved link points outside W anywhere in the chain) → flag `symlink_escape`. Comparison uses the platform's case rule.
6. **Stat** (best-effort, read-only): `exists`, `is_dir`, `is_symlink`.
7. **Globs**: classify by the longest literal directory prefix; flag `wildcard`; if the prefix is `/`, a drive root, HOME, W, or a standard HOME sub-directory → also flag `broad`.
8. **Classify** scope (§16.3) and apply sensitivity/tool-cache/temp flags (§16.6).

**INVARIANT I-14**: Scope classification MUST use the canonical (symlink-resolved) path; a target MUST never be classified as WORKSPACE or WORKSPACE_GENERATED solely because its textual path starts under the workspace.

Conservative note: for `DELETE`, a symlink as the final path component is treated as if the deletion follows the link (over-approximation; `rm -rf ws/build/link` where `link → ~/Documents` is classified as HOME).

### 16.2 Workspace root and project identity

- W = the nearest ancestor of `cwd` (inclusive) containing `.git` (directory or file). If none: `project_hint` if `cwd` is inside it; else `cwd`.
- W MUST NOT be HOME, an ancestor of HOME, the filesystem root, a drive root, or a SYSTEM root. If the candidate violates this → `context_status = WORKSPACE_UNDEFINED`; every filesystem-targeting action then yields ASK (`CONTEXT_UNAVAILABLE`); network-only and NOOP actions may still be evaluated.
- `project_id = sha256(canonical W)`; the git remote `origin` URL (if any) is stored as informational metadata only.
- Monorepos: nearest `package.json`/`pom.xml`/`build.gradle` upward from cwd (bounded by W) selects the resolution root; fingerprint keys embed the workspace-relative path.
- W and `project_id` are fixed per request from the request `cwd`. Each command's targets are absolutized against its **effective cwd** (after `cd` tracking, §14.2); git subcommands operate on the repository containing the effective cwd, and if that lies outside W their targets are classified by their actual location (e.g. HOME), so neither baseline B1 nor W-scoped approvals apply.

### 16.3 Scopes (disjoint, exhaustive, evaluated in this order)

| Scope | Definition |
|---|---|
| `SYSTEM` | `/`, drive roots (`C:\`), and platform system roots: macOS `/System /usr /bin /sbin /etc /var /Library /Applications /private /dev /opt /cores`; Linux `/bin /sbin /usr /etc /var /lib /lib32 /lib64 /boot /dev /proc /sys /opt /root /srv /snap`; Windows `%SystemRoot%`, `%ProgramFiles%`, `%ProgramFiles(x86)%`, `%ProgramData%`, `%SystemDrive%\Recovery`, `\Windows` on any drive. Any path under these — **except** the shared/user temp locations `/tmp`, `/private/tmp`, `/var/tmp`, `/private/var/tmp`, and the user's `TempDir()` (macOS `/private/var/folders/…`), which are `OUTSIDE_WORKSPACE` with flag `temp`. |
| `WORKSPACE_GENERATED` | Under W and under a generated root (§16.4). |
| `WORKSPACE` | Under W (including `.git`) and not generated. W itself is `WORKSPACE` with flag `broad`. |
| `HOME` | Under HOME and not under W. HOME itself → flag `broad`. |
| `OUTSIDE_WORKSPACE` | Everything else (other users' homes, `/tmp`, mounted volumes, UNC paths, other drives). Under `TempDir()` → flag `temp`. |

### 16.4 Generated roots G(W)

A directory D under W is a generated root iff one of:

- **Node**: D's name ∈ {`node_modules`, `dist`, `build`, `coverage`, `.next`, `.nuxt`, `.turbo`, `.cache`, `.parcel-cache`, `storybook-static`} and D's parent directory contains `package.json`.
- **Gradle**: D's name is `build` and D's parent contains `build.gradle`, `build.gradle.kts`, `settings.gradle`, or `settings.gradle.kts`; or D is `<W>/.gradle`; or D is `buildSrc/build`.
- **Maven**: D's name is `target` and D's parent contains `pom.xml`.
- **Configured**: D is listed in `scope.generated_dirs_extra` (workspace-relative).
- Optional (MAY): Python `__pycache__`, `.pytest_cache`, `.venv`/`venv` with `pyproject.toml`/`requirements.txt` present; Go `bin/` is NOT generated by default.

A folder named `build`/`dist` without the marker file is `WORKSPACE`, not generated. Generated roots are computed after canonicalization; a generated root that is a symlink escaping W is not generated (I-14).

### 16.5 Standard HOME sub-directories (`broad` when targeted as a whole)

`Desktop`, `Documents`, `Downloads`, `Pictures`, `Music`, `Movies`/`Videos`, `Library` (macOS), `.ssh`, `.config`, `AppData` (Windows), and HOME itself. Any recursive DELETE whose target is one of these or HOME → BLOCK (R2).

### 16.6 Sensitive (credential) paths and tool caches

Sensitive (flag `sensitive`): `~/.ssh/**`, `~/.aws/**`, `~/.gnupg/**`, `~/.kube/**`, `~/.docker/config.json`, `~/.netrc`, `~/_netrc`, `~/.npmrc`, `~/.yarnrc*`, `~/.pypirc`, `~/.gitconfig`, `~/.git-credentials`, `~/.config/gh/**`, `~/.config/gcloud/**`, `~/.azure/**`, `~/.claude/**`, `~/.anthropic/**`, macOS `~/Library/Keychains/**`, Windows `%APPDATA%\Microsoft\Credentials\**`, AgentGuard's own `DataDir`/`ConfigDir`/`RuntimeDir` and the Claude settings files it manages (self-protection), and any path whose final component matches `id_rsa*`, `id_ed25519*`, `id_ecdsa*`, `id_dsa*`, `*.pem`, `*.key`, `*.p12`, `*.pfx`, `*.jks`, `*.keystore`, `.env`, `.env.*`, `*.kdbx`, `credentials.json`, `service-account*.json`, plus `policy.sensitive_paths_extra`. Tool caches (flag `tool_cache`, writes allowed inside DECLARED envelopes): `~/.gradle/**`, `~/.m2/**`, `~/.npm/**`, `~/.yarn/**`, `~/.pnpm-store/**`, `~/.cache/**`, `~/Library/Caches/**`, `%LOCALAPPDATA%\npm-cache\**`, `%LOCALAPPDATA%\pnpm\**`, `%LOCALAPPDATA%\Yarn\**`.

---

## 17. Effect Model

### 17.1 Low-level effects

`READ`, `WRITE`, `CREATE`, `DELETE`, `EXECUTE`, `NETWORK` (§13.5). `WRITE` includes truncation/overwrite; `CREATE` covers new files/dirs; `MOVE` is modeled as `DELETE` + `CREATE`.

### 17.2 Semantic operations (prototype)

`FS_READ`, `FS_CREATE`, `FS_COPY`, `FS_MOVE`, `FS_DELETE`, `GIT_STATUS`, `GIT_DIFF`, `GIT_LOG`, `GIT_SHOW`, `GIT_BRANCH`, `GIT_REV_PARSE`, `GIT_ADD`, `GIT_COMMIT`, `GIT_CHECKOUT`, `GIT_RESET`, `GIT_PUSH`, `RUN_SCRIPT` (wrapper), `RUN_TESTS`, `BUILD`, `CLEAN`, `BUILD_TOOL_INFO`, `INSTALL_DEPENDENCIES`, `HTTP_REQUEST`, `SHELL_NAVIGATE` (`cd`), `NOOP`, `UNKNOWN`. A composite action lists the semantic ops of all its commands in order (wrappers included, e.g. `npm run cleanup` → `[RUN_SCRIPT, FS_DELETE]`).

### 17.3 Declared envelopes

| Semantic op | Declared effects |
|---|---|
| `RUN_TESTS` | `READ` WORKSPACE + WORKSPACE_GENERATED; `EXECUTE(DECLARED)`; `WRITE|CREATE` WORKSPACE_GENERATED; `READ|WRITE` HOME tool caches; `NETWORK{dependency-registry}` for Gradle/Maven (not for JS runners) |
| `BUILD` | same as `RUN_TESTS` |
| `CLEAN` | `DELETE(recursive)` on the tool's generated roots (WORKSPACE_GENERATED) + `READ` W |
| `BUILD_TOOL_INFO` | `READ` W, `READ` tool caches, `NETWORK{dependency-registry}` |

An action's effect set is the union of modeled and declared effects; matching (§20) and policy (§18) operate on this set plus flags.

---

## 18. Policy Engine

### 18.1 Evaluation order (deterministic)

1. **Hard rules** (§18.2) run over everything that was parsed/resolved — even when the overall status is `UNRESOLVED`/`PARSE_FAILED` — and produce the strongest outcome among `BLOCK` > `ASK_ALWAYS` > `PASS`. `BLOCK` → decision BLOCK (class `HARD_RULE_<id>`), stop.
2. If hard outcome is `ASK_ALWAYS` → decision ASK, class `POLICY_REQUIRES_CONFIRMATION`, stop (approvals are not consulted). This precedes the resolution check so that partially understood but clearly sensitive actions (`sudo …`, `curl … | sh`, `unknown && cat ~/.ssh/id_rsa`) force a prompt with the rule's reason instead of deferring.
3. If action status ∈ {`PARSE_FAILED`, `CONTEXT_FAILED`, `UNRESOLVED`} or any target is `AMBIGUOUS` → decision ASK with the corresponding class (`UNSUPPORTED_SYNTAX`, `CONTEXT_UNAVAILABLE`, `UNRESOLVED_COMMAND`, `AMBIGUOUS_PATH`), stop.
4. **Baseline rules** (§18.3): if B1 applies → ALLOW, class `POLICY_READONLY_WORKSPACE`, stop.
5. **Approval matching** (§20): first match → ALLOW, class `APPROVAL_MATCH`, `approval_id`; record use; stop.
6. **Consent import** (§19.5): if `agent_consent` is present and importable → create approval, ALLOW, class `RULE_IMPORT`; stop.
7. Otherwise ASK: class `APPROVAL_MISMATCH` if a mismatch report exists for a related approval (§20.4), else `NO_MATCHING_APPROVAL`.

Any internal error → ASK, class `ENGINE_ERROR` (never ALLOW).

### 18.2 Hard rules (safety baseline)

| ID | Condition | Outcome |
|---|---|---|
| R1 | `DELETE` (any flags) with target scope `SYSTEM`; or `WRITE|CREATE` with scope `SYSTEM` (excluding `/dev/null`-style ignored devices) | BLOCK |
| R2 | `DELETE` with scope `HOME` and (`recursive` or `wildcard` or target `broad` or target `sensitive`) | BLOCK |
| R3 | `DELETE` with scope `OUTSIDE_WORKSPACE` and (`recursive` or `wildcard` or `is_dir`), except targets flagged `temp`; a `temp` target that is the temp root itself (`broad`) is also BLOCKed | BLOCK |
| R4 | `DELETE` with scope `HOME` or `OUTSIDE_WORKSPACE` not caught by R2/R3 (single non-recursive file, or recursive/wildcard delete strictly inside the temp directory) | ASK_ALWAYS |
| R5 | Any effect on a `sensitive` target: `READ` → ASK_ALWAYS; `WRITE|CREATE|DELETE` → BLOCK |
| R6 | Any target with `traversal` or `symlink_escape` flag | at least ASK_ALWAYS (BLOCK if another rule says so) |
| R7 | `GIT_PUSH` with `force` and target branch ∈ (`policy.protected_branches` ∪ detected default branch) or branch unknown; `GIT_PUSH` with `delete` or `broad` | ASK_ALWAYS |
| R8 | `GIT_CHECKOUT`/`GIT_RESET` with `discards_changes` | ASK_ALWAYS |
| R9 | `DELETE` whose target is W itself, `broad`/`wildcard` at W root, or `<W>/.git` | ASK_ALWAYS |
| R10 | Any effect with flag `elevated` (sudo/doas/runas) or `inline_credential` | ASK_ALWAYS |
| R11 | `NETWORK` with `insecure_tls` and a non-`localhost` host | ASK_ALWAYS |
| R12 | `EXECUTE(UNRESOLVED)` produced by piping downloaded/streamed content into an interpreter | ASK_ALWAYS |

**INVARIANT I-4**: Hard-rule BLOCK and ASK_ALWAYS outcomes MUST take precedence over stored approvals; no approval, import, or baseline rule can override them.

### 18.3 Baseline rules

- **B1 (read-only workspace)**: ALLOW iff config `policy.allow_readonly_workspace` is true, action status is `RESOLVED`, every effect is `READ`, every target scope ∈ {`WORKSPACE`, `WORKSPACE_GENERATED`} with no `sensitive`/`traversal`/`symlink_escape`/`network_path` flag, there is no `NETWORK`/`EXECUTE` effect, and `context_status = OK`. Covers `git status`, `git diff`, `cat README.md`, `grep -r foo src`, `find . -name '*.go'`.
  - An action with **no** effects satisfies B1 only when it has at least one command and every command's `semantic_op` is `NOOP` (`echo`, `pwd`, `true`). An effect-free command carrying any other `semantic_op` is a resolver defect, and B1 MUST NOT allow it: the absence of effects has to be asserted by a recognizer, never inferred (I-3).

No other baseline ALLOW exists in the prototype.

### 18.4 Explanations

Every decision MUST carry a deterministic, human-readable `reason` and an `explanation` list built from templates: resolved chain (`npm run cleanup -> rm -rf ./dist`), targets with scopes, effects with flags, the rule or approval that decided, and mismatch reports (`approval 42 no longer matches: fingerprint npm-script:package.json#scripts.cleanup changed; target ./dist -> ~/Documents; scope WORKSPACE_GENERATED -> HOME`).

### 18.5 Decision classes

| Outcome | Classes |
|---|---|
| ALLOW | `POLICY_READONLY_WORKSPACE`, `APPROVAL_MATCH`, `RULE_IMPORT` |
| ASK | `NO_MATCHING_APPROVAL`, `APPROVAL_MISMATCH`, `POLICY_REQUIRES_CONFIRMATION`, `UNRESOLVED_COMMAND`, `UNSUPPORTED_SYNTAX`, `AMBIGUOUS_PATH`, `CONTEXT_UNAVAILABLE`, `AGENT_RULE_CONFLICT` (optional, informational — see §11.6), `ENGINE_ERROR` |
| BLOCK | `HARD_RULE_R1` … `HARD_RULE_R5` (only rules that can BLOCK) |

---

## 19. Approval Memory

### 19.1 Approval record

| Field | Notes |
|---|---|
| `id` | integer |
| `project_id` | §16.2 (approvals are project-scoped, agent-independent; `created_by_agent` recorded) |
| `kind` | `EXACT` or `SEMANTIC` |
| `semantic_ops` | ordered list from the resolved action |
| `envelope` | set of `(effect_type, scope, flags-subset)` pairs plus `network: [NetworkTarget]` (declared kinds as categories) |
| `targets` | EXACT only: canonical target set (workspace-relative display form for W-scoped targets, absolute otherwise) |
| `fingerprints` | required conditions `key → value` |
| `engine_version` | integer constant of the engine that created it |
| `origin` | `claude_prompt` (created via `agentguard approve` from a prompted event), `claude_rule` (consent import §19.5), `cli` |
| `origin_ref` | rule key, event id |
| `created_from_event_id`, `created_from_raw_command` | provenance |
| `state` | `ACTIVE`, `DISABLED` (temporarily off, re-enable-able), `REVOKED` (permanent, kept for audit) |
| `created_at`, `last_used_at`, `use_count`, `note` | |

**INVARIANT I-15**: Approval records are never physically deleted by normal operation; revoke/disable change `state`. Historical approval events are preserved.

### 19.2 Approval kinds

- **EXACT**: the exact resolved action (semantic ops, canonical targets, effects+scopes+flags, network targets) in this project, under the recorded fingerprints. Two different raw commands that resolve identically match the same EXACT approval; two raw-identical commands that resolve differently do not.
- **SEMANTIC**: the same semantic ops in this project with effects staying within the envelope (targets may differ within the same scopes), under the recorded fingerprints. Example: EXACT `DELETE ./dist [WORKSPACE_GENERATED]` vs. SEMANTIC `DELETE within WORKSPACE_GENERATED`.

Default kind for every automatic path is **EXACT**. SEMANTIC approvals are created only by explicit user request (`agentguard approve <event> --semantic`). No broader kinds exist in the prototype (no cross-project, no effect-only).

### 19.3 Creation paths

1. **Explicit CLI**: `agentguard approve <audit-event-id> [--semantic]` (§25). The daemon builds the approval from the resolved action stored in that event (targets, effects, fingerprints as at evaluation time).
2. **Consent import** (§19.5): from a Claude persistent allow rule covering the raw command, validated by full resolution and policy, at first use.
3. There is no other creation path. In particular, a user answering "Yes" once in Claude's dialog does not create an approval, and `PermissionRequest`/`PostToolUse` never create approvals.

Creation MUST be rejected (with reason) when the source event's action status is not `RESOLVED`/`DECLARED`, when its hard-rule outcome is BLOCK or ASK_ALWAYS, or when the event belongs to a different project than the caller specifies.

**INVARIANT I-16**: An approval MUST be created only from a fully evaluated `ResolvedAction` and MUST record every fingerprint that resolution depended on.

### 19.4 Usage tracking

On each match: `use_count += 1`, `last_used_at = now`, `approval_events(matched, audit_event_id)`. On mismatch of a related approval: `approval_events(not_matched, audit_event_id, differences)`.

### 19.5 Consent import from Claude persistent rules

Triggers: (a) during `evaluate` (step 6 of §18.1) when the request carries `agent_consent`; (b) during `report_execution` (`PostToolUse`) when the report carries `agent_consent` and the audit event for that `tool_use_id` has decision ASK with class `NO_MATCHING_APPROVAL` — this covers the user choosing "Yes, and don't ask again" in Claude's native dialog after AgentGuard deferred (§11.3). Both paths are idempotent.

Preconditions (all MUST hold): `agent_consent.kind = persistent_rule`; the action status is `RESOLVED` or `DECLARED`; hard outcome is `PASS`; no row exists in `agent_rule_imports` for this `(project_id, agent, rule_key, raw_command)` for any of the consenting rules (each rule may be imported **at most once per project and raw command**, regardless of whether the created approval is later invalidated, disabled, or revoked); the raw command contains no `AMBIGUOUS` word.

Effect: create an EXACT approval (`origin = claude_rule`, `origin_ref = rule_keys`), insert one `agent_rule_imports` row per consenting rule, record `approval_events(created)`. In path (a) the decision is ALLOW with class `RULE_IMPORT`; in path (b) the audit event is annotated (`imported_approval_id`) and the next occurrence matches via §20. Because import happens once, a later change of resolved behavior for the same raw command yields ASK/BLOCK (never a second import) — this is what makes the invalidation scenario hold even though Claude's own string rule still exists.

**INVARIANT I-5**: Raw command equality alone MUST never be sufficient evidence for reuse when the command references a mutable script/wrapper; reuse requires matching resolved effects and valid fingerprints. Consent import is a one-time, validated conversion — not a string allowlist.

---

## 20. Approval Matching

### 20.1 Candidate set

`ACTIVE` approvals of the action's `project_id`, ordered EXACT before SEMANTIC, then ascending `id`. The first approval that matches wins (deterministic).

### 20.2 Canonical form and `action_key`

`action_key = sha256(project_id ‖ engine_version ‖ semantic_ops ‖ sorted canonical targets (display form) ‖ sorted effects (type, scope, flags) ‖ sorted network targets ‖ sorted fingerprint keys+values)`. EXACT matching MAY short-circuit on `action_key` equality but MUST behave identically to the field-wise rules below.

### 20.3 Match rules

An approval A matches action X iff all hold:

1. `A.engine_version` is compatible with the current engine (equal major).
2. `A.semantic_ops == X.semantic_ops` (ordered).
3. Every fingerprint `key → value` in `A.fingerprints` is present in `X.fingerprints` with the same value; a key absent from X is a mismatch (an approval created via a wrapper does not cover the direct command and vice versa).
4. Effects: for EXACT, `set(X.effects (type, scope, flags)) == set(A.envelope)`; for SEMANTIC, `set(X.effects) ⊆ set(A.envelope)`. Flags are part of the pair: `DELETE/WORKSPACE_GENERATED/{recursive}` does not cover `{recursive, wildcard, broad}`.
5. Network: `set(X.network) == set(A.network)` (EXACT) or `⊆` (SEMANTIC), compared by host+port+scheme+method, declared kinds by category.
6. Targets: for EXACT, `set(X.targets.display) == set(A.targets)`; for SEMANTIC, no target constraint beyond scopes carried by effects.
7. X has no `AMBIGUOUS` target, status is `RESOLVED`/`DECLARED`, and no hard-rule outcome other than `PASS` (guaranteed by §18.1 order).

**INVARIANT I-1**: An approval MUST never match an action whose resolved effects exceed the effects originally permitted by that approval (superset of effects, broader scope, new network target, new flag, or missing/changed fingerprint all fail).

**INVARIANT I-6**: Matching is a pure function of (approval, resolved action, engine version). No LLM, heuristic scoring, or wall-clock dependence.

### 20.4 Mismatch reports (for explanations)

When no approval matches, compute differences against **related** approvals: those in the same project whose `created_from_raw_command == X.raw_command`, or whose `semantic_ops` intersect X's, or that share a fingerprint key with X. For each (max 5), list: changed fingerprints (`key changed`), target differences (`./dist → ~/Documents`), scope changes (`WORKSPACE_GENERATED → HOME`), added effects/flags, added network targets. Store in the audit event and use class `APPROVAL_MISMATCH`.

---

## 21. Approval Invalidation

"Invalidation" means an existing approval **no longer matches** the current resolved behavior; the record stays (I-15) and is reported as such. Signals that produce a mismatch by construction of §20.3:

| Signal | How it surfaces |
|---|---|
| Resolved script text changed | fingerprint `npm-script:…` differs (rule 3) |
| Target changed | rule 6 (EXACT) or scope change through effects (rule 4) |
| Scope broadened (e.g. WORKSPACE_GENERATED → WORKSPACE/HOME) | rule 4 |
| New network effect / new host | rule 5 |
| Write became delete; new flags (recursive, wildcard, force, discards_changes) | rule 4 |
| Project identity changed (moved/cloned elsewhere) | different `project_id` → not a candidate |
| Build/config fingerprint changed (`gradle-config`, `maven-config`, `.npmrc` script-shell) | rule 3 |
| Engine semantics changed | rule 1 |

The audit event MUST record `matched_approval_id = null`, `related_approval_ids` and the mismatch report; the CLI shows "approval 42 no longer matches because …". Users may then re-approve (creating a new approval for the new behavior) or revoke.

Distinct from invalidation: `agentguard approval revoke <id>` (state REVOKED, permanent) and `disable <id>` (state DISABLED, reversible with `enable`).

---

## 22. Safety Baseline

Summarizing §18.2 for readers of this section: recursive/broad deletion of SYSTEM → BLOCK; broad/recursive deletion in HOME → BLOCK; dangerous deletion outside the workspace → BLOCK, single-file deletion outside the workspace and deletion inside the temp directory → ASK (never auto-approvable); credential/private-key access → READ ASK, modification BLOCK; force push to protected/default branch, remote branch deletion → ASK (never auto-approvable); path traversal or symlink escape → never silently allowed; unknown executable and unresolved shell construct → ASK; elevation and inline credentials → ASK; workspace-root or `.git` deletion → ASK.

**INVARIANT I-4** (restated): Persistent approvals cannot override hard safety rules; hard rules are evaluated first and their BLOCK/ASK_ALWAYS outcomes are final.

---

## 23. Storage

### 23.1 Location and settings

`<DataDir>/agentguard.db`, SQLite, `journal_mode=WAL`, `synchronous=NORMAL`, `foreign_keys=ON`, `busy_timeout=5000`. Only the daemon writes. CLI commands go through the daemon; `agentguard history`/`approvals` MAY fall back to read-only direct access when the daemon is down (with a warning).

### 23.2 Schema v1 (logical)

```
schema_version(version INTEGER PK, applied_at TEXT)

meta(key TEXT PK, value TEXT)                       -- install info, hooks version, claude version, engine_version

projects(id TEXT PK,                                -- sha256(canonical root)
         root_path TEXT, remote_url TEXT NULL,
         first_seen_at TEXT, last_seen_at TEXT)

approvals(id INTEGER PK, project_id TEXT FK, kind TEXT, semantic_ops TEXT(JSON),
          envelope TEXT(JSON), targets TEXT(JSON) NULL, network TEXT(JSON),
          engine_version INTEGER, origin TEXT, origin_ref TEXT NULL,
          created_from_event_id INTEGER NULL, created_from_raw_command TEXT,
          created_by_agent TEXT, state TEXT, note TEXT NULL,
          created_at TEXT, last_used_at TEXT NULL, use_count INTEGER DEFAULT 0,
          disabled_at TEXT NULL, revoked_at TEXT NULL)

approval_conditions(approval_id INTEGER FK, kind TEXT ('fingerprint'), key TEXT, value TEXT,
                    PRIMARY KEY(approval_id, kind, key))

approval_events(id INTEGER PK, approval_id INTEGER FK, event_type TEXT
                ('created','matched','not_matched','disabled','enabled','revoked'),
                audit_event_id INTEGER NULL, at TEXT, details TEXT(JSON) NULL)

audit_events(id INTEGER PK, at TEXT, agent TEXT, agent_version TEXT NULL, session_id TEXT NULL,
             tool_use_id TEXT NULL, hook_event TEXT NULL, project_id TEXT NULL, cwd TEXT,
             tool TEXT, dialect TEXT, raw_command TEXT,
             resolved TEXT(JSON),            -- ResolvedAction (commands, targets, effects, fingerprints, chain)
             resolution_status TEXT, decision TEXT, decision_class TEXT, reason TEXT,
             hard_rule TEXT NULL, matched_approval_id INTEGER NULL,
             related_approval_ids TEXT(JSON) NULL, mismatch_report TEXT(JSON) NULL,
             adapter_action TEXT NULL,       -- allow|deny|prompt|defer
             adapter_context TEXT(JSON) NULL,
             prompt_shown INTEGER DEFAULT 0, permission_suggestions TEXT(JSON) NULL,
             execution_status TEXT NULL, execution_at TEXT NULL, response_summary TEXT NULL,
             engine_version INTEGER, dry_run INTEGER DEFAULT 0)

agent_rule_imports(id INTEGER PK, project_id TEXT, agent TEXT, rule_key TEXT, raw_command TEXT,
                   approval_id INTEGER FK, imported_at TEXT,
                   UNIQUE(project_id, agent, rule_key, raw_command))

INDEXES: approvals(project_id, state); audit_events(at); audit_events(session_id, tool_use_id);
         audit_events(project_id, at); approval_events(approval_id, at)
```

### 23.3 Data that MUST survive restarts

Approvals with conditions and state; approval events; audit events including the full resolved action, decision, reason, mismatch report, execution outcome; agent rule imports; projects; meta (install/setup info); schema version.

### 23.4 Migrations

Numbered forward-only migrations applied in one transaction each on daemon start; `schema_version` records each; the daemon refuses to run against a newer schema than it knows. v1 is the only version in the prototype, but the mechanism MUST exist.

### 23.5 Retention

No automatic deletion in the prototype. `audit_events` MAY be pruned by a future command; not required.

---

## 24. Audit

Every `evaluate` (except `dry_run`) MUST write exactly one `audit_events` row before the response is returned (write failure → ASK/`ENGINE_ERROR`, never ALLOW). Fields per §23.2. `PermissionRequest` updates `prompt_shown`/`permission_suggestions` on the correlated row (`session_id` + `raw_command`, latest within 60 s) or creates a row with `hook_event=PermissionRequest` if none. `PostToolUse` updates `execution_status`, `execution_at`, `response_summary` on the row with the same `tool_use_id`. The adapter updates `adapter_action` on the evaluated row after it has answered the agent (§11.3).

These later annotations are written by separate calls and MUST NOT overwrite what the evaluation recorded, nor each other.

The audit MUST let a user answer "Why did AgentGuard auto-approve this?" (decision class, approval id, envelope, fingerprints valid) and "Why did AgentGuard block this?" (hard rule, targets, scopes, mismatch report) via `agentguard history show <id>`.

**INVARIANT I-17**: Every ALLOW and BLOCK MUST be traceable to a persisted audit event whose stored data alone (without re-evaluation) explains the decision.

---

## 25. CLI

One binary; sub-commands (exact syntax may be refined, semantics MUST stay):

| Command | Behavior |
|---|---|
| `agentguard setup claude [--dry-run] [--settings <path>] [--no-service]` | §12.2 |
| `agentguard uninstall claude [--keep-daemon] [--purge]` | §12.3 |
| `agentguard hook claude` | hook entry point (stdin JSON → stdout JSON; always exits 0 — see §11.1) |
| `agentguard daemon [run|start|stop|restart|status|install|uninstall]` | §9.2 |
| `agentguard approvals [--project <path>|--all] [--inactive] [--json]` | table: `ID  KIND  ACTION  TARGETS/SCOPE  PROJECT  USES  LAST USED  STATE  ORIGIN` |
| `agentguard approval show <id>` | full record: semantic ops, envelope, targets, fingerprints (key + short hash + description), origin, provenance event, state, usage, last 10 approval events |
| `agentguard approval disable <id>` / `enable <id>` / `revoke <id>` | state changes with confirmation line |
| `agentguard approve <audit-event-id> [--semantic] [--note <text>]` | create an approval from an evaluated event (§19.3). Prints the resulting approval in `show` format. This is the fallback/explicit path when the native Claude flow cannot signal persistent consent (Appendix B-7) and the non-interactive path used by tests. |
| `agentguard history [--blocked] [--asked] [--allowed] [--project <path>] [--session <id>] [--since <duration>] [--limit N] [--json]` | table: `ID  TIME  DECISION  CLASS  COMMAND  RESOLVED  REASON  APPROVAL` |
| `agentguard history show <id>` | full explanation: raw → resolved chain, targets+scopes, effects+flags, fingerprints, decision, rule/approval, mismatch report, prompt/execution info |
| `agentguard status` | daemon state, version, endpoint, DB path, counts (approvals active/disabled/revoked; events last 24 h by decision), Claude integration state |
| `agentguard doctor` | §12.5 |
| `agentguard version` | version, engine_version, protocol_version, schema version |

Output MUST prioritize: what is trusted (semantic op + envelope), scope, project, last used, usage count, and why something was allowed/blocked. `--json` MUST be available on list/show commands for scripting and tests. Exit codes: 0 success, 1 error, 2 daemon unreachable.

---

## 26. Failure Modes

| Failure | Behavior |
|---|---|
| Daemon not running / unreachable / timeout / protocol mismatch | Hook defers to native flow (§11.3), warns once per session/hour, optionally lazy-starts (§9.5). Never ALLOW. |
| Hook binary missing/moved | Claude reports the hook error and proceeds (its behavior); `doctor` detects the moved binary; setup rewrites hooks. Documented limitation: a missing hook is a Claude-side non-blocking error — the user is warned by Claude. |
| Parse error / unsupported construct | ASK (`UNSUPPORTED_SYNTAX`), hard rules still applied to parsed parts. |
| Unknown executable / unresolvable wrapper / ambiguous path | ASK (`UNRESOLVED_COMMAND` / `AMBIGUOUS_PATH`). |
| Workspace undefined (cwd is HOME/root) | ASK (`CONTEXT_UNAVAILABLE`) for filesystem actions. |
| Fingerprint input unreadable (permissions, too large) | UNRESOLVED → ASK. |
| SQLite locked/corrupt/write failure | evaluate → ASK (`ENGINE_ERROR`); daemon logs; `doctor` reports integrity. |
| Claude settings file invalid JSON | setup fails without writing; uninstall fails without writing. |
| Migration failure | daemon exits non-zero; hook defers. |
| Request over size limits | BAD_REQUEST → hook defers. |
| Clock skew | irrelevant to decisions (no time-based matching). |
| Concurrent hooks (parallel sessions/subagents) | independent evaluations; SQLite serializes writes; caches are per-workspace and safe under concurrency. |

**INVARIANT I-12**: No failure path in the adapter or daemon may produce `allow` output.

---

## 27. Security Model / Limitations

- AgentGuard is **local**: no network listener, no cloud, no account, no telemetry. IPC is restricted to the current OS user by filesystem permissions/pipe ACLs.
- AgentGuard is a **policy/control gate around supported agent tool actions** (Claude `Bash`/`PowerShell` in the prototype). It **does not sandbox** processes; once a command is allowed, it runs with the user's privileges. It does not gate Claude's `Write`/`Edit`/`Read`/MCP tools; an agent may modify workspace files (including `package.json`, hook scripts, test code) through those tools — AgentGuard's answer is fingerprinting: such modifications invalidate approvals for actions that depend on those files.
- It does not claim perfect static analysis: recognized commands are modeled; DECLARED tools execute workspace code within an envelope the user consciously accepts (RUN_TESTS/BUILD/CLEAN); everything else fails to ASK. It never executes commands to discover their effects; it only reads files.
- Hard safety policy takes precedence over approval memory (I-4). Daemon failure defers to the agent's native flow, never ALLOW (I-3). No hidden unrestricted fallback (P12).
- Known limitations of the prototype: environment-variable overrides of npm's script shell are invisible; `--allowedTools` and session-only Claude allowances are invisible; Windows dual-dialect evaluation of scripts may over-approximate; DECLARED envelopes are conventions, not proofs; Claude's own deny/ask rules are enforced by Claude, never overridden by AgentGuard; the hook approach depends on Claude Code honoring hook decisions (verified for the referenced version, Appendix B).
- Threat model summary: protects against (a) mutable-script drift behind approved commands, (b) shell composition attacks on prefix allowlists, (c) traversal/symlink escapes, (d) catastrophic deletions and credential access via gated tools, (e) silent bypass on daemon failure. Does not protect against a malicious approved program, non-gated tools, or a compromised local user account.

---

## 28. Testing Strategy

### 28.1 Unit tests (table-driven; run on all three OSes)

- `parser/*`: every construct in §14.2 (per dialect), every unsupported construct in §14.3, quoting/escaping edge cases, `cd` tracking, redirections, pipelines, globs, expansions, comment stripping; property: no panic on arbitrary input (fuzz target).
- `scope/`: normalization steps 1–8 with fixtures in `t.TempDir()`: relative/absolute, `.`/`..`, `~`, drive letters, MSYS paths, UNC, case rules, non-existent suffixes, globs/broad, generated-root detection with/without marker files, symlink/junction escape (skip with a clear reason if the OS forbids link creation), W validation (HOME/root rejection).
- `resolver/`: each recognizer's SAFE/SEMANTIC/UNKNOWN grammar; npm/pnpm/yarn script composition (pre/post, `--` args, recursion, cycles, workspace flags → UNRESOLVED), `.npmrc` script-shell, yarn-berry dialect, Windows dual-dialect union; Gradle/Maven task→op tables, flag classes, fingerprint file sets; git remote/branch/default-branch/hook detection from fixture `.git` dirs (no git binary required); curl grammar.
- `policy/`: each hard rule R1–R12 with positive/negative cases; baseline B1; evaluation order; explanation templates.
- `approval/`: EXACT/SEMANTIC matching rules 1–7, mismatch reports, invalidation signals table (§21), engine version compatibility, consent import once-only semantics.
- `storage/`: migrations from empty, idempotent restart, persistence round-trips, concurrent writers, integrity.
- `ipc/`: framing, size limits, protocol version rejection, timeouts, per-OS transport (UDS permissions; pipe ACL smoke test on Windows).
- `adapter/claude/`: hook JSON → `ActionRequest`; decision → hook JSON mapping table (§11.3) for every (outcome, class, permission_mode) combination; settings merge/unmerge preserving unrelated content (golden files); rule matching (exact/prefix/glob/uncertain) and conflict avoidance; PermissionRequest/PostToolUse correlation.
- `platform/`: directory resolution, endpoint derivation, service manager mocks; real service install/uninstall behind an opt-in env flag in CI.

### 28.2 Integration tests

In-process daemon on a real UDS/named pipe with a temp data dir; a real client sending `evaluate`/`record_prompt`/`report_execution`/`create_approval`/`list_*`; assertions on responses **and** on persisted rows.

### 28.3 End-to-end tests (`test/e2e`)

Drive the built binary: pipe Claude-format hook JSON into `agentguard hook claude` with `AGENTGUARD_*` env pointing at temp dirs and a temp workspace; assert on stdout JSON and on `agentguard history --json`. Scenarios S1–S10 (§29) MUST run on macOS, Linux, and Windows in CI. Tests MUST NOT touch real user data: HOME is redirected to a temp dir for the daemon under test (env override `AGENTGUARD_TEST_HOME` honored only when `AGENTGUARD_TEST_MODE=1`), and no destructive command is ever executed — hooks only evaluate.

### 28.4 Invariant tests

Each INVARIANT (I-1 … I-17) MUST map to at least one named test (e.g. `TestInvariant_I3_DaemonFailureNeverAllows`).

### 28.5 CI

GitHub Actions matrix `ubuntu-latest`, `macos-latest`, `windows-latest`; `go vet`, `staticcheck` (or `golangci-lint`), `go test ./... -race` (race on Linux/macOS), cross-compile all six targets, GoReleaser snapshot build. Windows jobs enable Developer Mode or skip symlink cases with an explicit skip message (junction cases MUST still run).

---

## 29. End-to-End Acceptance Scenarios

All scenarios run in a temporary workspace `W` that is a git repository (fixture `.git`) with `package.json` and, where noted, Gradle/Maven fixtures; HOME is a temp dir; the daemon uses a temp data dir. "→ hook output" refers to the `PreToolUse` mapping in `default` permission mode unless noted.

| ID | Scenario | Expected |
|---|---|---|
| S1 | `git status` (cwd = W) | RESOLVED, `GIT_STATUS`, `READ WORKSPACE`; decision ALLOW `POLICY_READONLY_WORKSPACE` → hook `allow`. With `policy.allow_readonly_workspace=false`: ASK `NO_MATCHING_APPROVAL` → hook `ask`; after `agentguard approve <event>`: `git status`, `git status --short`, `git -C . status` → ALLOW `APPROVAL_MATCH`; `git diff` → ASK (different semantic op; needs its own approval); `git status && rm -rf ~` → BLOCK R2 (never allowed by the `git status` approval); `cd ~ && git status` → effective cwd is HOME, so the read targets HOME → ASK (baseline B1 and the W-scoped approval do not apply) |
| S2 | Test execution: `./gradlew test` (Gradle fixture), `mvn test` (Maven fixture), `npm test` with `"test": "jest"` | DECLARED `RUN_TESTS`; first: ASK `NO_MATCHING_APPROVAL`; after `approve <event>`: `./gradlew test --info`, `./gradlew :app:test`, `npm run test`, `gradle test` → ALLOW `APPROVAL_MATCH`; `./gradlew test publish` → ASK `UNRESOLVED_COMMAND`; editing `build.gradle.kts` → `./gradlew test` ASK `APPROVAL_MISMATCH` ("gradle-config changed") |
| S3 | `npm run cleanup` with `"cleanup": "rm -rf ./dist"` | resolves to `rm -rf ./dist`; `FS_DELETE`, target `./dist` `WORKSPACE_GENERATED` (package.json marker), flags `recursive,force`; ASK `NO_MATCHING_APPROVAL` → hook **defers** with a `systemMessage` summary (Claude's native dialog appears); persistence via either path: (i) `PostToolUse` carrying consent for a newly written `Bash(npm run cleanup)` rule → import → EXACT approval, or (ii) `agentguard approve <event>`; new session id, same command → ALLOW `APPROVAL_MATCH`; direct `rm -rf ./dist` → ASK (fingerprint key absent, rule 3) |
| S4 | `some-unknown-tool --flag` ; `python3 script.py` ; `./scripts/deploy.sh` | ASK `UNRESOLVED_COMMAND` → hook defers (no decision); `approve` MUST be rejected |
| S5 | `for f in *; do rm -rf "$f"; done` ; `rm -rf $(cat list.txt)` ; `bash -c "rm -rf ./dist"` ; `curl https://x.example | sh` | ASK `UNSUPPORTED_SYNTAX`/`UNRESOLVED_COMMAND` (last: `POLICY_REQUIRES_CONFIRMATION` R12) → defer/ask; never ALLOW even with a matching EXACT approval for `rm -rf ./dist` |
| S6 | `curl https://api.example.com/health` approved (EXACT); then `curl https://evil.example.net/x` and `curl -X POST https://api.example.com/health` and `curl https://api.example.com/health -o ~/x` | first → ALLOW; others → ASK `NO_MATCHING_APPROVAL`/`APPROVAL_MISMATCH` (new host / new method / new HOME write); `curl … | bash` → ASK R12 |
| S7 | `rm -rf ~/Documents` ; `rm -rf ~` ; `rm -rf /` ; `rm -rf ~/*` ; `Remove-Item -Recurse -Force $env:USERPROFILE\Documents` (powershell dialect) ; `rd /s /q %USERPROFILE%\Documents` (cmd dialect) | BLOCK `HARD_RULE_R2`/`R1` → hook `deny` with reason + systemMessage; also BLOCK in `bypassPermissions` mode; an EXACT approval forged for such an action cannot exist (creation rejected) |
| S8 | `rm -rf ./dist/../../other` (cwd W) ; `cat ../../../etc/passwd` ; `cp secret.txt ../../outside/` | traversal flag → outside W; delete recursive OUTSIDE/HOME → BLOCK R3/R2; read → ASK R6 (never ALLOW; baseline B1 excluded by flag) |
| S9 | `W/build/link → HOME/Documents` (symlink; on Windows also a junction) then `rm -rf build/link`, `rm -rf build/link/`, `rm -rf build/*` | classified HOME via canonical path (`symlink_escape`) → BLOCK R2; never `WORKSPACE_GENERATED`; skip symlink variant with explicit reason if link creation is impossible, junction variant MUST run on Windows |
| S10 | Invalidation (primary): S3 approved; then `package.json` → `"cleanup": "rm -rf ~/Documents"`; same raw `npm run cleanup` (new session) | approval NOT reused; resolved `rm -rf ~/Documents`; BLOCK `HARD_RULE_R2` with mismatch report naming approval id, `npm-script:package.json#scripts.cleanup changed`, `./dist → ~/Documents`, `WORKSPACE_GENERATED → HOME`; audit row shows `related_approval_ids=[id]`, `matched_approval_id=null`; `history show` explains. Variant: `"cleanup": "rm -rf ./src"` → ASK `APPROVAL_MISMATCH` → hook `ask` (interactive) / defer (bypass) |
| S11 | Consent import: (a) no AgentGuard approval, Claude `settings.local.json` allow rule `Bash(npm run cleanup)` already present, `npm run cleanup` → `rm -rf ./dist` at `PreToolUse`; (b) no rule at `PreToolUse` (deferred), rule present at `PostToolUse` for the same `tool_use_id` | (a) ALLOW `RULE_IMPORT`, approval created (`origin=claude_rule`), `agent_rule_imports` row; (b) `report_execution` returns `imported_approval_id`, next `PreToolUse` → ALLOW `APPROVAL_MATCH`; in both: script then changes to `rm -rf ./src` → ASK `APPROVAL_MISMATCH` (no second import) → hook `ask`; a prefix rule `Bash(npm run *)` imports once per raw command; a Claude `deny` rule matching the command is left to Claude to enforce (AgentGuard `allow` does not override it — verified) |
| S12 | Daemon down (endpoint unreachable) with any command, including `rm -rf ~/Documents` | hook exits 0 with no decision (defer) + systemMessage warning; audit unavailable; test asserts no `allow` and no `deny` were emitted (native flow decides) |
| S13 | Full flow through the hook binary in Windows exec form and macOS/Linux shell form with a synthetic `PreToolUse` payload | valid JSON per §11.3, exit 0 |

Additional required checks: `PermissionRequest` payload updates `prompt_shown` and stores suggestions; `PostToolUse` payload updates execution fields by `tool_use_id`; `bypassPermissions` mapping table for ASK classes; `agentguard uninstall claude` leaves an unrelated user hook and unrelated settings byte-for-byte equivalent (JSON-equal); `setup claude` twice does not duplicate hooks.

---

## 30. Definition of Done

The prototype is DONE only when all of the following hold on **macOS, Linux, and Windows** (CI green on all three, plus one manual run per OS documented in the repo):

1. Install the binary; run `agentguard setup claude` → all setup steps report ✓ (daemon installed and running, hooks installed, DB initialized, self-test passed).
2. In Claude Code, run `npm run cleanup` in a workspace whose `package.json` has `"cleanup": "rm -rf ./dist"` → AgentGuard resolves `rm -rf ./dist`, classifies `FS_DELETE`, target `./dist`, scope `WORKSPACE_GENERATED`; AgentGuard's summary is shown as a hook message and Claude's native dialog prompts the user.
3. The user creates a persistent approval through the intended flow: choosing "Yes, and don't ask again" in Claude's native dialog (imported at `PostToolUse` or on the next occurrence, §19.5) or `agentguard approve <event-id>`.
4. Restart Claude / start a new session; the same action → auto-allowed with `APPROVAL_MATCH`/`RULE_IMPORT`, no prompt.
5. Modify `package.json` to `"cleanup": "rm -rf ~/Documents"`; run `npm run cleanup` → AgentGuard detects the changed script, target, and scope (`WORKSPACE_GENERATED → HOME`); the previous approval is not reused; the action is BLOCKed by hard policy with the explanation; `agentguard history show <id>` explains why. The `rm -rf ./src` variant yields ASK with the mismatch explanation.
6. Scenarios S1–S13 pass in CI on all three OSes; every invariant I-1…I-17 has a passing named test.
7. `agentguard uninstall claude` restores Claude to a working state with only AgentGuard entries removed.
8. `README` documents install, setup, the demo, limitations (§27), and the open questions resolved during implementation (Appendix B).

---

## Appendix A — Invariants index

| ID | Invariant |
|---|---|
| I-1 | An approval MUST never match an action whose resolved effects exceed the effects originally permitted by that approval. |
| I-2 | Parser/resolution uncertainty MUST never result in ALLOW. |
| I-3 | A daemon failure MUST never result in ALLOW. |
| I-4 | Hard safety BLOCK/ASK_ALWAYS rules MUST take precedence over stored approvals. |
| I-5 | Raw command equality alone MUST never be sufficient evidence for semantic approval reuse when the command references a mutable script/wrapper. |
| I-6 | Approval matching MUST be a pure, deterministic function; no runtime LLM. |
| I-7 | Core packages MUST NOT depend on agent-specific structures; the adapter is the only agent-aware component. |
| I-8 | Agent string rules MUST NOT become AgentGuard approvals without full resolution and policy validation in the daemon. |
| I-9 | Setup/uninstall MUST NOT delete or rewrite settings AgentGuard did not create, and MUST back up before modifying. |
| I-10 | No raw-string prefix checks anywhere in parsing, recognition, or policy. |
| I-11 | UNRESOLVED/PARSE_FAILED/CONTEXT_FAILED actions MUST NOT be auto-allowed nor approvable in the prototype. |
| I-12 | No failure path in adapter or daemon may produce `allow` output. |
| I-13 | Uncertain script interpretation MUST be evaluated under all plausible interpretations with union of effects. |
| I-14 | Scope classification MUST use canonical (symlink-resolved) paths; textual prefix under W is never sufficient. |
| I-15 | Approval records are never physically deleted by normal operation; state changes are audited. |
| I-16 | Approvals are created only from fully evaluated resolved actions and record every fingerprint resolution depended on. |
| I-17 | Every ALLOW and BLOCK MUST be explainable from persisted audit data alone. |

## Appendix B — Open technical questions and their resolution status

Resolved items were verified during planning (see `research.md` R-10/R-11 for evidence); the remaining defaults are fail-safe.

| # | Question | Status / default |
|---|---|---|
| B-1 | Does the hook input `cwd` track the persistent `cd` state of Claude's Bash tool across calls? | **Resolved — yes** (build 2.1.233 syncs the tracked cwd after each Bash command; hooks read the same accessor). `cwd` is used as the effective cwd. |
| B-2 | Does a `PreToolUse` `permissionDecision: "ask"` force the dialog even when a Claude `permissions.allow` rule matches? | **Resolved — yes** (hook `ask` skips allow-rule lookup; only deny/ask rules are consulted). |
| B-3 | After a hook-forced `ask`, does Claude's dialog still offer "don't ask again"? | **Resolved — no** (persistent rows require `suggestions`, absent on hook decisions). Consequence: never-approved understood actions are *deferred* so Claude's native dialog (with persistence) is used; mismatch/policy prompts are forced and persist via `agentguard approve`. |
| B-4 | Minimum supported Claude Code version. | Reference 2.1.233 verified; setup warns below 2.1.0 and installs PreToolUse/PostToolUse only below 2.0.45 (research R-16). |
| B-5 | Windows per-user autostart mechanism. | **Decided**: `HKCU\…\Run` + hidden detached spawn + hook lazy start (research R-09). |
| B-6 | Are Claude `permissions.deny`/`ask` rules enforced when a `PreToolUse` hook returns `allow`? | **Resolved — yes** (deny overrides; ask forces the full pipeline). Adapter conflict handling is optional. |
| B-7 | Re-consent UX after invalidation when Claude already holds a matching string rule. | `agentguard approve <event-id>`; the forced-`ask` reason includes the event id and command. |
| B-8 | `permission_suggestions` schema stability across versions. | Store verbatim JSON; never depend on it for decisions. |
| B-9 | Are hooks invoked for Bash calls made by subagents? | **Resolved — yes** (`agent_id`/`agent_type` present); treated identically. |
| B-10 | macOS notarization / Windows SmartScreen. | Out of prototype; note in README. |
| B-11 | Semantics of the `defer` `permissionDecision` value. | **Resolved**: print-mode (`-p`) only, ignored interactively. Not used; deferral = omit `permissionDecision`. |
| B-12 | Claude Bash rule pattern grammar. | **Resolved** (docs): `*` anywhere incl. spaces, trailing ` *` word boundary, `:*` ≡ ` *`, per-subcommand matching, wrapper stripping — implemented conservatively for consent detection only. |

## Appendix C — Requirement conflicts found and how they were resolved

| Conflict | Resolution |
|---|---|
| "ASK → Claude's native mechanism decides" vs. "explain that the resolved script/target/scope changed" (a native string rule would silently allow the changed command). | Mode-aware mapping (§11.3): never-approved understood actions defer to Claude's native dialog (which offers persistence) with an AgentGuard summary message; approval-mismatch and policy-confirmation cases force the dialog with AgentGuard's reason (a hook `ask` overrides allow rules — verified); actions AgentGuard cannot judge defer; bypass mode enforces BLOCK only. |
| "PreToolUse must deny in bypass mode" vs. verified fact that `ask` becomes `deny` in bypass mode (forcing prompts would deny everything unknown). | In bypass mode ASK defers; only BLOCK is emitted. |
| "Import existing Claude permissions during setup" vs. "never blindly convert string permissions" and lack of project context at setup time. | Setup inventories rules only; import is lazy, once per (project, rule, raw command), and validated by full resolution + policy (§19.5). |
| "Reuse Claude's native prompt; no separate permission UX" vs. the need for a non-interactive/observable persistent-consent path (CI, re-consent after invalidation, unresolved commands). | Minimal `agentguard approve <event-id>` CLI (no new UI); native flow remains primary. |
| "Reduce prompts significantly" vs. "unknown operations are not harmless". | Unknown/unresolved → ASK but *deferred*, so behavior for commands AgentGuard does not understand is identical to today (no regression); improvements come from understood commands and the read-only baseline. |
| "`git status` allowed after appropriate trust/policy" (approval or built-in?). | Built-in baseline B1 (read-only inside workspace, configurable) plus approvals for everything else. |
| Semantic approvals vs. "no automatic generalization without confirmation". | Automatic paths create EXACT approvals only; SEMANTIC requires `--semantic` on the CLI. |
| Windows npm scripts run under `cmd.exe` where `~` is literal, yet the DoD demo must behave identically on Windows. | Dual-dialect evaluation with union of effects on Windows (§15.5.4, I-13). |
| "`agentguard daemon`" listed as a command alongside start/stop/status. | Bare `daemon` = foreground `run` with a hint. |
| R12 fires on "`EXECUTE(UNRESOLVED)` **produced by piping** streamed content into an interpreter", but the effect model records nothing about where an execution came from, so R12 could not distinguish `curl … \| sh` from an ordinary unknown executable. (Found implementing T030.) | `ProgramRef` gains `streamed: bool`, set by the resolver for a pipeline stage that is an interpreter. It does not enter the approval envelope, so `action_key` is unchanged; such actions are UNRESOLVED and unapprovable anyway (I-11). |
| §11.4 step 1 has `PermissionRequest` reuse "the cached evaluation for `(session_id, raw_command)` from the last 60 s", because that hook carries no `tool_use_id`. Read literally, that also serves a stale decision to a **new** invocation of the same command in the same session: running an approved `npm run cleanup`, rewriting the script, and running it again within 60 s would reuse the ALLOW — precisely the bypass the product exists to prevent. (Found implementing T036.) | The cache is written under both keys but read by kind of request: a request carrying a `tool_use_id` (every `PreToolUse`, i.e. every distinct invocation) is answered only from its own entry and otherwise re-resolves; only a request without one — `PermissionRequest` — falls back to the `(session_id, raw_command)` entry. §11.4 gets exactly the correlation it asks for, and a re-run always re-resolves. |
| §23.2 contracts an `adapter_action` column and `data-model.md` §2.5 calls it "reported by adapter after mapping", but contracts/ipc-protocol.md defined no method for reporting it. The adapter computed the value and could only log it, so the column was always NULL and the audit could not tell a *forced* prompt from a *deferral* — the distinction §11.3 is built on. (Found by the T074 audit review.) | Added the `record_adapter_action` method (additive, no protocol bump). The adapter calls it **after** writing its response, on a short timeout, and swallows every failure: the annotation is bookkeeping and must never delay or change what the agent is told (I-12). The daemon validates the value against the four documented actions rather than storing what it is sent. |
| B1 requires "every effect is `READ`", which is vacuously true of an action with **no** effects — the shape produced both by `echo hello` (a modeled command that genuinely does nothing) and by a recognizer defect that names an operation and forgets to attach its effects. Read literally the rule allows both; read strictly it prompts for `echo`, which §18.3's own purpose rules out. (Found implementing T071.) | B1 admits the effect-free case only when the action has at least one command and every command's `semantic_op` is `NOOP`. The absence of effects must be **asserted** by a recognizer, never inferred from an empty slice, so a defect degrades to ASK rather than to permission (I-3). |
| §15.4 gives `git push` the flags `delete` and `broad`, and R7 tests the push target branch, but the effect-flag set in the data model is `{recursive, force, wildcard, discards_changes, elevated, inline_credential, insecure_tls}` and no structure carries a ref. Without them a plain-push approval would silently cover `--mirror`, and R7 could not be evaluated. (Found implementing T027.) | `delete` and `broad` added to the effect-flag set, so they take part in the approval envelope and `action_key` (I-1: `--mirror` invalidates a plain-push approval). The ref is carried by a new optional `ResolvedCommand.git = {remote, branch, branch_known}`, deliberately **outside** `action_key`: R7 runs at step 1–2 of §18.1, before approvals are consulted, so an approval can never bypass it and two pushes differing only in branch may share an identity. |
