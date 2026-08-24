# Implementation Plan: Update Check & Guided Self-Update

**Branch**: `003-update-check-self-update` | **Date**: 2026-08-16 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/003-update-check-self-update/spec.md` (clarified: option B — prompt at terminal start-up)

**Progress tracking**: `tasks.md` (tick `- [x]` as work completes)

## Summary

Add an `internal/updater` package and an `agentguard update` command family. A managed start-up block (separate markers from the installer's PATH block) in zsh/bash/fish rc files and both PowerShell profiles calls the hidden `agentguard update --startup`, which reads a small JSON state file (< 50 ms, never network-blocking) and, when a newer eligible release is known, shows a three-way prompt (update now / not now / skip this version) at most once per reminder interval across all terminals (file lock). Checks are performed in the background by the daemon (hourly ticker, 24 h interval) or by a detached process spawned from start-up when no daemon runs, using feature 002's release discovery (redirect of `/releases/latest`, atom feed for pre-releases). "Update now" downloads and checksum-verifies the matching release asset, atomically replaces the executable at the stable path (Windows rename swap), restarts the daemon, verifies the integration, and records the outcome; package-manager installs are delegated to `brew upgrade` / `winget upgrade`. `setup claude` installs the block for detected shells (with a `--no-startup-check` opt-out and Windows execution-policy guidance); `uninstall claude` removes it byte-exactly.

## Technical Context

**Language/Version**: Go 1.22 (updater, CLI, daemon ticker), POSIX sh / fish / PowerShell snippets (managed blocks only), Markdown docs

**Primary Dependencies**: standard library (`net/http`, `crypto/sha256`, `archive/tar`, `archive/zip`, `encoding/json`), `golang.org/x/sys` (file locks — already a dependency), existing `internal/platform` (`SelfExecutablePath`, `SpawnDetached`, `ServiceManager`), `internal/config`, `internal/cli` (cobra), `internal/daemon` (ticker), feature 002 `tools/releaseserve` + `test/install` harness for tests. No new third-party modules.

**Storage**: `<DataDir>/update/state.json` (atomic), `state.lock`, `prompt.lock`, `history.jsonl`, `tmp/` for downloads; no SQLite changes

**Testing**: unit (state/locks/semver/eligibility/channel/blocks/prompt/download/replace), e2e (`test/e2e/update_test.go`: real interactive shells with fake release server, driven through the test-mode override `AGENTGUARD_TEST_TTY=1` plus one real-PTY smoke test via `script -q /dev/null bash -ic …` on macOS/Linux; latency assertion; concurrency; non-interactive silence; Windows profile/policy), CI matrix ubuntu/macos/windows (CI installs `fish` and `zsh` where missing; tests skip with a message when a shell is absent)

**Target Platform**: macOS/Linux (bash, zsh, fish) and Windows (Windows PowerShell 5.1, PowerShell 7); `cmd.exe` unsupported for the prompt

**Project Type**: CLI + daemon extension (single Go module)

**Performance Goals**: `update --startup` ≤ 50 ms when nothing to show; background check ≤ 5 s; self-update < 60 s on CI runners

**Constraints**: never network-block user-facing paths; no checks in hooks/daemon decision path; anonymous checks; HTTPS + checksums; no downgrade by default; never overwrite package-manager files; one prompt/update at a time; files outside the managed block byte-identical

**Scale/Scope**: 1 new package (~10 files), 1 command family, 1 daemon goroutine, 1 setup step + 1 uninstall step, installer flag pass-through, ~4 docs pages touched, e2e suite

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

`.specify/memory/constitution.md` is still the unfilled template → no ratified gates. Effective gates from feature 001 principles/invariants:

| Gate | Pre-research | Post-design |
|---|---|---|
| P1 local-first / no telemetry — checks are anonymous, opt-out, no identifiers | PASS | PASS (R-03: no query params/cookies, UA only) |
| P4/P12 fail-safe — no path may block hooks/daemon decisions; failures never change installed state | PASS | PASS (R-02/R-06: rename-based swap, checksum gate) |
| I-9 setup/uninstall never destroy user settings — managed block only, byte-identical otherwise | PASS | PASS (separate markers, golden tests) |
| P10 one binary — no helper executables | PASS | PASS |
| P11 easy install — prompt makes staying current effortless; opt-out respected | PASS | PASS |
| Feature 002 upgrade-coherence contract respected (stable path, daemon restart) | PASS | PASS (R-06/R-11) |

No violations → Complexity Tracking empty.

## Project Structure

### Documentation (this feature)

```text
specs/003-update-check-self-update/
├── spec.md
├── plan.md              # This file
├── research.md          # R-01…R-13
├── data-model.md        # state.json, locks, history, hook blocks, config, channel detection
├── quickstart.md
├── contracts/
│   ├── update-cli.md                    # command family, prompt, setup/uninstall integration
│   └── startup-hook-and-checker.md      # block placement/guards, checker, self-update procedure, latency budget
├── checklists/requirements.md
└── tasks.md
```

### Source Code (repository root — additions/changes only)

```text
internal/updater/
  state.go            # UpdateState load/save (atomic), eligibility, check-due, back-off
  lock.go             # state.lock / prompt.lock (x/sys flock / LockFileEx), stale detection
  history.go          # history.jsonl append/tail
  semver.go           # strict SemVer parse/compare (prerelease aware)
  check.go            # latest discovery (redirect / atom), timeouts, proxies, UA
  channel.go          # install channel detection (homebrew/winget/script/manual/unknown)
  download.go         # asset+checksums download, sha256 verify, extract binary
  replace.go          # POSIX rename swap; replace_windows.go (rename .old, cleanup)
  apply.go            # orchestration: plan, confirm, replace/delegate, daemon restart, verify
  prompt.go           # start-up prompt with timeout, TTY detection, CI/env gates
  startupcheck.go     # managed block writer/remover/status per shell (zsh, bash, fish, powershell); updates state + history
  startupcheck_windows.go # profile paths, execution-policy probe
internal/cli/update.go            # `update`, `--check/--plan/--yes/--version/--skip/--unskip`, `startup status|enable|disable`, hidden --startup/--background-check;
                                  # after apply: Claude hook-path check via adapter doctor helper (CLI layer, not core)
internal/cli/root.go              # register update command; fast path for `update --startup` (no DB/logging init)
internal/config/config.go         # [updates] section + env AGENTGUARD_NO_UPDATE_CHECK / AGENTGUARD_UPDATE_CHANNEL
internal/daemon/updatecheck.go    # hourly ticker → updater.Check when due (background goroutine)
internal/adapter/claude/setup.go  # step "Start-up update check" (+ --no-startup-check); uninstall.go removes block
internal/cli/setup.go             # --no-startup-check flag; doctor.go: start-up check status + blocked_by_policy + "update available" line
cmd/agentguard/main.go            # startup: delete stale agentguard.exe.old (Windows)
install.sh, install.ps1           # pass --no-startup-check to setup when --no-modify-path/-NoModifyPath; mention `update startup enable`
docs/updates.md, docs/configuration.md, docs/troubleshooting.md, README.md, CHANGELOG.md, docs/cli/* (regenerated)
test/e2e/update_test.go           # start-up prompt in real shells, silence, latency, concurrency, self-update, uninstall byte-identity
internal/updater/*_test.go        # unit tests incl. golden shell blocks and fake release server
```

**Structure Decision**: New `internal/updater` package depends on `internal/platform`, `internal/config`, `internal/version` only (core dependency direction unchanged; `depguard` rules unaffected). The daemon imports `updater` for the ticker; the CLI imports it for commands; `adapter/claude/setup.go` imports it for the shell-hook step.

## Implementation Phases (roadmap)

| # | Step | Delivers | Exit criterion | `tasks.md` phase(s) |
|---|---|---|---|---|
| 1 | Foundation | `[updates]` config, updater state/locks/history/semver, channel detection, fake-release test helpers reuse | unit tests green on 3 OSes | 1 Setup, 2 Foundational |
| 2 | Start-up prompt (US1/US2/US3) | checker (daemon ticker + background/explicit), `update --startup` fast path, prompt + choices + locks, `--check/--skip` | e2e: prompt once in real shells, choices persisted, silence in non-interactive, latency < 50 ms | 3 US1, 4 US2, 5 US3 |
| 3 | Start-up check lifecycle (US4) | shell block writer/remover for zsh/bash/fish/PowerShell, setup step + `--no-startup-check`, uninstall removal, `update startup status|enable|disable`, execution-policy guidance, installer flag pass-through | golden tests + setup/uninstall byte-identity | 6 US4 |
| 4 | Self-update (US1/US5) | download/verify/replace (POSIX + Windows), delegation for brew/winget, daemon restart + verify, `--plan/--yes/--version` | e2e self-update with fake release; corrupted checksum refused; Windows in-use swap | 7 US5 |
| 5 | Docs & release | docs pages, README "Updating", regenerated CLI reference, CHANGELOG, feature 002 contract note (installer flag) | docs job green; quickstart validated | 8 Polish |

## Complexity Tracking

> No constitution violations to justify.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| — | — | — |
