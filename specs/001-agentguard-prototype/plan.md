# Implementation Plan: AgentGuard Prototype — Semantic Runtime Permission Layer for AI Coding Agents

**Branch**: `001-agentguard-prototype` | **Date**: 2026-08-16 | **Spec**: [spec.md](./spec.md) · technical contract [PROTOTYPE_SPEC.md](./PROTOTYPE_SPEC.md)

**Input**: Feature specification from `/specs/001-agentguard-prototype/spec.md`

**Progress tracking**: [tasks.md](./tasks.md) — the checkbox list you tick as work completes (`- [x]`).

## Summary

Build `agentguard`, a single Go binary that acts as CLI, Claude Code hook client, and per-user daemon. The daemon parses shell commands (POSIX/PowerShell/cmd), resolves package-manager scripts and build-tool wrappers, normalizes targets into scopes, classifies effects, applies deterministic hard-safety rules, and matches **semantic approvals** stored in SQLite; approvals stop matching as soon as the resolved behavior or its fingerprints change. The Claude adapter maps daemon decisions onto `PreToolUse`/`PermissionRequest`/`PostToolUse` hooks — allow / deny / forced-ask / defer — in a way verified against Claude Code 2.1.233 (see `research.md` R-10), and imports Claude's native "don't ask again" consent into validated approvals. `agentguard setup claude` installs everything with one command on macOS, Linux and Windows.

## Technical Context

**Language/Version**: Go ≥ 1.22, `CGO_ENABLED=0`, module `github.com/agentguard/agentguard` (placeholder)

**Primary Dependencies**: `modernc.org/sqlite` (pure-Go SQLite), `mvdan.cc/sh/v3` (POSIX parse-only), `github.com/Microsoft/go-winio` (named pipes), `github.com/spf13/cobra` (CLI), `github.com/BurntSushi/toml` (config), `gopkg.in/natefinch/lumberjack.v2` (log rotation) — see `research.md` R-01…R-08

**Storage**: SQLite file `<DataDir>/agentguard.db`, WAL, schema v1 with forward-only migrations (`contracts/storage-schema.md`)

**Testing**: `go test` (table-driven), `-race` on Linux/macOS, `golangci-lint`, e2e tests driving the built binary with temp dirs; GitHub Actions matrix ubuntu/macos/windows; GoReleaser snapshot builds

**Target Platform**: macOS (arm64/amd64), Linux (arm64/amd64), Windows (arm64/amd64); per-user daemon (launchd / systemd --user / HKCU Run + lazy start)

**Project Type**: CLI + local daemon (single Go module, single binary)

**Performance Goals**: `evaluate` p95 < 100 ms warm; hook round trip < 500 ms p95; daemon start < 1 s; setup < 60 s

**Constraints**: local-only (no network listener), deterministic decisions, no LLM at runtime, fail-safe (never ALLOW on uncertainty/failure), one binary, no CGO, hooks always exit 0

**Scale/Scope**: single user per daemon; thousands of audit events; ≤ 32 commands per action; resolution depth ≤ 4; ≤ 500 fingerprint files; 5 s per-request budget

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

`.specify/memory/constitution.md` is the unfilled template (no ratified principles), so no constitution gates apply. The effective gates for this feature are the brief's design principles P1–P14 and invariants I-1…I-17 in `PROTOTYPE_SPEC.md`:

| Gate | Status (pre-research) | Status (post-design) |
|---|---|---|
| Local-first, no cloud/telemetry (P1) | PASS — UDS/named pipe only, no HTTP | PASS |
| Deterministic, no LLM at runtime (P2, P3, I-6) | PASS — rule engine + fingerprints | PASS |
| Fail-safe, no hidden unrestricted fallback (P4, P12, I-2, I-3, I-12) | PASS — deferral on failure, hooks exit 0 | PASS — verified exit-code semantics |
| Approve effects not strings; re-resolve wrappers (P5, P6, I-1, I-5) | PASS | PASS — consent import once-only |
| Cross-platform core, thin adapters, one daemon, one binary (P7–P10, I-7) | PASS — `platform/` isolation, `adapter/claude` only agent-aware code | PASS |
| Easy install (P11) | PASS — `setup claude` steps defined | PASS — service mechanisms decided (R-09) |
| Explainability (P13, I-17) | PASS — audit + history show | PASS |
| Prototype scope only (P14) | PASS — non-goals enforced | PASS — no new features added in design |

No violations → Complexity Tracking is empty.

## Project Structure

### Documentation (this feature)

```text
specs/001-agentguard-prototype/
├── brief.md             # Original product brief (verbatim)
├── spec.md              # Spec Kit feature spec (user stories, FRs, SCs)
├── PROTOTYPE_SPEC.md    # Technical contract (source of truth for behavior)
├── plan.md              # This file
├── research.md          # Phase 0: decisions + verified Claude Code behavior
├── data-model.md        # Phase 1: domain + persisted entities
├── quickstart.md        # Phase 1: build/test/demo validation guide
├── contracts/
│   ├── ipc-protocol.md  # daemon JSON protocol v1
│   ├── claude-hooks.md  # hook I/O contract + decision mapping
│   ├── cli.md           # command surface, exit codes, --json shapes
│   └── storage-schema.md# SQLite DDL v1
├── checklists/requirements.md
└── tasks.md             # Phase 2: implementation checklist (tick as you go)
```

### Source Code (repository root)

```text
cmd/agentguard/main.go                  # entry point: builds the internal/cli root command and executes it

internal/
  cli/             # cobra command implementations (root, version, daemon, setup/uninstall, hook, approvals, approve, history, status, doctor)
  logging/         # slog setup: JSON file handler with rotation for daemon/hook logs, text handler to stderr for CLI
  action/          # ActionRequest, Context, Target, Effect, ResolvedCommand/Action, Decision, enums
  adapter/         # Adapter interface + registry
  adapter/claude/  # hook I/O, decision mapping, settings discovery/merge/unmerge, rule matcher, consent, setup/uninstall steps
  parser/          # shared command model, Dialect interface, registry
  parser/posix/    # mvdan.cc/sh AST walker with node whitelist
  parser/powershell/ # minimal PowerShell parser
  parser/cmd/      # minimal cmd.exe parser
  resolver/        # recognizer registry; fs, git, npm/pnpm/yarn, gradle, maven, jstest, curl; wrappers; fingerprints
  scope/           # normalization, canonicalization, scope classification, generated roots, sensitive/tool-cache/temp
  policy/          # hard rules R1–R12, baseline B1, evaluation order, decision classes, explanations
  approval/        # approval model, creation, matching (EXACT/SEMANTIC), mismatch reports, consent import
  audit/           # audit event model, recording, correlation (tool_use_id / session+command)
  storage/         # sqlite open, migrations, repositories (projects, approvals, conditions, events, audit, imports, meta)
  daemon/          # server loop, single-instance lock, daemon.json, method router, caches, `evaluate dry_run` (used by the setup self-test)
  ipc/             # protocol types, framing, transports (uds, winpipe), client, server
  platform/        # Platform interface + shared helpers; per-OS implementations as build-tagged files in the same package:
                   #   dirs_{darwin,linux,windows}.go, pathrules_{darwin,linux,windows}.go, exec.go,
                   #   service_{darwin,linux,windows}.go (launchd / systemd --user / HKCU Run), spawn_windows.go (hidden detached spawn)
  config/          # TOML config, defaults, env overrides
  version/         # version, engine_version, protocol_version, schema constants

test/e2e/          # scenarios S1–S13 driving the built binary; fixtures under test/e2e/testdata
docs/              # validation records (docs/validation-<date>.md), design notes
.github/workflows/ci.yml   # matrix build/lint/test + goreleaser snapshot
.goreleaser.yaml, .golangci.yml, Makefile, README.md, LICENSE, install.sh, packaging/{homebrew,winget}/
```

**Structure Decision**: Single Go module with the layout mandated by `PROTOTYPE_SPEC.md` §6.2 (plus `internal/cli` and `internal/logging`). Dependencies point inward (`adapter/claude → action, ipc, config, platform`; `cli → ipc, config, platform, adapter/claude`; core packages never import `adapter/*` or `cli`) and the boundary is enforced by a `depguard` lint rule plus `TestInvariant_I7`. `runtime.GOOS` is confined to `platform/`, dialect selection helpers, and `scope/` path rules.

## Implementation Phases (roadmap)

Detailed, tickable tasks are in `tasks.md`; this is the shape of the work and its checkpoints.

| # | Phase | Delivers | Exit criterion |
|---|---|---|---|
| 1 | Setup | Go module, layout, Makefile, lint, CI matrix, GoReleaser snapshot, version pkg | `go build ./...` on all 3 OSes in CI |
| 2 | Foundational | domain model, platform layer, config, logging, storage+migrations, IPC (UDS/pipe), daemon skeleton (`ping/status/shutdown`), CLI skeleton, parser model, scope classification, context/workspace | daemon runs, `agentguard daemon status` works on all OSes; scope tests green |
| 3 | US1 MVP — approve once, auto-allow | POSIX parser, fs/git-read/npm recognizers, npm script resolution + fingerprints, policy engine (all hard rules + B1), approvals (create/match/mismatch), audit, `evaluate` & management methods, Claude adapter (hook I/O, mapping, consent import), CLI (`approvals`, `approve`, `history`, `history show`) | S1, S3 pass; DoD steps 2–4 demonstrable via hook JSON |
| 4 | US2 — invalidation & declared tools | gradle/maven/jstest recognizers with config fingerprints, curl recognizer, mismatch explanations, invalidation tests | S2, S6, S10 pass |
| 5 | US3 — hard safety & Windows dialects | PowerShell + cmd parsers, Windows recognizers, symlink/junction + traversal tests, credential paths, git push/force, bypass-mode mapping | S7, S8, S9 pass on all OSes |
| 6 | US4 — fail-safe | daemon-down deferral + rate-limited warning, lazy start, limits, ENGINE_ERROR paths, unsupported syntax/unknown exe coverage | S4, S5, S12 pass |
| 7 | US5 — setup/uninstall | Claude detection, settings backup/merge/unmerge, service managers (launchd/systemd/Run key), self-test, `doctor`, `status` | S13 + setup/uninstall tests pass; manual setup on 3 OSes |
| 8 | US6/US7 — explainability & baseline polish | history/approvals output quality, JSON output, read-only recognizers (`ls head tail wc pwd echo`), invariant test index | I-1…I-17 tests present; quickstart validated |
| 9 | Release polish | README, install script, packaging templates, perf check, GoReleaser dry run | DoD §30 checklist complete |

## Complexity Tracking

> No constitution violations to justify.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| — | — | — |
