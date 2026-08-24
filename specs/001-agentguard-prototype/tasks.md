# Tasks: AgentGuard Prototype — Semantic Runtime Permission Layer for AI Coding Agents

**Input**: Design documents from `/specs/001-agentguard-prototype/` — `plan.md`, `spec.md`, `PROTOTYPE_SPEC.md` (behavioral source of truth), `research.md`, `data-model.md`, `contracts/`, `quickstart.md`

**Tests**: The specification explicitly requires automated tests (PROTOTYPE_SPEC §28, invariants I-1…I-17, scenarios S1–S13). Every implementation task below includes its unit tests; e2e scenarios are separate tasks.

**Organization**: Setup → Foundational (blocking) → user stories in priority order (US1 = MVP) → polish. Tick a task by changing `- [ ]` to `- [x]`. Keep this file as the single progress tracker.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel with neighbours (different files, no unmet dependency)
- **[Story]**: US1 approve-once/auto-allow · US2 invalidation · US3 hard safety · US4 fail-safe · US5 setup/uninstall · US6 explainability · US7 read-only baseline
- Paths are repository-relative; the layout is defined in `plan.md` → Project Structure

> **Reconciled with feature 005 (2026-08-19).** The one open item, T079 (manual validation with a real Claude Code session on three OSes), is tracked as `specs/005-make-product-usable/tasks.md` T033–T038 and is not duplicated here. The product was renamed to Intenter in feature 005; this file keeps its original wording as history.

## Progress summary

| Phase | Tasks | Done |
|---|---|---|
| 1 Setup | T001–T007 | 7/7 |
| 2 Foundational | T008–T024 | 17/17 |
| 3 US1 MVP | T025–T044 | 20/20 |
| 4 US2 invalidation | T045–T051 | 7/7 |
| 5 US3 hard safety | T052–T058 | 7/7 |
| 6 US4 fail-safe | T059–T062 | 4/4 |
| 7 US5 setup/uninstall | T063–T069 | 7/7 |
| 8 US6/US7 polish | T070–T074 | 5/5 |
| 9 Release | T075–T080 | 5/6 (T075–T076 superseded by feature 002; T079 needs three real machines) |

(Update the "Done" column when you tick tasks.)

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: repository skeleton, tooling, CI — nothing product-specific yet.

- [x] T001 Initialize Go module `go.mod` (module `github.com/agentguard/agentguard`, go 1.22), create the directory skeleton from `plan.md` (empty `doc.go` files where needed) and `cmd/agentguard/main.go` that prints version
- [x] T002 [P] Add `Makefile` (targets: `build`, `test`, `test-race`, `lint`, `e2e`, `cross` for the 6 targets, `snapshot`), `.gitignore`, `.editorconfig`, and a `LICENSE` placeholder (owner to choose license)
- [x] T003 [P] Add `.golangci.yml` (govet, staticcheck, errcheck, gosimple, ineffassign, unused, gofmt, goimports, misspell) plus a `depguard` rule set enforcing the dependency direction (I-7): packages under `internal/{action,parser,resolver,scope,policy,approval,audit,storage,daemon,ipc,platform,config,version,logging}` MUST NOT import `internal/adapter/...` or `internal/cli`; only `internal/cli`, `internal/adapter/**`, `cmd/**`, `test/**` may import adapters; make `make lint` pass on the skeleton
- [x] T004 [P] Add `.github/workflows/ci.yml`: matrix ubuntu/macos/windows → `go build ./...`, `make lint`, `go test ./...` (`-race` on ubuntu/macos), cross-compile all 6 targets with `CGO_ENABLED=0`, cache modules
- [x] T005 [P] Add `.goreleaser.yaml` (6 targets, `CGO_ENABLED=0`, ldflags for version, archives, checksums), `packaging/homebrew/agentguard.rb.tmpl`, `packaging/winget/AgentGuard.AgentGuard.yaml.tmpl`, `install.sh` skeleton
- [x] T006 [P] Create `internal/version/version.go` (Version via ldflags, `EngineVersion=1`, `ProtocolVersion=1`, `SchemaVersion=1`) + `internal/version/version_test.go`
- [x] T007 [P] Write `README.md` skeleton (what/why, install per OS placeholder, `agentguard setup claude`, demo pointer to quickstart, limitations placeholder)

**Checkpoint**: CI green on all three OSes for the skeleton.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: everything every story depends on. ⚠️ No user-story work starts before this phase is complete.

- [x] T008 Domain model in `internal/action/` — `enums.go` (Scope, EffectType, SemanticOp, ResolutionStatus, DecisionOutcome, DecisionClass, ApprovalKind, ApprovalState, TargetFlag, EffectFlag), `request.go` (ActionRequest, AgentConsent), `context.go` (Context, GitInfo, PackageManagerInfo), `target.go`, `effect.go` (Effect, NetworkTarget), `resolved.go` (Fingerprint, ResolvedCommand, ResolvedAction), `decision.go` (Decision, MismatchReport, EvaluationResult) with JSON tags per `data-model.md`; tests in `internal/action/model_test.go`
- [x] T009 Canonical hashing in `internal/action/canonical.go` (sorted-key canonical JSON, `ActionKey()`, `sha256Hex`, text normalization `\r\n`→`\n`) + `canonical_test.go` (determinism across orderings)
- [x] T010 [P] Platform interface `internal/platform/platform.go` (methods per PROTOTYPE_SPEC §8.1) and shared helpers `internal/platform/dirs.go` (env overrides `AGENTGUARD_*`, 0700 creation) + `internal/platform/exec.go` (FindExecutable with PATHEXT) + `internal/platform/self.go` (SelfExecutablePath); per-OS implementations as build-tagged files in the same package: `internal/platform/dirs_darwin.go`, `dirs_linux.go`, `dirs_windows.go` (DataDir/ConfigDir/RuntimeDir/HomeDir/TempDir/IPCEndpoint/DefaultShellDialect) and `internal/platform/pathrules_darwin.go`, `pathrules_linux.go`, `pathrules_windows.go` (SYSTEM roots, standard HOME dirs, sensitive paths, tool caches, temp carve-outs); tests `internal/platform/platform_test.go`
- [x] T011 [P] Config in `internal/config/config.go` (TOML load from ConfigDir or `--config`, defaults table PROTOTYPE_SPEC §12.6, unknown-key warnings, validation) + `config_test.go`
- [x] T012 [P] Logging in `internal/logging/logging.go` (slog JSON to `<DataDir>/logs/daemon.log` and `hook.log` with lumberjack rotation; text handler to stderr for CLI; never log secrets) + `logging_test.go`
- [x] T013 Storage open + migrations: `internal/storage/db.go` (open, pragmas WAL/synchronous/foreign_keys/busy_timeout, refuse newer schema), `internal/storage/migrate.go` + embedded `internal/storage/migrations/0001_initial.sql` (DDL from `contracts/storage-schema.md`); tests `internal/storage/migrate_test.go` (fresh, idempotent restart, newer-schema refusal)
- [x] T014 Storage repositories: `internal/storage/projects.go`, `approvals.go` (insert, get, list, set state, usage update), `conditions.go`, `approval_events.go`, `audit.go` (insert, update prompt/execution/import fields, list with filters, get), `imports.go` (once-only insert with UNIQUE), `meta.go`; tests `internal/storage/repos_test.go` (round trips, concurrent writers, filters)
- [x] T015 IPC protocol + framing: `internal/ipc/protocol.go` (envelope, method constants, error codes, typed params/results mirroring `contracts/ipc-protocol.md`), `internal/ipc/framing.go` (one JSON + `\n`, 1 MiB cap); tests `internal/ipc/protocol_test.go`
- [x] T016 [P] IPC transports: `internal/ipc/transport.go` (Listener/Dialer interfaces), `internal/ipc/transport_unix.go` (UDS in 0700 dir, 0600 socket, path-length check, peer-UID verification), `internal/ipc/transport_windows.go` (go-winio pipe, current-user SDDL); tests `internal/ipc/transport_test.go` (per-OS build tags)
- [x] T017 IPC client/server: `internal/ipc/client.go` (endpoint discovery env→daemon.json→default, connect 1 s, request 5 s, single call helper), `internal/ipc/server.go` (accept loop, goroutine per conn, handler registry, per-request timeout, graceful stop); tests `internal/ipc/client_server_test.go`
- [x] T018 Daemon core: `internal/daemon/daemon.go` (startup sequence §9.3: config, logging, lock, DB+migrate, bind, `daemon.json` atomic write, serve, graceful shutdown on signals), `internal/daemon/lock.go` (single instance), `internal/daemon/router.go`, handlers `ping`, `status` (skeleton counters), `shutdown` in `internal/daemon/handlers_core.go`; tests `internal/daemon/daemon_test.go`
- [x] T019 CLI skeleton with cobra: `cmd/agentguard/main.go` → `internal/cli/root.go` (global flags `--json --data-dir --config -v`, exit codes), `internal/cli/version.go`, `internal/cli/daemon.go` (`daemon [run]`, `start|stop|restart|status|install|uninstall` — install/uninstall stubs until T066), unmanaged start via `platform.SpawnDetached` (`internal/platform/spawn.go` for POSIX, `internal/platform/spawn_windows.go` with `CREATE_NO_WINDOW` + `DETACHED_PROCESS`, stdio redirected to the log file), pid-file stop; tests `internal/cli/daemon_test.go`
- [x] T020 Parser shared model: `internal/parser/model.go` (ParsedCommand, SimpleCommand, Word, Redirection, UnsupportedConstruct, Dialect interface, `Registry`), `internal/parser/expand.go` (per-dialect expansion tables for `~ $HOME ${HOME} $PWD $TMPDIR $env:X %VAR%`, marks unexpanded vars); tests `internal/parser/model_test.go`
- [x] T021 Path normalization: `internal/scope/normalize.go` (steps 1–7 of §16.1: expansion hook, separators + MSYS `/c/` + UNC, absolutize vs effective cwd, lexical clean + `traversal`, canonicalize via `EvalSymlinks` on longest existing prefix + `symlink_escape`, stat, glob literal prefix + `wildcard`/`broad`, case-insensitive compare helper); tests `internal/scope/normalize_test.go` (temp fixtures, symlink skip logic, Windows junction via `mklink /J`)
- [x] T022 Scope classification: `internal/scope/classify.go` (SYSTEM roots incl. temp carve-outs, HOME + broad standard dirs, WORKSPACE, WORKSPACE_GENERATED via generated roots, OUTSIDE + `temp`, sensitive/tool-cache flags incl. config extras, self-protection paths) + `internal/scope/generated.go` (G(W) rules: node/gradle/maven markers, configured extras, symlink-escaping roots excluded); tests `internal/scope/classify_test.go`, `generated_test.go`
- [x] T023 Workspace/context: `internal/resolver/context.go` (nearest `.git`, project_hint fallback, W validation vs HOME/root/system → `WORKSPACE_UNDEFINED`, `project_id`, per-workspace cache), `internal/resolver/gitinfo.go` (`.git` file/dir, `HEAD` current branch, `config` remotes → hosts, `refs/remotes/<r>/HEAD` default branch, hooks dir incl. `core.hooksPath` from repo + `~/.gitconfig`/`~/.config/git/config`, hooks presence ignoring `*.sample`), `internal/resolver/pkgmgr.go` (npm/pnpm/yarn-classic/yarn-berry detection, `.npmrc` script-shell from W and HOME); tests with fixture `.git` trees in `internal/resolver/testdata/`
- [x] T024 Fingerprints: `internal/resolver/fingerprint.go` (key formats, content hashing with newline normalization, aggregate hashing over sorted `(rel path, hash)` sets, file-set enumeration with caps 500 files / 50 MiB, always re-hash < 1 MiB); tests `internal/resolver/fingerprint_test.go`

**Checkpoint**: `agentguard daemon run` + `agentguard daemon status` work on all OSes in CI; scope/normalize tests green including symlink/junction cases.

---

## Phase 3: User Story 1 — Approve intent once, stop being asked (Priority: P1) 🎯 MVP

**Goal**: `npm run cleanup` → resolved `rm -rf ./dist` → native prompt with AgentGuard summary → persistent approval (native "don't ask again" imported, or `agentguard approve`) → auto-allow in later sessions.

**Independent Test**: e2e S1 + S3 (`test/e2e/s01_readonly_test.go`, `test/e2e/s03_npm_cleanup_test.go`) pass; hook JSON round trip demonstrates DoD steps 2–4.

- [x] T025 [US1] POSIX parser `internal/parser/posix/parser.go` using `mvdan.cc/sh/v3/syntax`: AST walker with node whitelist (simple commands, quoting, `;` `&&` `||`, pipelines, redirections incl. ignored devices, env prefix with dangerous-var detection, `cd` tracking, comments, optional grouping/heredoc), unsupported detection (substitutions, loops/conditionals/functions, `eval`/`source`/`exec`/`xargs`/`export`/`alias`/`pushd`, background `&`, `sh -c`/`bash -c` wrappers, `sudo`/`doas`/`su` elevation wrapper parsing inner command with `elevated`), pipe-into-interpreter marking; tests `internal/parser/posix/parser_test.go` (table + fuzz `FuzzParse`)
- [x] T026 [P] [US1] Recognizer framework `internal/resolver/registry.go` (Recognizer interface, argument grammar helper with SAFE/SEMANTIC/UNKNOWN classes, default UNKNOWN→UNRESOLVED) + `internal/resolver/fs.go` (rm, cp, mv, mkdir, cat, grep, find with predicate whitelist/`-delete`/`-exec`; POSIX names) producing targets/effects/flags; tests `internal/resolver/fs_test.go`
- [x] T027 [P] [US1] Git recognizer `internal/resolver/git.go` (global `-C`, `--no-pager`; `-c`/other globals → UNRESOLVED; status/diff/log/show/branch/rev-parse per-subcommand ops; add; commit with hooks check and `--no-verify`; checkout/switch/restore incl. `discards_changes`; reset incl. `--hard`; push with remote host, branch, `force`/`delete`/`broad`, default/protected branch resolution); tests `internal/resolver/git_test.go` with fixtures
- [x] T028 [US1] npm/pnpm/yarn resolver `internal/resolver/npm.go` (`run`/`run-script`/`test`/`start`/`stop`/`restart`, non-builtin `yarn <s>`/`pnpm <s>`; nearest package.json bounded by W; pre/post; `--` passthrough; recursive `npm run` with cycle detection and depth limit; `install/ci/add/update/uninstall` → INSTALL_DEPENDENCIES UNRESOLVED unless `--ignore-scripts`; `npx`/`dlx`/`exec` local-bin rule; workspace flags → UNRESOLVED; dialect selection incl. yarn-berry posix, `.npmrc` script-shell, Windows dual-dialect union; fingerprints npm-script/npm-config); tests `internal/resolver/npm_test.go`
- [x] T029 [US1] Resolution pipeline `internal/resolver/resolve.go` (parse → recognize → wrapper resolution → normalize targets → aggregate ResolvedAction: status ordering, effects union, fingerprint union, explanation chain, `action_key`, limits: 32 commands, depth 4, 5 s budget) + `internal/resolver/dialect.go` (dialect registry lookup); tests `internal/resolver/resolve_test.go`
- [x] T030 [US1] Hard rules `internal/policy/hard_rules.go` (R1–R12 exactly as PROTOTYPE_SPEC §18.2, outcomes BLOCK/ASK_ALWAYS/PASS with rule ids and reason templates); tests `internal/policy/hard_rules_test.go` (positive/negative per rule)
- [x] T031 [US1] Policy engine `internal/policy/engine.go` (evaluation order §18.1: hard BLOCK → hard ASK_ALWAYS → unresolved/parse/context/ambiguous → baseline B1 → approvals → consent import → default ASK with mismatch class; ENGINE_ERROR on panic/error), `internal/policy/baseline.go` (B1 with config toggle), `internal/policy/explain.go` (deterministic reason/explanation templates), decision classes; tests `internal/policy/engine_test.go`, `baseline_test.go`
- [x] T032 [US1] Approval model + creation `internal/approval/model.go`, `internal/approval/create.go` (from stored ResolvedAction: EXACT default / SEMANTIC flag, envelope/targets/network/fingerprints, validation: status RESOLVED|DECLARED, hard PASS, no AMBIGUOUS, project match; approval_events `created`); tests `internal/approval/create_test.go` (I-11, I-16)
- [x] T033 [US1] Approval matching `internal/approval/match.go` (candidate ordering EXACT→SEMANTIC→id, rules 1–7 of §20.3, `action_key` short-circuit consistent with field-wise rules, usage tracking + `matched` events) + `internal/approval/mismatch.go` (related approvals by raw command/semantic ops/fingerprint keys, difference list: fingerprint changed, target diff, scope diff, added effects/flags/hosts, `not_matched` events); tests `internal/approval/match_test.go` (I-1, I-5, I-6)
- [x] T034 [US1] Consent import `internal/approval/import.go` (preconditions §19.5, once-only via `agent_rule_imports`, path (a) during evaluate → `RULE_IMPORT`, path (b) during report_execution → `imported_approval_id`, origin `claude_rule`); tests `internal/approval/import_test.go`
- [x] T035 [US1] Audit `internal/audit/audit.go` (record evaluate before response; `record_prompt` correlation by session+command ≤60 s or new row; `report_execution` by tool_use_id; explanation persisted; dry_run skip) ; tests `internal/audit/audit_test.go` (I-17: explanation reproducible from stored row)
- [x] T036 [US1] Daemon handlers `internal/daemon/handlers_evaluate.go` (`evaluate` incl. `dry_run`, result cache by tool_use_id and by session+command 60 s), `internal/daemon/handlers_approvals.go` (`list_approvals`, `get_approval`, `set_approval_state`, `create_approval`), `internal/daemon/handlers_history.go` (`list_history`, `get_history_event`), `internal/daemon/handlers_reports.go` (`record_prompt`, `report_execution` + import), full `status`; integration tests over real transport `internal/daemon/handlers_test.go`
- [x] T037 [US1] Claude hook I/O `internal/adapter/claude/hookio.go` (parse stdin JSON, tool filter Bash/PowerShell, dialect selection, `CLAUDE_PROJECT_DIR` hint, `ActionRequest` build, PostToolUse response summary ≤1 KiB) + `internal/adapter/adapter.go` (Adapter interface + registry); tests `internal/adapter/claude/hookio_test.go` with golden hook payloads in `testdata/`
- [x] T038 [US1] Claude settings + rule matcher `internal/adapter/claude/settings.go` (discover managed/user/project/local files with git-root resolution, parse permissions, cache by mtime/size) + `internal/adapter/claude/rules.go` (Bash/PowerShell rule grammar per `contracts/claude-hooks.md`: separators split, wrapper stripping, `*`/word-boundary/`:*`, exact vs pattern, leading env assignment ⇒ uncertain; consent computation → AgentConsent or nil; optional deny visibility); tests `internal/adapter/claude/rules_test.go` (table from docs examples)
- [x] T039 [US1] Decision mapping `internal/adapter/claude/mapping.go` (table in `contracts/claude-hooks.md`: allow/deny/forced-ask/defer by outcome, class and permission_mode; `systemMessage` for BLOCK and NO_MATCHING_APPROVAL; reason text with event id and `agentguard approve N` hint; PermissionRequest decision JSON; never non-zero exit) + `internal/adapter/claude/hook.go` (dispatch PreToolUse/PermissionRequest/PostToolUse; daemon calls; failure ⇒ defer; rate-limited daemon-down `systemMessage` via marker file in RuntimeDir) + `internal/cli/hook.go` (`hook claude`); tests `internal/adapter/claude/mapping_test.go` (full matrix outcome×class×mode) and `hook_test.go`
- [x] T040 [US1] CLI approvals `internal/cli/approvals.go` (`approvals` table + `--json`, `approval show|disable|enable|revoke`, `approve <event-id> [--semantic] [--note]`) via IPC client; tests `internal/cli/approvals_test.go` (golden output)
- [x] T041 [US1] CLI history `internal/cli/history.go` (`history` filters `--blocked/--asked/--allowed/--project/--session/--since/--limit/--json`, `history show <id>` full explanation); read-only direct-DB fallback when daemon down (warning); tests `internal/cli/history_test.go`
- [x] T042 [US1] E2E harness `test/e2e/harness_test.go` (build binary once per run, temp DataDir/ConfigDir/RuntimeDir/HOME via `AGENTGUARD_TEST_MODE`, start/stop daemon, helper `runHook(event, payload)`, helper `cli(args…)`, temp workspace factory with git fixture + package.json)
- [x] T043 [US1] E2E S1 `test/e2e/s01_readonly_test.go` (git status allow via B1; toggle off → ask/defer; approve → `git status --short`/`git -C . status` allow; `git diff` separate; `git status && rm -rf ~` block; `cd ~ && git status` ask)
- [x] T044 [US1] E2E S3 `test/e2e/s03_npm_cleanup_test.go` (first run defer + systemMessage; consent import path (b) via PostToolUse with a written `settings.local.json` rule → approval; alternative `agentguard approve`; new session → allow APPROVAL_MATCH; direct `rm -rf ./dist` → ask)

**Checkpoint**: MVP demonstrable end-to-end through hook JSON; `agentguard approvals`/`history` explain decisions. (Windows: only Claude's Bash tool — Git Bash, POSIX dialect — is covered until Phase 5 adds the PowerShell/cmd dialects; this matches Claude Code's default tool on Windows.)

---

## Phase 4: User Story 2 — Changed behavior is never silently trusted (Priority: P1)

**Goal**: approvals stop matching when script/target/scope/network/config fingerprints change; explanations name what changed; declared build/test tools carry config fingerprints.

**Independent Test**: e2e S10 (primary DoD), S2, S6 pass; `internal/approval/invalidation_test.go` table green.

- [x] T045 [US2] Gradle recognizer `internal/resolver/gradle.go` (task→op tables RUN_TESTS/BUILD/CLEAN/BUILD_TOOL_INFO/publish→UNRESOLVED, module qualifiers, SAFE vs UNRESOLVED flags per §15.5.2, declared envelope incl. tool caches and dependency-registry network, `gradle-config` fingerprint set); tests `internal/resolver/gradle_test.go`
- [x] T046 [P] [US2] Maven recognizer `internal/resolver/maven.go` (goals/phases mapping, flags, envelope, `maven-config` fingerprint set); tests `internal/resolver/maven_test.go`
- [x] T047 [P] [US2] JS test runners + rimraf `internal/resolver/jstest.go` (jest/vitest/mocha/`node --test` DECLARED RUN_TESTS with SAFE flags, path/unknown flags → UNRESOLVED; `rimraf` DECLARED FS_DELETE); tests `internal/resolver/jstest_test.go`
- [x] T048 [P] [US2] curl recognizer `internal/resolver/curl.go` (URL parsing → NetworkTarget host/port/scheme/method, `-X`, data/form/upload flags, `-o/-O` outputs, `-K` → UNRESOLVED, `-u`/bearer → `inline_credential`, `-k` → `insecure_tls`, SAFE list, unknown → UNRESOLVED, unexpanded vars → AMBIGUOUS); tests `internal/resolver/curl_test.go`
- [x] T049 [US2] Invalidation table tests `internal/approval/invalidation_test.go` (each signal in PROTOTYPE_SPEC §21 produces a mismatch with the expected difference strings; engine version mismatch; project change)
- [x] T050 [US2] E2E S10 `test/e2e/s10_invalidation_test.go` (approve cleanup → change to `rm -rf ~/Documents` → BLOCK R2 with mismatch report naming approval id, fingerprint key, target and scope diffs; `history show` output; variant `rm -rf ./src` → APPROVAL_MISMATCH → forced `ask` in default mode / defer in bypass) + `test/e2e/s02_tests_test.go` (gradle/maven/npm test approvals, equivalent invocations allow, `gradlew test publish` UNRESOLVED, build-file edit → mismatch) + `test/e2e/s06_network_test.go`
- [x] T051 [US2] Explanation quality pass: `internal/policy/explain.go` + `internal/approval/mismatch.go` render `resolved chain`, targets+scopes, effects+flags, fingerprint diffs, host diffs; `history show` golden test updated in `internal/cli/history_test.go`

**Checkpoint**: DoD steps 5 and 6 pass in CI on all OSes.

---

## Phase 5: User Story 3 — Catastrophic actions stopped regardless of approvals/mode (Priority: P2)

**Goal**: hard rules hold under PowerShell/cmd dialects, symlink/junction and traversal escapes, credential paths, force pushes, and bypass mode.

**Independent Test**: e2e S7, S8, S9 pass on macOS/Linux/Windows; adapter mode matrix tests pass.

- [x] T052 [US3] PowerShell parser `internal/parser/powershell/parser.go` (tokens, quoting, `;`, `&&`/`||`, `|`, `&` call operator, redirections, `$env:`/`$HOME`/`~`/`$PWD` expansion, cmdlet named/positional params, aliases table; unsupported: script blocks, `$(...)`, `Invoke-Expression`/`iex`, `Start-Process`, `-Command`, background `&`); tests `internal/parser/powershell/parser_test.go`
- [x] T053 [P] [US3] cmd.exe parser `internal/parser/cmd/parser.go` (`&`, `&&`, `||`, `|`, quoting `"`, `^` escapes, `%USERPROFILE%`/`%HOMEDRIVE%%HOMEPATH%`/`%TEMP%`/`%TMP%`/`%CD%`, redirections incl. `NUL`, `REM`/`::`, `cd`/`chdir`; unknown `%VAR%` → AMBIGUOUS); tests `internal/parser/cmd/parser_test.go`
- [x] T054 [US3] Windows-equivalent recognizers (tests live in `fs_windows_cmdlets_test.go`, not `fs_windows_test.go`: Go treats a `_windows_test.go` suffix as a GOOS build constraint, which would have stopped them compiling anywhere but Windows) `internal/resolver/fs_windows_cmdlets.go` (Remove-Item/Copy-Item/Move-Item/New-Item/Get-Content/Select-String/Get-ChildItem + aliases with `-Recurse/-Force/-LiteralPath/-Path/-Destination/-ItemType`) and `internal/resolver/fs_cmd_builtins.go` (del/erase, rd/rmdir `/s /q`, copy, move, md/mkdir, type, dir, findstr) mapping onto fs semantics; tests compiled and run on all OSes `internal/resolver/fs_windows_test.go`
- [x] T055 [US3] Scope escape hardening tests `internal/scope/escape_test.go` (traversal out of W, symlink escape incl. final-component and trailing-slash forms, Windows junction, generated-root symlink excluded, HOME/root/drive-root as W rejected, UNC → OUTSIDE+`network_path`, temp carve-outs, sensitive path list incl. self-protection and config extras)
- [x] T056 [US3] E2E S7 `test/e2e/s07_catastrophic_test.go` (`rm -rf ~/Documents`, `rm -rf ~`, `rm -rf /`, `rm -rf ~/*`, PowerShell and cmd variants → deny; bypass mode still deny; `create_approval` for such events rejected), S8 `test/e2e/s08_traversal_test.go`, S9 `test/e2e/s09_symlink_test.go` (skip symlink variant with reason if unprivileged on Windows; junction variant runs)
- [x] T057 [US3] Adapter permission-mode matrix `internal/adapter/claude/mapping_test.go` extended (default/acceptEdits/plan/bypassPermissions/dontAsk × every outcome/class) and hard-rule precedence tests (approval cannot override R1–R12: `internal/policy/precedence_test.go`, I-4)
- [x] T058 [US3] Git safety tests `internal/resolver/git_test.go` extended (force push to default/protected/unknown branch → ASK_ALWAYS; `--delete`/`--mirror`/`--all`; hooks present → UNRESOLVED; `--no-verify`; `reset --hard`/`checkout -- .` → `discards_changes`)

**Checkpoint**: safety floor verified on all OSes.

---

## Phase 6: User Story 4 — When unsure, native flow decides; never auto-allow (Priority: P2)

**Goal**: unknown/unsupported/ambiguous → deferral; daemon down → deferral + one warning; limits and internal errors → ASK.

**Independent Test**: e2e S4, S5, S12 pass; `TestInvariant_I3`, `I2`, `I12` green.

- [x] T059 [US4] Daemon-unavailable handling: `internal/adapter/claude/hook.go` deferral on connect/timeout/protocol errors, rate-limited warning marker `<RuntimeDir>/warned-<session>.json`, lazy start `internal/adapter/claude/lazystart.go` (spawn `SelfExecutablePath daemon run` once, retry ≤2 s, config `daemon.lazy_start`); tests `internal/adapter/claude/hook_failure_test.go` + E2E S12 `test/e2e/s12_daemon_down_test.go` (assert no allow/deny emitted, exit 0, warning once)
- [x] T060 [P] [US4] Limits and ENGINE_ERROR paths: request size (BAD_REQUEST), command length 64 KiB, depth 4, 32 commands, fingerprint caps, 5 s budget → UNRESOLVED/ASK; storage write failure → ASK `ENGINE_ERROR`; panic recovery in handlers; tests `internal/daemon/limits_test.go`, `internal/policy/engine_error_test.go`
- [x] T061 [P] [US4] E2E S4/S5 `test/e2e/s04_unknown_test.go` (unknown executable defer, `approve` rejected), `test/e2e/s05_unsupported_test.go` (loops, `$(...)`, `bash -c`, `curl … | sh` → forced ask R12; never allow even with matching EXACT approval)
- [x] T062 [US4] Windows/POSIX dual-dialect union test (in `npm_dialects_test.go`; `npm_windows_test.go` would be GOOS-constrained to Windows and never run in CI's other legs) for npm scripts on Windows CI (`internal/resolver/npm_windows_test.go`: `rm -rf ~/Documents` script → HOME DELETE under union → BLOCK) and `.npmrc` unknown script-shell → UNRESOLVED

**Checkpoint**: fail-safe invariants proven by tests.

---

## Phase 7: User Story 5 — One-command setup and clean removal (Priority: P3)

**Goal**: `agentguard setup claude` / `uninstall claude` on all OSes with backups, exact-ownership hook editing, per-user service, self-test, doctor.

**Independent Test**: setup/uninstall golden tests + e2e S13; manual setup on each OS per quickstart.

- [x] T063 [US5] Claude detection `internal/adapter/claude/detect.go` (executable discovery incl. `~/.local/bin/claude`, `~/.claude/local/claude`; `claude --version` ≤5 s; settings path; min-version warnings per research R-16); tests `internal/adapter/claude/detect_test.go` (fake PATH)
- [x] T064 [US5] Settings editing `internal/adapter/claude/settings_edit.go` (backup to `<DataDir>/backups/claude-settings-<ts>.json` keep 10; generic JSON tree merge preserving unrelated keys/hooks; AgentGuard-owned entry detection by command path + `hook claude`; shell form on macOS/Linux, exec form on Windows; stale-entry replacement; atomic write; invalid JSON refusal; unmerge for uninstall incl. empty-group cleanup); golden tests `internal/adapter/claude/settings_edit_test.go` (I-9)
- [x] T065 [US5] Service managers as build-tagged files: `internal/platform/service.go` (ServiceManager interface, unmanaged fallback shared logic, status enum), `internal/platform/service_darwin.go` (LaunchAgent plist RunAtLoad/KeepAlive, `launchctl bootstrap/bootout/kickstart`, status), `internal/platform/service_linux.go` (systemd --user unit `Restart=on-failure`, `daemon-reload`, `enable --now`, `is-active`; unmanaged fallback when systemd --user unavailable — hook lazy start provides first-use start, per FR-022), `internal/platform/service_windows.go` (HKCU Run value `"<exe>" daemon start`, status via pid/ping); tests with an injected command runner `internal/platform/service_test.go`; opt-in real-install tests behind `AGENTGUARD_SERVICE_TESTS=1`
- [x] T066 [US5] `daemon install|uninstall|start|stop|restart|status` wired to ServiceManager (managed) with unmanaged fallback in `internal/cli/daemon.go`; status output incl. mode; tests
- [x] T067 [US5] Setup orchestration `internal/adapter/claude/setup.go` (steps 1–8 with ✓/✗ lines, exit 3 on first required failure, idempotent re-run, `--dry-run`, `--settings`, `--no-service`, existing-rules inventory summary, self-test: ping, dry-run evaluate BLOCK for `rm -rf ~/Documents` and ASK for unknown tool, hook command-line execution with synthetic PreToolUse payload under `AGENTGUARD_SELFTEST=1` incl. Windows exec form, meta recording; each ✓/✗ line prints the step duration, e.g. `✓ Daemon running (0.8s)`) + `internal/adapter/claude/uninstall.go` (`--keep-daemon`, `--purge`) + `internal/cli/setup.go`, `internal/cli/uninstall.go`; tests `internal/adapter/claude/setup_test.go`
- [x] T068 [US5] E2E S13 `test/e2e/s13_hook_binary_test.go` (setup into a temp HOME with fake `claude` shim, verify hooks JSON, run the exact configured hook command line with a synthetic payload, verify valid JSON deny; assert `setup claude` wall-clock < 60 s (SC-004) with the service install stubbed; uninstall leaves unrelated hooks/settings JSON-equal; `uninstall --keep-daemon` leaves the service registered; `uninstall --purge` removes DataDir contents after writing a backup; setup twice → no duplicates)
- [x] T069 [US5] `internal/cli/doctor.go` (checks per `contracts/cli.md` with fixes) and `internal/cli/status.go`; tests `internal/cli/doctor_test.go`

**Checkpoint**: DoD steps 1, 7 satisfied; quickstart install section works on all OSes.

---

## Phase 8: User Story 6 & 7 — Explainability polish and read-only baseline (Priority: P3)

**Goal**: management output explains trust and decisions clearly; read-only commands never nag; invariant tests indexed.

**Independent Test**: golden CLI outputs; `go test ./... -run TestInvariant` lists I-1…I-17.

- [x] T070 [US6] Output polish `internal/cli/format.go` (table widths, truncation, no color when not a TTY, consistent time formatting, `--json` stable shapes) applied to `approvals`, `approval show`, `history`, `history show`, `status`, `doctor`; golden tests updated
- [x] T071 [P] [US7] Read-only recognizers set (`ls head tail wc pwd echo printf true false test [` and Windows `Get-ChildItem/dir/type`) in `internal/resolver/fs.go` + baseline tests `internal/policy/baseline_test.go` (`git status`, `grep -r`, `find . -name` allow; `cat .env` → R5 ask; `cat ../../etc/passwd` → R6; B1 off → ask)
- [x] T072 [P] [US6] PermissionRequest/PostToolUse correlation tests (`prompt_shown`, verbatim `permission_suggestions`, execution status by `tool_use_id`, PermissionRequest second-enforcement allow/deny) in `internal/adapter/claude/hook_test.go` and `internal/daemon/handlers_test.go`
- [x] T073 [P] Invariant test index: add `TestInvariant_I1…I17` named tests (delegating to existing tests where present) — `internal/approval/invariants_test.go`, `internal/policy/invariants_test.go`, `internal/adapter/claude/invariants_test.go`, `internal/scope/invariants_test.go`, `internal/audit/invariants_test.go`; `TestInvariant_I7_CoreIndependentOfAdapters` (`internal/action/invariants_test.go`) runs `go list -deps` over the core packages and fails on any `internal/adapter` or `internal/cli` import (belt-and-braces with the depguard rule from T003)
- [x] T074 [US6] Audit completeness review: every ALLOW/BLOCK row contains rule/approval id, targets+scopes, effects, fingerprints, mismatch report; add `internal/audit/completeness_test.go`

### Follow-ups found by the T074 review

- [x] F-1 `adapter_action` was never populated: §23.2 contracted the column and `data-model.md` §2.5 called it "reported by adapter after mapping", but `contracts/ipc-protocol.md` defined no method for reporting it, so the audit could not tell an ASK that *forced* Claude's dialog from one that *deferred* to it (§11.3). Closed by the additive `record_adapter_action` method: the adapter reports the mapping it applied after writing its response, on a short timeout, swallowing every failure (I-12); the daemon validates the value and stores it. Spec §11.3/§24, `data-model.md`, `contracts/ipc-protocol.md` and Appendix C updated.

---

## Phase 9: Release polish & Definition of Done

> **T075 and T076 are superseded by feature `002-one-line-install-docs`**, which
> absorbed them: the README rewrite and the full documentation set are its
> Phase 6, and the installers and packaging are its Phases 1–5 and 8. Track them
> in `specs/002-one-line-install-docs/tasks.md`. T077–T080 below remain open here.

- [x] ~~T075 README complete~~ — superseded by feature 002 (T035–T045, T060–T062) (install per OS incl. brew/winget/install.sh placeholders, `setup claude`, demo walkthrough from `quickstart.md`, CLI reference summary, limitations from PROTOTYPE_SPEC §27, resolved/open questions from Appendix B), `CHANGELOG.md`
- [x] ~~T076 [P] Finalize `install.sh`~~ — superseded by feature 002 (T015–T034, T054–T059) (detect OS/arch, download release asset + checksum, print next step) and packaging templates (`packaging/homebrew`, `packaging/winget`) — **superseded by feature `specs/002-one-line-install-docs` (together with T075); track there**
- [x] T077 [P] Performance tests: `TestEvaluateLatency` in `internal/daemon/perf_test.go` (p95 `evaluate` < 100 ms warm on fixture workspace) and `TestHookRoundTripLatency` in `test/e2e/harness_test.go` (< 500 ms p95 for `agentguard hook claude` end-to-end)
- [x] T078 GoReleaser snapshot in CI (`goreleaser release --snapshot --clean`) producing 6 artifacts; verify binaries start (`--version`) on each matrix OS
- [ ] T079 Manual validation per `quickstart.md` on macOS, Linux, Windows with a real Claude Code session; record results in `docs/validation-<date>.md` (include Claude Code version, screenshots/log excerpts of DoD steps 2–5)
- [x] T080 Final DoD review against PROTOTYPE_SPEC §30 — recorded in `docs/definition-of-done.md`; items 2–8 automated, item 1 and the three-OS claim of item 6 need the manual runs tracked as T079. Follow-ups filed there (vanity domain, code signing, Scoop, approval portability, gating the file-editing tools) (all S1–S13 green in CI, I-1…I-17 present, uninstall clean, README done); file follow-ups for anything deferred

---

## Dependencies & Execution Order

- **Phase 1 → Phase 2 → Phase 3**: strictly sequential (foundation before MVP).
- **Phase 3 (US1)** is the MVP and must complete before Phases 4–8 are meaningful; Phases 4, 5, 6 can proceed **in parallel** after Phase 3 (different packages: resolvers vs parsers vs adapter failure paths).
- **Phase 7 (US5)** depends only on Phase 2 + T039 (hook entry point) and can start in parallel with Phase 4.
- **Phase 8** after Phases 3–7; **Phase 9** last.
- Within stories: model → engine → daemon handlers → adapter/CLI → e2e.

### Parallel opportunities

- Setup: T002–T007 in parallel.
- Foundational: T010–T012 in parallel; T015/T016 in parallel; T021→T022 sequential; T023/T024 parallel after T022.
- US1: T026/T027 parallel with T025; T037/T038 parallel; T040/T041 parallel.
- US2: T045–T048 parallel.
- US3: T052/T053 parallel; T054 after both.
- Phases 4/5/6 in parallel by different people once US1 is done.

### Parallel example (after Phase 2)

```text
Dev A: T025 posix parser → T028 npm resolver → T029 pipeline
Dev B: T026 fs recognizer, T027 git recognizer → T030 hard rules → T031 engine
Dev C: T032–T034 approvals → T035 audit → T036 handlers
Dev D: T037–T039 Claude adapter → T040–T041 CLI → T042–T044 e2e
```

## Implementation Strategy

1. **MVP first**: Phases 1–3 → demonstrate DoD steps 2–4 with hook JSON (no real Claude session needed) → then S10 (Phase 4, T050) proves the hypothesis.
2. **Cross-platform from the start**: every phase's tests run in the 3-OS matrix; do not defer Windows parsers/services to the end (Phase 5 and 7 tasks are scheduled right after MVP).
3. **Fail-safe by construction**: hard rules and deferral paths (Phase 6) are tested with named invariant tests; a failing `TestInvariant_*` blocks merge.
4. **Stop at checkpoints** to validate independently; keep `tasks.md` and the Progress summary current.

## Notes

- Behavior questions are answered by `PROTOTYPE_SPEC.md` first, then `research.md`; if implementation reveals a contradiction, update the spec (record the change in Appendix C) before coding around it.
- Never run destructive commands in tests; use temp workspaces and `AGENTGUARD_TEST_MODE=1`.
- Commit after each task or logical group; reference the task id (e.g. `T033`) in commit messages.
