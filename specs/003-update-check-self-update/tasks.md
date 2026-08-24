# Tasks: Update Check & Guided Self-Update

**Input**: Design documents from `/specs/003-update-check-self-update/` — `plan.md`, `spec.md` (option B: prompt at terminal start-up), `research.md` (R-01…R-13), `data-model.md`, `contracts/update-cli.md`, `contracts/startup-hook-and-checker.md`, `quickstart.md`

**Prerequisites**: features 001 and 002 implemented (stable executable path, `SpawnDetached`, `ServiceManager`, `tools/releaseserve`, `test/install` harness, `setup claude` step framework, installers with `--no-modify-path`/`-NoModifyPath`).

**Tests**: The specification requires measurable guarantees (silence in non-interactive contexts, one prompt per interval, < 50 ms start-up cost, checksum refusal, byte-identical shell files); unit and e2e test tasks are therefore included.

**Organization**: Setup → Foundational (state, config, checker) → user stories in priority order (US1/US2/US3 P1 → US4/US5 P2) → polish. Tick a task by changing `- [ ]` to `- [x]`.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel with neighbours (different files, no unmet dependency)
- **[Story]**: US1 start-up prompt & update · US2 defer/skip/explicit command · US3 non-interactive silence · US4 hook lifecycle · US5 trust
- Paths are repository-relative

> **Reconciled with feature 005 (2026-08-19).** The one open item, T049 (manual validation of the update prompt on macOS, Linux and Windows), is tracked as `specs/005-make-product-usable/tasks.md` T033/T036/T037 (validation-template §5 "Upgrade in place") and is not duplicated here. Feature 005 also added signature verification to the updater (`checksums.txt.sig`, exit code 8) and renamed the product to Intenter; this file keeps its original wording as history.

## Progress summary

| Phase | Tasks | Done |
|---|---|---|
| 1 Setup | T001–T003 | 3/3 |
| 2 Foundational | T004–T011 | 8/8 |
| 3 US1 start-up prompt & update-now | T012–T019 | 8/8 |
| 4 US2 defer, skip, explicit command | T020–T024 | 5/5 |
| 5 US3 non-interactive silence | T025–T028 | 4/4 |
| 6 US4 hook lifecycle | T029–T036 | 8/8 |
| 7 US5 trust & channels | T037–T042 | 6/6 |
| 8 Polish & docs | T043–T050 | 7/8 |

**Outstanding**: T049 only — manual validation on macOS (zsh/bash/fish), Linux
(bash/zsh) and Windows (PowerShell 5.1 + 7). It needs the three machines and a
published release; everything it would exercise is covered by automated tests
except the fish and PowerShell prompts, which no machine here can run.

---

## Phase 1: Setup (Shared Infrastructure)

- [x] T001 Add `[updates]` config section to `internal/config/config.go` (`Check bool`, `CheckInterval`, `RemindInterval`, `PromptTimeout` as duration strings, `Channel string`, `StartupHook bool`; defaults `true/"24h"/"24h"/"30s"/"stable"/true`; validation of durations and channel enum; env overrides `AGENTGUARD_NO_UPDATE_CHECK=1` → `Check=false`, `AGENTGUARD_UPDATE_CHANNEL`); tests in `internal/config/config_test.go`
- [x] T002 [P] Create package skeleton `internal/updater/doc.go` (package purpose, file map, dependency rule: platform/config/version only) and add `internal/updater` to the core list in `.golangci.yml` depguard (must not import `adapter/*` or `cli`)
- [x] T003 [P] Add `updates.*` keys and `AGENTGUARD_NO_UPDATE_CHECK`/`AGENTGUARD_UPDATE_CHANNEL` to `docs/configuration.md`; add "Updating" placeholder section to `README.md` and `docs/updates.md` stub (filled in Phase 8)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: state, versioning, checking and channel detection that every story uses. ⚠️ Blocks all user stories.

- [x] T004 `internal/updater/semver.go`: strict SemVer parse/compare (prerelease ordering, `v` prefix tolerated, unparsable → error, never "newer"); tests `internal/updater/semver_test.go`
- [x] T005 `internal/updater/state.go`: `UpdateState` (data-model §1) load (missing → zero state), atomic save (temp+rename), `Eligible(now, cfg)` and `CheckDue(now, cfg)` per data-model rules, back-off schedule (1 h/6 h/24 h), skipped-version clearing when a newer latest arrives; tests `internal/updater/state_test.go`
- [x] T006 [P] `internal/updater/lock.go` (+ `lock_unix.go`/`lock_windows.go` using `golang.org/x/sys`): `state.lock` blocking writer lock and `prompt.lock` non-blocking with PID/timestamp and 10-minute staleness; tests `internal/updater/lock_test.go` (two processes/goroutines, stale takeover)
- [x] T007 [P] `internal/updater/history.go`: append `UpdateDecision` lines to `history.jsonl`, tail(n), truncate to last 500; tests `internal/updater/history_test.go`
- [x] T008 `internal/updater/check.go`: `Check(ctx, cfg)` — stable via redirect of `AGENTGUARD_LATEST_URL`/`https://github.com/<repo>/releases/latest` (no API), prerelease via `AGENTGUARD_RELEASES_ATOM_URL`/`releases.atom` first entry, 5 s timeout, HTTPS-only unless overridden, proxies from env, `User-Agent: agentguard-updater/<version>`, writes state under `state.lock`, records `check_ok`/`check_failed`; tests `internal/updater/check_test.go` with `httptest` (302 redirect, atom, timeout, 429, proxy env)
- [x] T009 [P] `internal/updater/channel.go`: install-channel detection (data-model §7) from `platform.SelfExecutablePath()` + resolved path + optional `<DataDir>/install-channel` hint; tests `internal/updater/channel_test.go` with fixture trees (Cellar symlink, WinGet Links/Packages, `~/.local/bin`, custom)
- [x] T010 [P] Test helpers `internal/updater/testutil_test.go`: build/fake release layout for the current OS/arch (reuse `tools/releaseserve` package or `httptest` mirror of it), temp DataDir/HOME, clock injection; implement the test-mode overrides from `contracts/update-cli.md` §"Test overrides" in `internal/updater` (`AGENTGUARD_TEST_TTY=1` treats stdio as interactive and `AGENTGUARD_TEST_NOW` overrides the clock — both ignored unless `AGENTGUARD_TEST_MODE=1`), with a unit test proving they are inert outside test mode
- [x] T011 `internal/daemon/updatecheck.go`: background goroutine started by the daemon (hourly ticker + on start after 2 min) calling `updater.Check` only when `CheckDue` and `updates.check` is true; never touches the request path; log lines; tests `internal/daemon/updatecheck_test.go` (fake clock, disabled config → no calls)

**Checkpoint**: `go test ./internal/updater ./internal/daemon ./internal/config` green on 3 OSes; state/lock/check contracts hold.

---

## Phase 3: User Story 1 — Prompt at terminal start-up and update in one step (Priority: P1) 🎯 MVP

**Goal**: `agentguard update --startup` shows the three-way prompt when eligible (< 50 ms otherwise) and "update now" replaces the binary, restarts the daemon and verifies.

**Independent Test**: `go test ./test/e2e -run TestUpdateStartupPrompt` (real `bash -i`/`zsh -i`/`fish -i`/`pwsh` with fake newer release) and `TestUpdateApply`.

- [x] T012 [US1] `internal/cli/update.go` skeleton: `update` command with flags per `contracts/update-cli.md` (`--check`, `--plan`, `--yes`, `--version`, `--skip`, `--unskip`, `--channel`, hidden `--startup`, hidden `--background-check`, sub-command `hook status|enable|disable --shell`), wired in `internal/cli/root.go`; exit-code mapping (0–7)
- [x] T013 [US1] Fast path for `update --startup` in `internal/cli/root.go`/`update.go`: bypass DB open, daemon client init and file logging; TTY/CI/env/config gates; read state; if check due and no daemon (`daemon.json` absent or 100 ms ping fails) → `platform.SpawnDetached(self, "update", "--background-check")`; latency test `internal/cli/update_startup_test.go` asserting < 50 ms with nothing to show
- [x] T014 [US1] `internal/updater/prompt.go`: prompt text (research R-05), line read with `prompt_timeout`, mapping `y/yes` → update, `s/skip` → skip, else/Enter/EOF/timeout → not now; requires `prompt.lock`; writes `last_prompt_at`, `deferred_until`/`skipped_version`; history events; tests `internal/updater/prompt_test.go` with fake stdin/timeouts
- [x] T015 [US1] `internal/updater/download.go`: asset name for runtime OS/arch, download archive + `checksums.txt` to `<DataDir>/update/tmp/<rand>` (cleanup on any exit), SHA-256 verify (mismatch → exit 3, nothing replaced), extract only the binary, sanity-run `<tmp>/agentguard version` == target; tests `internal/updater/download_test.go` (fake server, corrupted checksum, wrong version inside archive, interrupted download)
- [x] T016 [US1] `internal/updater/replace.go` (POSIX rename swap with mode 0755) and `internal/updater/replace_windows.go` (rename running exe → `.old`, new → exe, rollback on failure) + startup cleanup of stale `agentguard.exe.old` in `cmd/agentguard/main.go`; tests `internal/updater/replace_test.go` (Windows: replace while a copy of the binary is running)
- [x] T017 [US1] `internal/updater/apply.go`: orchestration per `contracts/startup-hook-and-checker.md` §3 — plan (installed→target, channel, path, actions), `prompt.lock`/`update_in_progress`, download → verify → replace, `daemon restart` via `platform.ServiceManager`/unmanaged when registered, wait ≤ 5 s for ping = target, verification limited to what core may know (service definition still points at an existing executable at the stable path; ping version) — **no Claude-settings access here** (`internal/updater` must not import `adapter/claude`), print result + notes link, exit codes 2/3/4/6/7, history events; tests `internal/updater/apply_test.go` (fake daemon binary in unmanaged mode)
- [x] T018 [US1] Wire "update now" from the prompt to `apply` inline in `internal/cli/update.go` (output continues in the same terminal; failures never break shell start-up: always exit 0 from `--startup` after printing); after `apply` returns, the CLI layer runs the Claude hook-path check via the adapter's doctor helper (`internal/adapter/claude`) and prints `agentguard setup claude` guidance on drift; also add a 60-second wall-clock measurement of the whole update printed as `updated in N s`
- [x] T019 [US1] E2E `test/e2e/update_test.go`: harness starts `tools/releaseserve` with a fake newer version and a temp HOME containing the start-up block (written via `agentguard update startup enable` once T029 exists — until then via a test fixture); shells are driven with `AGENTGUARD_TEST_MODE=1 AGENTGUARD_TEST_TTY=1` (contract "Test overrides") because Go tests have no pty, plus one real-PTY smoke test on macOS/Linux using `script -q /dev/null bash -ic '<cmd>'` to prove the TTY guards; CI installs `fish` and `zsh` where missing (`apt-get`/`brew`) and each shell test skips with a message when its shell is absent; then `TestUpdateStartupPrompt` (bash/zsh/fish/pwsh interactive → prompt once), `TestUpdateApply` (answer `y` → new version in same+new terminals, daemon restarted, `doctor` clean, approvals/history intact via a fixture DB, wall-clock < 60 s per SC-004), `TestUpdateStartupNothingToShow` (no output, < 50 ms)

**Checkpoint**: MVP — prompt appears in a fresh interactive shell and "y" updates end-to-end against a fake release.

---

## Phase 4: User Story 2 — Decline or defer without being nagged (Priority: P1)

**Goal**: "not now"/timeout quiet for the reminder interval; "skip" per version; one prompt across concurrent terminals; explicit `--check`, `--skip`, `update`.

**Independent Test**: `go test ./test/e2e -run 'TestUpdateNotNow|TestUpdateSkip|TestUpdateConcurrentTerminals|TestUpdateExplicit'`.

- [x] T020 [US2] Implement `update --check [--json]` in `internal/cli/update.go` (immediate check ignoring interval/back-off, print status table per contract; `--json` = `UpdateState` + `prompt_due` + last 20 history entries; exit 2 on check failure with status still printed) ; tests `internal/cli/update_test.go`
- [x] T021 [P] [US2] Implement `update --skip <version>` / `--unskip` and confirmation output; `--check` shows the skip; tests
- [x] T022 [US2] Interactive `agentguard update` flow: plan → `Proceed? [y/N]` (TTY) / exit 1 with "use --yes" (non-TTY) → apply; `--plan` prints and exits 0; `--yes` skips confirmation; `--version` explicit target with downgrade confirmation (never with `--yes` unless `--allow-downgrade`); tests
- [x] T023 [US2] Concurrency: `prompt.lock` in `--startup` (losers exit silently) and `update_in_progress` in `apply` (second run → exit 7 "update in progress"); e2e `TestUpdateConcurrentTerminals` (five parallel `zsh -i` → exactly one prompt) and `TestUpdateSecondApplyRejected`
- [x] T024 [US2] E2E `TestUpdateNotNow` (Enter and timeout → `deferred_until`, no prompt in new shells within interval, prompt after clock advance via `AGENTGUARD_TEST_NOW` override), `TestUpdateSkip` (skip 9.9.9 → no prompt; publish 9.9.10 → prompt), `TestUpdateExplicit` (`--check --json`, `--plan`, `--yes`)

**Checkpoint**: user decisions are respected across terminals and time.

---

## Phase 5: User Story 3 — Never disturb non-interactive or time-critical paths (Priority: P1)

**Goal**: zero output/network in hooks, daemon path, `--json`, non-TTY, CI; checks never block.

**Independent Test**: `go test ./test/e2e -run 'TestUpdateSilent'` and hook latency test unchanged.

- [x] T025 [US3] Gates in `update --startup` and prompt: non-TTY (stdin/stdout; `AGENTGUARD_TEST_TTY` honored only in test mode), `CI` set, `AGENTGUARD_NO_UPDATE_CHECK`, `updates.check=false`, `--json` anywhere → no prompt/no output; unit tests in `internal/updater/prompt_test.go`
- [x] T026 [P] [US3] Ensure the hook entry point (`internal/cli/hook.go`) and daemon evaluate path never touch `updater.Check`; add `TestInvariant_NoUpdateCheckOnHookPath` in `internal/adapter/claude/invariants_test.go` (fake release server must receive zero requests during hook runs) and a daemon handler test asserting no network during `evaluate`
- [x] T027 [P] [US3] Background checker never blocks: `--background-check` detached, 5 s timeout, back-off on failure; e2e `TestUpdateSilentNonInteractive` (`bash -c`, `zsh -c`, `fish -c`, `pwsh -NonInteractive -Command`, piped stdin → no output) and `TestUpdateOfflineStartup` (server down → start-up still < 50 ms, no output, failure recorded once, back-off honored)
- [x] T028 [US3] `agentguard doctor` additions in `internal/cli/doctor.go` (cached state only, no network): "update available: X (run `agentguard update`)", and a check "start-up update check: installed in <files> / not installed (run `agentguard update startup enable`) / blocked by PowerShell execution policy (fix …)"; `--json` shapes extended with stable keys; tests `internal/cli/doctor_test.go`

**Checkpoint**: silence guarantees proven; hook latency unchanged.

---

## Phase 6: User Story 4 — Start-up hook installed, removed and configured with the tool (Priority: P2)

**Goal**: managed `agentguard:update-check` block per shell; setup/uninstall integration; opt-out; Windows profiles + policy guidance; installers pass the opt-out.

**Independent Test**: golden tests for every shell + `TestSetupInstallsStartupHook`/`TestUninstallRemovesStartupHook` (byte-identical outside block).

- [x] T029 [US4] `internal/updater/startupcheck.go`: block templates (data-model §5) for zsh/bash/fish/PowerShell with the guards, marker constants `# >>> agentguard:update-check >>>`/`# <<< agentguard:update-check <<<`, `Install(shells)`, `Remove()`, `Status()`; shell detection (`$SHELL`, rc files, fish dir, `pwsh`/`powershell` presence); idempotent insert; removal leaves other content byte-identical (fish file deleted when it only held the block); every Install/Remove/Status updates `state.json` `startup_hook.*` under `state.lock` and appends `hook_installed`/`hook_removed`/`hook_blocked` history events; golden tests `internal/updater/startupcheck_test.go`
- [x] T030 [P] [US4] `internal/updater/startupcheck_windows.go`: profile paths for 5.1 and 7 (`Documents\WindowsPowerShell\profile.ps1`, `Documents\PowerShell\profile.ps1`, honoring redirected Documents), execution-policy probe via `powershell.exe -NoProfile -Command Get-ExecutionPolicy` / `pwsh` → `blocked_by_policy` + fix text; tests with an injected command runner
- [x] T031 [US4] Setup step "Start-up update check" in `internal/adapter/claude/setup.go` (after "Permission hooks installed"; `--no-startup-check` option in `SetupOptions` and `internal/cli/setup.go`; skipped when config `updates.startup_hook=false`; reports files and policy warning); tests `internal/adapter/claude/setup_test.go`
- [x] T032 [US4] Uninstall step "Start-up update check removed" in `internal/adapter/claude/uninstall.go`; tests (byte-identity fixtures for each shell)
- [x] T033 [P] [US4] `agentguard update startup status|enable|disable [--shell …]` in `internal/cli/update.go` using `startupcheck` (sub-command is named `startup`, never `hook`, to avoid confusion with Claude Code hooks); tests
- [x] T034 [P] [US4] Installer pass-through: `install.sh` (`--no-modify-path` ⇒ `--setup claude` runs `agentguard setup claude --no-startup-check`, and prints `agentguard update startup enable` hint) and `install.ps1` (`-NoModifyPath` ⇒ same); update `specs/002-one-line-install-docs/contracts/installer.md` (setup invocation note); tests in `test/install/*_test.go`
- [x] T035 [US4] E2E `TestSetupInstallsStartupHook` (setup with Claude shim → block present in detected shells; `hook status` reports), `TestUninstallRemovesStartupHook` (files byte-identical outside the block), `TestSetupNoShellHook`, Windows: `TestStartupHookProfilePolicy` (`-ExecutionPolicy Restricted` → shell unaffected, status `blocked_by_policy=true`)
- [x] T036 [US4] Repoint the Phase 3 e2e fixture (T019) to use `agentguard update startup enable` for block installation; remove the temporary fixture

**Checkpoint**: hook lifecycle complete for all four shells; installers and setup agree.

---

## Phase 7: User Story 5 — Trust the update (Priority: P2)

**Goal**: checksum refusal, no downgrade, channel rules, package-manager delegation, plan-only, in-use Windows swap.

**Independent Test**: `go test ./internal/updater -run 'TestApply|TestChannel'` and e2e `TestUpdateTrust*`.

- [x] T037 [US5] Delegation in `internal/updater/apply.go` for `homebrew` (`brew upgrade agentguard/tap/agentguard`, fallback `brew upgrade agentguard`) and `winget` (`winget upgrade --id AgentGuard.AgentGuard --exact`): run with inherited stdio after consent, then daemon restart + verify; `unknown` → print path, require confirmation (never `--yes`); tests with an injected command runner
- [x] T038 [P] [US5] Channel rules: stable users never offered pre-releases; `updates.channel=prerelease` (or installed pre-release) uses the atom feed; `--channel` override; tests `internal/updater/check_test.go`/`state_test.go`
- [x] T039 [P] [US5] Downgrade protection: `Eligible` never true for lower versions; `--version` lower than installed requires confirmation / `--allow-downgrade`; tests
- [x] T040 [US5] Read-only/shared install location and multiple-installs warning in `apply` (detect other `agentguard` on PATH that is not the stable path; warn which wins); tests
- [x] T041 [US5] E2E `TestUpdateTrustChecksum` (corrupted `checksums.txt` → exit 3, binary untouched), `TestUpdatePrereleaseNotOffered`, `TestUpdatePlanOnly` (nothing changes), `TestUpdateDelegatesToBrew` (fake `brew` on PATH + Cellar layout → delegated command invoked, Cellar file untouched), Windows `TestUpdateReplaceWhileRunning`
- [x] T042 [US5] Anonymity and opt-out assertions in `internal/updater/check_test.go`: fake server logs show only `GET/HEAD /releases/latest` (or atom) and asset/checksum downloads with `User-Agent: agentguard-updater/<ver>` and no query strings; with `updates.check=false` or `AGENTGUARD_NO_UPDATE_CHECK=1` the fake server receives **zero** requests across `--startup`, the daemon ticker and `--background-check` (SC-008), while `agentguard update --check` (explicit) still works

**Checkpoint**: trust properties proven; SC-005/SC-006 covered.

---

## Phase 8: Polish, docs & release

- [x] T043 Write `docs/updates.md` (how checks work, the prompt and choices, intervals/timeout, channels, opting out, `update` command reference, package-manager installs, Windows execution policy, anonymity) and README "Updating" section (final)
- [x] T044 [P] `docs/troubleshooting.md` entries: no prompt appears (hook status, policy, TTY, disabled), prompt every terminal (state file permissions), update failed mid-way (exit 6 guidance), daemon version mismatch
- [x] T045 [P] Regenerate `docs/cli/` (`make docs`) for the `update` family; `CHANGELOG.md` `[Unreleased]` entry
- [x] T046 [P] `install-test.yml`/`test/install` addition: after install, `agentguard update --check --json` works against the fake server; `--no-modify-path` path prints the hook-enable hint
- [x] T047 Latency guard in CI: `TestUpdateStartupNothingToShow` threshold 50 ms (warn-only on nightly), plus hook latency test from feature 001 re-run to confirm unchanged
- [x] T048 [P] Update `specs/002-one-line-install-docs/spec.md` "Out of scope" note (self-update now provided by feature 003) and `docs/install.md` upgrade section to mention the start-up prompt
- [ ] T049 Manual validation per `quickstart.md` on macOS (zsh/bash/fish), Linux (bash/zsh) and Windows (PowerShell 5.1 + 7); record in `docs/validation-<date>.md`
- [x] T050 Final review against `spec.md` SC-001…SC-008; file follow-ups (Claude-session notice, scheduled updates) as future features

---

## Dependencies & Execution Order

- Phase 1 → Phase 2 sequential; Phase 2 blocks all stories.
- Phase 3 (US1) is the MVP; Phase 4 (US2) and Phase 5 (US3) build on the same command and can proceed in parallel after T014/T017.
- Phase 6 (US4) depends only on Phase 2 (+ T012 for the `hook` sub-command) and can run in parallel with Phases 3–5; T036 re-points the e2e fixture once T029/T033 exist.
- Phase 7 (US5) depends on Phase 3 (apply) and T009 (channel).
- Phase 8 last.

### Parallel opportunities

- T002/T003 ∥ T001; T006/T007/T009/T010 ∥ T004–T005; T021 ∥ T020; T026/T027 ∥ T025; T030/T033/T034 ∥ T029; T038/T039 ∥ T037; T044–T046/T048 ∥ T043.

### Parallel example (after Phase 2)

```text
Dev A: T012→T013→T014→T018 (startup + prompt) → T020–T024 (US2)
Dev B: T015→T016→T017 (download/replace/apply) → T037–T042 (US5)
Dev C: T029→T030→T031→T032→T033→T034→T035 (hook lifecycle)
Dev D: T025–T028 (silence) → T043–T046 (docs)
```

## Implementation Strategy

1. **MVP** = Phases 1–3: a real interactive shell shows the prompt and "y" updates against a fake release.
2. **Respect decisions before shipping** (Phase 4/5): no nagging, no noise in scripts/hooks — these are P1 because a noisy prompt gets disabled.
3. **Hook lifecycle** (Phase 6) can proceed in parallel; without it the prompt never appears for real users.
4. **Trust** (Phase 7) before any release announcement; docs last (Phase 8).

## Success criteria review (T050)

Each criterion, and what actually holds it up. "Untested" is stated where it is
true rather than inferred from the code being present.

| # | Criterion | Evidence | Gap |
|---|---|---|---|
| SC-001 | Prompt in every interactive session, once per interval, never elsewhere | `TestUpdatePromptAppearsInRealShells` (zsh + bash through a **real pseudo-terminal**), `TestUpdateConcurrentTerminals` (1 of 5 prompts), `TestUpdateSilentNonInteractive`, `TestUpdateCheckNeverRunsOnTheHookPath`, `TestThePromptIsSilencedWhereItWouldDoHarm` | fish and PowerShell prompts are proven only by the block's contents and CI's own runners; no local machine has either |
| SC-002 | Skip honored 100%; "not now" and timeouts quiet for the interval | `TestUpdateStartupSkip`, `TestSkipIsForeverForThatVersionOnly`, `TestNotNowIsQuietForTheReminderInterval`, `TestAnUnansweredPromptCountsAsNotNow`, `TestUpdateNotNowIsForgottenAfterTheInterval` (clock advanced via `AGENTGUARD_TEST_NOW`) | — |
| SC-003 | Start-up cost under 50 ms | `TestUpdateStartupNothingToShow`: **best 6.3 ms, median 8.0 ms** on the development machine. Logs a warning over 50 ms; fails only an order of magnitude out, so a busy shared runner does not produce a false failure | the 50 ms figure is not enforced as a hard failure on CI |
| SC-004 | Update completes in under 60 s | `TestUpdateApply` asserts the wall clock; **~2.6 s** measured against a locally served real build | — |
| SC-005 | 100% checksum verification; a mismatch changes nothing | `TestACorruptedChecksumStopsEverything` (exit 3, nothing extracted), `TestUpdateTrustChecksum` end to end (installed version unchanged), `TestAnAssetMissingFromTheChecksumsFileIsRefused` | — |
| SC-006 | Package-manager installs never overwritten | `TestAPackageManagerInstallIsDelegatedNotOverwritten` (brew's file byte-identical, no download attempted), `TestAWingetInstallDelegatesToWinget`, `TestAFailedDelegationIsReportedAsSuch` | the delegated command is asserted, not executed: no test runs a real `brew` or `winget` |
| SC-007 | Install → uninstall byte-identical outside the block, all four shells | `TestRemovalIsByteIdenticalForEveryShell` (zsh, bash, fish, PowerShell — including CRLF), `TestUninstallRemovesStartupHook` end to end, `TestAFishFileTheUserAlsoEditedIsKept` | — |
| SC-008 | Checks off ⇒ zero requests, zero prompts | `TestNothingIsCheckedWithCheckingSwitchedOff` (e2e: start-up, background check, daemon, hook — via config *and* `AGENTGUARD_NO_UPDATE_CHECK`), `TestCheckingOffMeansNoRequestsAtAll`, `TestTheStartupCheckMakesNoNetworkRequestWhenCheckingIsOff` | — |

**Defect found and fixed during implementation**: the managed block's separator
was not reversible — installing after a file that did not end in a newline, then
uninstalling, left two extra newlines and broke SC-007. `trimOneNewline` and a
single always-one-newline separator fix it; `TestRemovalIsIdempotentAndDoesNotAccumulateBlankLines`
would have caught a regression.

**Documentation correction**: `README.md` and `docs/troubleshooting.md` both
claimed AgentGuard makes no network requests at all. That stopped being true
with this feature, and both now say what the one request is and how to switch it
off.

### Follow-ups (not in this feature)

- A notice inside a Claude Code session (spec options A/C, deliberately not chosen).
- Scheduled or unattended updates — the user is always asked.
- Signature/notarization verification beyond the published checksums.
- Delta updates, and a rollback command beyond `update --version <older> --allow-downgrade`.

## Notes

- Behavior questions: `contracts/*.md` first, then `research.md`; conflicts → update the contract before coding around it.
- Deviations recorded while implementing, each deliberate:
  - **T004**: the SemVer ordering itself stays in `internal/version`, which already implements it for the daemon's self-refresh; `internal/updater/semver.go` is the updater-facing surface (`Newer`/`Older`/`Same`/`Prerelease`/`ParseVersion`) over that one implementation. Two comparators that disagree by an edge case would mean the daemon and the updater disagreeing about which build is newer.
  - **T005**: `Eligible` takes the running version as a parameter rather than reading `state.installed_version`. The file may have been written by another build (a package-manager upgrade, a second installation), and offering an update to a version already installed is exactly the noise that gets a prompt disabled.
  - **T006**: the 10-minute staleness applies to the *recorded contents* of `prompt.lock`, not to the lock: `flock`/`LockFileEx` are released by the operating system when the holder exits, so a crashed prompt already frees it. Where a filesystem cannot lock at all (some network homes), the lock degrades to none rather than to a failure — two prompts is a better outcome than never prompting.
  - **T015**: the post-extraction sanity run is injectable (`Fetcher.SanityCheck`) so the "wrong build in the archive" case can be tested without compiling a binary per case; the real implementation is covered directly on POSIX and end to end everywhere.
  - **T017**: the daemon restart is an injected function (`Applier.Restart`) rather than implemented in the core, because "start the daemon, managed or unmanaged" already exists as `agentguard daemon restart` and two implementations of it would drift. Step 6 of the contract still happens inside `Apply`; a nil Restart reports the step as the user's to perform rather than skipping it silently.
  - **T019**: the per-shell rc-file tests (bash/zsh/fish/pwsh) are deferred to T035/T036 where the block writer exists; this phase drives `update --startup` directly, plus one real pseudo-terminal test through `script(1)` that proves the TTY guard itself. The concurrency test uses a first terminal *sitting at the prompt* rather than `flock(1)`, which is absent on macOS — and is the situation the lock actually exists for.
  - **T008**: `Checker.Check` does not consult `updates.check`. The unprompted callers (daemon ticker, `--startup`, `--background-check`) are responsible for not calling it, which is what makes `agentguard update --check` still work when checking is off (T042).
- Tests must never touch the real HOME/rc files or the real binary: temp HOME/DataDir, fake release server, fixture shells.
- Commit after each task; reference the task id (e.g. `T017`) in commit messages.
