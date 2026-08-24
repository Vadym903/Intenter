# Contract: `agentguard update` command family

All commands exit 0 on success; `1` usage/unsupported; `2` check/download failed; `3` checksum mismatch; `4` cannot write install location; `5` package-manager delegation failed; `6` post-update step failed (daemon restart / verification) — the binary is already replaced; `7` another update in progress.

| Command | Behavior |
|---|---|
| `agentguard update` | Resolve target (latest eligible for the channel unless `--version`); print plan (installed → target, channel, path, actions); ask `Proceed? [y/N]` unless `--yes`; perform the update (research R-06) or delegate to the package manager (R-07); print result and notes link. Non-TTY without `--yes` → prints the plan and exits 1 with "use --yes". |
| `agentguard update --plan` | Print the plan only; exit 0; changes nothing (also honors `--version`). |
| `agentguard update --yes` | No confirmation (scripted use); still refuses unknown channel without confirmation → exit 1 with guidance. |
| `agentguard update --version X.Y.Z` | Explicit target (allows downgrade only after an extra confirmation, never with `--yes` unless `--allow-downgrade`). |
| `agentguard update --check [--json]` | Perform an immediate check (ignores interval and back-off), update `state.json`, print status: installed, latest, channel, skipped, deferred-until, last check time/result, next check, start-up hook status. `--json` prints the `UpdateState` object plus `prompt_due: bool`. Exit 0 whether or not an update exists; exit 2 if the check failed (status still printed). |
| `agentguard update --skip <version>` / `--unskip` | Set/clear `skipped_version`; prints confirmation. |
| `agentguard update startup status\|enable\|disable [--shell zsh,bash,fish,powershell]` | Report / write / remove the start-up check block (data-model §5); `status` also reports Windows execution-policy blocking. (Named "startup", never "hook", to avoid confusion with Claude Code hooks.) |
| `agentguard update --startup` (hidden) | Start-up path (research R-02): silent unless a prompt is due; never network-blocking; < 50 ms when nothing to show; honors `AGENTGUARD_NO_UPDATE_CHECK`, `CI`, non-TTY, config `updates.check=false`. Prompt text and choices per research R-05; choice recorded; on `y` runs the update inline. |
| `agentguard update --background-check` (hidden) | Detached checker: acquires nothing user-visible, performs one check with a 5 s timeout, writes state, exits. |

Global flags apply (`--json` where noted, `--config`, `--data-dir`).

## Prompt (start-up) contract

```
AgentGuard <new> is available (you have <installed>).
Release notes: <notes_url>
Update now? [y]es / [N]ot now / [s]kip this version  (auto "not now" in <timeout>):
```
- Shown only when `prompt due` (data-model §1) and `prompt.lock` acquired.
- Input: `y`/`yes` → update now; `s`/`skip` → skip `<new>`; anything else, Enter, EOF or timeout → not now (`deferred_until = now + remind_interval`).
- After "update now" the update output follows inline; failure never blocks the shell (exit codes are printed, the shell continues).
- Package-manager channel: the "y" path prints and runs the delegated command (research R-07) after a second `Run "<cmd>"? [y/N]` only when the command needs elevation or is `unknown`; homebrew/winget commands run directly.

## Setup / uninstall integration

- `agentguard setup claude [--no-startup-check]` adds step **"Start-up update check"** after "Permission hooks installed": writes the block for detected shells (or reports "skipped (--no-startup-check)" / "disabled by config"), reports files, and on Windows warns when the execution policy blocks profile scripts (fix printed).
- `agentguard uninstall claude` removes the block from every file that contains it (and deletes `agentguard-update.fish` when it only contained the block); reports files.
- The installers (`install.sh`/`install.ps1`) pass `--no-startup-check` to `setup claude` when `--no-modify-path`/`-NoModifyPath` was given and print `agentguard update startup enable` as the way to add it later.
- After a successful `apply`, the CLI layer (`internal/cli/update.go`, not the core `internal/updater` package) runs the Claude hook-path check from the adapter's doctor helpers and prints `agentguard setup claude` guidance if the hook path no longer resolves; `internal/updater` verifies only the service definition path and the daemon `ping` version (dependency direction: core never imports `adapter/claude`).

## Records

Every check, prompt, choice, update and start-up-check change (`hook_installed`/`hook_removed`/`hook_blocked` event names kept for the history format) appends an `UpdateDecision` line to `<DataDir>/update/history.jsonl` (data-model §3); `agentguard update --check --json` includes the last 20 entries under `history`.

## Test overrides (honored only when `AGENTGUARD_TEST_MODE=1`)

| Variable | Effect |
|---|---|
| `AGENTGUARD_TEST_TTY=1` | treat stdin/stdout as an interactive terminal (lets Go e2e tests drive the prompt without a pty); production builds ignore it unless test mode is on |
| `AGENTGUARD_TEST_NOW=<RFC3339>` | clock override for eligibility/deferral/back-off logic |
| `AGENTGUARD_LATEST_URL`, `AGENTGUARD_RELEASES_ATOM_URL`, `AGENTGUARD_DOWNLOAD_BASE` | point discovery/downloads at a local fake release server (also used by the release verification job; plaintext allowed only when overridden) |

At least one e2e smoke test also drives a **real** pseudo-terminal on macOS/Linux via `script -q /dev/null bash -ic '<cmd>'` (no new dependency) to prove the TTY guards themselves.
