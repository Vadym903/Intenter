# Research: Update Check & Guided Self-Update (Phase 0)

**Feature**: `003-update-check-self-update` · **Date**: 2026-08-16 · **Inputs**: `spec.md` (option B chosen: prompt at terminal start-up), repository state after features 001/002 (`install.sh` managed PATH block with `# >>> agentguard >>>` markers, `install.ps1` user-PATH registry update, `internal/config` keys, `internal/adapter/claude/setup.go` step list, `internal/daemon/refresh.go` self-refresh, `internal/platform` stable path + `SpawnDetached`, `tools/releaseserve`, `test/install` harness).

Format: **Decision / Rationale / Alternatives**. Items marked **[repo]** were verified against the implemented code.

---

## R-01 Where the start-up prompt runs

- **Decision**: A managed, marker-delimited block `# >>> agentguard:update-check >>> … # <<< agentguard:update-check <<<` (distinct from the installer's PATH block **[repo: `# >>> agentguard >>>`]**) is written by `agentguard setup claude` (and removed by `agentguard uninstall claude`) into: `~/.zshrc` (zsh), `~/.bashrc` (bash; plus `~/.bash_profile` on macOS where Terminal starts login shells and `.bashrc` may not be sourced — the block is idempotent and guarded so it runs once per session), `~/.config/fish/conf.d/agentguard-update.fish` (fish), and on Windows `$PROFILE.CurrentUserAllHosts` for **both** hosts (`~\Documents\WindowsPowerShell\profile.ps1` for 5.1 and `~\Documents\PowerShell\profile.ps1` for 7). The block only calls a hidden subcommand: `agentguard update --startup` guarded by an interactive-shell test (`case $- in *i*)` + `[ -t 0 ] && [ -t 1 ]` for POSIX shells; `status is-interactive` for fish; `[Environment]::UserInteractive -and $Host.Name -ne 'ServerRemoteHost'` + `-not $env:AGENTGUARD_NO_UPDATE_CHECK` for PowerShell) and by `[ -x <exe> ]`.
- **Rationale**: Interactive rc files are the shells' own "terminal started" hook; a separate block keeps installer-owned and setup-owned lines independent (either can be removed without touching the other); a once-per-session guard (`AGENTGUARD_STARTUP_CHECKED` env exported by the block) prevents double runs when several files are sourced; the actual logic lives in the binary so shells only pay for one process start.
- **Alternatives**: pure-shell logic reading a JSON state file (fragile across sh/fish/PowerShell), a login-only hook (misses new tabs), a system-wide profile (touches other users).

## R-02 What the start-up path does and how fast it is

- **Decision**: `agentguard update --startup` (hidden): (1) exit 0 immediately unless stdin+stdout are TTYs and `CI`/`AGENTGUARD_NO_UPDATE_CHECK` are unset and config `updates.check` is true; (2) read `<DataDir>/update/state.json` (single small file, no DB, no IPC); (3) if a check is due (`now − last_check_at ≥ check_interval`, honoring back-off) and the daemon is not running (`daemon.json` absent or ping fails within 100 ms — skipped entirely if a fresh state exists), spawn `agentguard update --background-check` detached (`platform.SpawnDetached` **[repo]**) and continue; (4) if `latest_known` is eligible (newer than installed, not skipped, not deferred, channel matches) and no other terminal holds the prompt lock, show the prompt; else exit silently. Budget: < 50 ms when nothing to show (Go process start + one file read); the command must not initialize the SQLite store, cobra sub-trees beyond `update`, or logging to file.
- **Rationale**: SC-003 (< 50 ms) and FR-006 (never wait for the network); the daemon is the normal background checker, the detached spawn covers unmanaged/off situations without blocking the shell.
- **Alternatives**: synchronous time-boxed check at start-up (oh-my-zsh style; makes every terminal wait up to the timeout on bad networks — rejected), a long-running shell coprocess (rejected).

## R-03 Where checks run and how "latest" is discovered

- **Decision**: Two producers of `state.json`, one format: (a) the daemon runs a background ticker (every hour) and performs a check when due; (b) `agentguard update --background-check` (detached from the start-up hook) and `agentguard update --check` (explicit, synchronous, ignores the interval). Discovery reuses the release contract from feature 002: stable → follow the redirect of `https://github.com/<repo>/releases/latest` (no API, no rate limits); pre-release channel → parse the first entry of `https://github.com/<repo>/releases.atom`. Timeout 5 s, HTTPS only, proxies from environment, `User-Agent: agentguard-updater/<version>` (no identifiers), back-off on failure 1 h → 6 h → 24 h with the failure reason stored. Version comparison is strict SemVer; unparsable versions are never "newer".
- **Rationale**: FR-001/FR-002/FR-003; identical to the installer's resolution so both agree; a daemon-owned check keeps the cache fresh for most users.
- **Alternatives**: GitHub REST API (rate-limited), a hosted `latest.json` (extra publish step; possible later), checking on every CLI command (option A, not chosen).

## R-04 Update state, locking, concurrency

- **Decision**: `<DataDir>/update/state.json` written atomically (temp file + rename) under `<DataDir>/update/state.lock` (advisory lock: `flock` on POSIX, `LockFileEx` on Windows via `golang.org/x/sys` — already a dependency); readers do not lock (atomic rename guarantees a consistent file). A separate `<DataDir>/update/prompt.lock` (acquired non-blocking, with owner PID and timestamp; stale after 10 min) ensures at most one prompt/update across simultaneous terminals; losers exit silently. `deferred_until`, `skipped_version`, `last_prompt_at` are updated by the prompting process; `latest_known*` and `last_check_*` by checkers.
- **Rationale**: FR-007/FR-008/FR-018; file-based state works when the daemon is down and is readable in < 1 ms; locks are cheap and cross-platform with `x/sys`.
- **Alternatives**: SQLite `meta` table (needs the daemon or opening the DB from the hook — too slow/fragile), one combined lock (a check in progress would block a prompt).

## R-05 Prompt UX

- **Decision**: Text:
  ```
  AgentGuard 0.2.0 is available (you have 0.1.0).
  Release notes: https://github.com/<repo>/releases/tag/v0.2.0
  Update now? [y]es / [N]ot now / [s]kip this version  (auto "not now" in 30s):
  ```
  Line input read with a timeout (goroutine + select; no raw mode); Enter or unknown input → not now; `y` → update; `s` → skip `0.2.0`. After the choice the shell continues; the update output is printed inline. Timeout, intervals and channel are configurable.
- **Rationale**: FR-005/FR-007; a safe default (Enter = not now) because the prompt interrupts unrelated work; timeouts keep unattended terminals from hanging.
- **Alternatives**: single-key raw input (extra platform code, little benefit), default "yes" on Enter (surprising for a security tool).

## R-06 Self-update mechanics

- **Decision**: `internal/updater` implements: resolve target version → download `agentguard_<ver>_<os>_<arch>.<ext>` + `checksums.txt` from the download base (same asset names as feature 002) → verify SHA-256 → extract only the binary → **replace at the stable path** (`platform.SelfExecutablePath()` **[repo: stable-path rule]**): POSIX write `agentguard.new` in the same directory then `rename(2)` over the target (running processes keep the old inode); Windows rename the running `agentguard.exe` → `agentguard.exe.old`, rename `.new` → `agentguard.exe`, and every start of the binary deletes stale `agentguard.exe.old` next to it (in-use file rules allow renaming a running executable) → `agentguard daemon restart` (service manager or unmanaged) → verify inside `updater`: `ping` reports the new version and the service definition still points at an existing executable at the stable path; the Claude hook-path check runs afterwards in the CLI layer via the adapter's doctor helper (core packages never import `adapter/claude`) → print old→new + notes link. Never downgrade unless `--version` is explicit (then confirm). Read-only target dir → exit with guidance. All steps also available as `--plan` (print only) and `--yes` (no confirmation).
- **Rationale**: FR-012…FR-017; reuses the artifact contract and the coherence rules; rename-based swap is atomic on both platform families.
- **Alternatives**: re-running the installer script from the binary (needs curl/pwsh; two code paths), download to a versioned dir + symlink flip (breaks the stable-path assumption on Windows).

## R-07 Install-channel detection and package-manager delegation

- **Decision**: `internal/updater/channel.go` classifies the stable path: **homebrew** if the resolved executable lives under `*/Cellar/agentguard/*` or the stable path is under `/opt/homebrew/bin`, `/usr/local/bin` (macOS Intel brew) or `/home/linuxbrew/.linuxbrew/bin` and resolves into a Cellar; **winget** if the resolved path is under `*\Microsoft\WinGet\Packages\*` or the stable path under `*\Microsoft\WinGet\Links\*`; **script/manual** otherwise (`~/.local/bin`, `%LOCALAPPDATA%\AgentGuard\bin`, custom). For homebrew: offer/run `brew upgrade agentguard/tap/agentguard` (falls back to `brew upgrade agentguard`); for winget: `winget upgrade --id AgentGuard.AgentGuard --exact`; run only after the user chose "update now" (or `--yes`), stream output, then daemon restart + verify as usual. Unknown/ambiguous → treat as manual but print the detected path and ask for confirmation (not with `--yes`).
- **Rationale**: FR-014 / SC-006; overwriting a Cellar file breaks brew's bookkeeping; the package managers already implement upgrade correctly.
- **Alternatives**: an installer-written channel marker file (helps but not sufficient for brew installs that never ran our installer; kept as an optional hint `<DataDir>/install-channel`).

## R-08 Configuration & environment

- **Decision**: New `[updates]` config section **[repo: `internal/config` has no updates keys]**: `check = true`, `check_interval = "24h"`, `remind_interval = "24h"`, `prompt_timeout = "30s"`, `channel = "stable" | "prerelease"`, `startup_hook = true`. Environment: `AGENTGUARD_NO_UPDATE_CHECK=1` (disables checks, prompts and the start-up hook body), `AGENTGUARD_UPDATE_CHANNEL`, `CI` (any value → non-interactive). Documented in `docs/configuration.md`.

## R-09 Setup / uninstall / installer integration

- **Decision**: `agentguard setup claude` gains a step "Start-up update check" **[repo: step list in `setup.go`]** that installs the block for shells detected on the machine (`$SHELL`, existing rc files, fish dir, PowerShell profiles) unless `--no-startup-check` or config `updates.startup_hook=false`; the step reports which files were written and, on Windows, whether the current execution policy blocks profile scripts (`powershell.exe -NoProfile -Command Get-ExecutionPolicy -Scope CurrentUser` / `pwsh …`) with the fix `Set-ExecutionPolicy -Scope CurrentUser RemoteSigned`. `agentguard uninstall claude` removes the block (files otherwise byte-identical; fish file deleted if it only contained our block). Explicit management: `agentguard update startup enable|disable|status` — named "startup", never "hook", because in this project "hooks" are Claude Code hooks. The installers pass `--no-startup-check` to setup when the user chose `--no-modify-path` / `-NoModifyPath` (feature 002 scripts) and mention `agentguard update startup enable` afterwards. Install/remove/status update `state.json` (`startup_hook.*`) and append `hook_installed`/`hook_removed`/`hook_blocked` history events.
- **Rationale**: FR-009/FR-010; setup runs for every channel (script, brew, winget) so package-manager users get the prompt too; separate markers keep uninstall precise.

## R-10 CLI surface

- **Decision**: `agentguard update` (interactive confirm; `--yes` to skip; `--plan` prints the plan and exits; `--version X.Y.Z` explicit target; `--channel`), `agentguard update --check` (status + immediate check, `--json`), `agentguard update --skip <version>` / `--unskip`, `agentguard update startup enable|disable|status`, hidden `--startup` and `--background-check`. All decisions/outcomes appended to `<DataDir>/update/history.jsonl` and surfaced by `--check --json` (FR-020).

## R-11 Interaction with the daemon self-refresh (feature 002)

- **Decision**: After a successful replacement the updater always calls `daemon restart` explicitly; the self-refresh (exit 75 when a newer client talks to an older daemon whose executable changed) remains as the safety net for package-manager upgrades performed outside AgentGuard.

## R-12 Testing strategy

- **Decision**: Unit: state read/write/locking, SemVer compare, eligibility (skip/defer/channel), channel detection (fixture paths), shell block writer/remover per shell (golden files, byte-identical outside the block), prompt with fake stdin and timeout, download/verify/replace against `httptest` fake release (reuse `tools/releaseserve` and `test/install` harness helpers), Windows in-use swap. E2E (`test/e2e/update_test.go`): start `bash -ic`, `zsh -ic`, `fish -ic`, `pwsh -NoProfile -Command . <profile>; ...` with a temp HOME whose rc contains the block and a fake newer release → prompt appears once (two shells in parallel → one prompt), answers y/n/s produce the expected state and, for `y`, a real replacement + daemon restart; non-interactive shells (`bash -c`, `pwsh -NonInteractive`) print nothing; start-up latency test asserts `update --startup` < 50 ms when nothing to show. Windows profile/policy test uses `-ExecutionPolicy Restricted` to assert graceful behavior.

## R-13 Documentation

- **Decision**: `docs/updates.md` (how checks work, prompt choices, intervals, channel, opting out, package-manager installs, Windows execution policy), README section "Updating", `docs/configuration.md` `[updates]` keys, `docs/troubleshooting.md` entries, regenerated CLI reference (`agentguard update …`), CHANGELOG entry.
