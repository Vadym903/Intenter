# Data Model: Update Check & Guided Self-Update

**Feature**: `003-update-check-self-update` · **Date**: 2026-08-16

No database changes. State lives in small files under `<DataDir>/update/` owned by the `internal/updater` package; the daemon and the CLI both use that package.

## 1. UpdateState (`<DataDir>/update/state.json`, atomic writes, single JSON object)

| Field | Type | Rules |
|---|---|---|
| `schema` | int | `1` |
| `installed_version` | string | SemVer of the binary that last wrote the file (informational) |
| `install_channel` | enum `script\|homebrew\|winget\|manual\|unknown` | recomputed on every write from the stable path (research R-07) |
| `channel` | enum `stable\|prerelease` | effective channel at last check |
| `latest_known.version` | string? | newest eligible-channel version found by the last successful check |
| `latest_known.notes_url` | string? | `https://github.com/<repo>/releases/tag/v<ver>` |
| `latest_known.found_at` | RFC3339 | |
| `last_check_at` | RFC3339? | start of the last attempted check |
| `last_check_ok` | bool | |
| `last_check_error` | string? | short reason (proxy, timeout, HTTP status) |
| `check_failures` | int | consecutive failures → back-off 1 h / 6 h / 24 h |
| `next_check_after` | RFC3339 | `last_check_at + check_interval` or back-off |
| `skipped_version` | string? | exactly one version; cleared when a newer version becomes `latest_known` |
| `deferred_until` | RFC3339? | set by "not now"/timeout: `now + remind_interval` |
| `last_prompt_at` | RFC3339? | |
| `update_in_progress` | `{pid, started_at, target_version}`? | cleared on completion; stale after 30 min |
| `last_update` | `{from, to, at, result: ok\|failed, error?}`? | |
| `startup_hook` | `{installed_files: [path], installed_at?, blocked_by_policy?: bool}` | reported by `hook status` |

**Eligibility rule** (`prompt due`): `latest_known.version > installed_version` (SemVer, same or opted-in channel) ∧ `latest_known.version ≠ skipped_version` ∧ (`deferred_until` empty or past) ∧ (`last_prompt_at` empty or `now − last_prompt_at ≥ remind_interval`) ∧ `update_in_progress` empty/stale.

**Check due**: `next_check_after` empty or past ∧ checks enabled.

## 2. Locks (`<DataDir>/update/`)

| File | Purpose | Semantics |
|---|---|---|
| `state.lock` | serialize writers of `state.json` | advisory (`flock`/`LockFileEx`), held only around read-modify-write |
| `prompt.lock` | one prompt/update at a time across terminals | non-blocking acquire; contains `pid` + `started_at`; stale after 10 min; released on exit |

## 3. UpdateDecision (append-only `<DataDir>/update/history.jsonl`)

| Field | Type |
|---|---|
| `at` | RFC3339 |
| `event` | `check_ok\|check_failed\|prompt_shown\|choice_update\|choice_not_now\|choice_skip\|choice_timeout\|update_started\|update_ok\|update_failed\|hook_installed\|hook_removed\|hook_blocked` |
| `installed_version`, `target_version` | string? |
| `channel` | `script\|homebrew\|winget\|manual\|unknown` (install channel for update events) |
| `detail` | string? (error, files touched, delegated command) |

Retention: file truncated to the last 500 lines on write.

## 4. Release (remote, read-only) — as published by feature 002

`version`, `is_prerelease`, `assets[os_arch] = agentguard_<ver>_<os>_<arch>.<ext>`, `checksums.txt`, `notes_url`. Discovery: stable → redirect of `/releases/latest`; prerelease → first entry of `/releases.atom`.

## 5. StartupHook block (per shell)

| Shell | File | Block content (semantics) |
|---|---|---|
| zsh | `~/.zshrc` | if interactive (`case $- in *i*)`), TTY on stdin+stdout, `AGENTGUARD_NO_UPDATE_CHECK` unset, `AGENTGUARD_STARTUP_CHECKED` unset, and `<exe>` executable → `export AGENTGUARD_STARTUP_CHECKED=1; "<exe>" update --startup` |
| bash | `~/.bashrc` (+ `~/.bash_profile` on macOS if it does not source `.bashrc`) | same |
| fish | `~/.config/fish/conf.d/agentguard-update.fish` | `status is-interactive; and not set -q AGENTGUARD_NO_UPDATE_CHECK; and not set -q AGENTGUARD_STARTUP_CHECKED; and test -x <exe>; and begin; set -gx AGENTGUARD_STARTUP_CHECKED 1; <exe> update --startup; end` |
| PowerShell 5.1 | `~\Documents\WindowsPowerShell\profile.ps1` | `if ([Environment]::UserInteractive -and -not $env:AGENTGUARD_NO_UPDATE_CHECK -and -not $env:AGENTGUARD_STARTUP_CHECKED -and (Test-Path "<exe>")) { $env:AGENTGUARD_STARTUP_CHECKED = '1'; & "<exe>" update --startup }` |
| PowerShell 7 | `~\Documents\PowerShell\profile.ps1` | same |

Markers: `# >>> agentguard:update-check >>>` / `# <<< agentguard:update-check <<<` (fish and PowerShell use `#` comments too). Invariants: idempotent (never duplicated), removal restores the file byte-identically outside the block, `<exe>` is the stable path.

## 6. Configuration additions (`config.toml` `[updates]`)

| Key | Default | Meaning |
|---|---|---|
| `check` | `true` | master switch (checks, prompts, start-up body) |
| `check_interval` | `"24h"` | minimum time between background checks |
| `remind_interval` | `"24h"` | quiet period after "not now"/timeout and between prompts |
| `prompt_timeout` | `"30s"` | auto "not now" |
| `channel` | `"stable"` | `stable` or `prerelease` |
| `startup_hook` | `true` | whether setup installs/keeps the start-up block |

Environment: `AGENTGUARD_NO_UPDATE_CHECK=1`, `AGENTGUARD_UPDATE_CHANNEL`, `CI`.

## 7. Install channel detection (derived)

| Channel | Evidence |
|---|---|
| `homebrew` | resolved executable under `*/Cellar/agentguard/*` or stable path under a brew prefix `bin/` whose target resolves into a Cellar |
| `winget` | resolved path under `*\Microsoft\WinGet\Packages\*` or stable path under `*\Microsoft\WinGet\Links\*` |
| `script` | stable path equals `~/.local/bin/agentguard` or `%LOCALAPPDATA%\AgentGuard\bin\agentguard.exe`, or `<DataDir>/install-channel` says `script` |
| `manual` | any other writable location |
| `unknown` | detection failed (treated as manual with confirmation; never with `--yes`) |
