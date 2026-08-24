# Contract: Start-up Hook Blocks, Background Checker, Self-Update Procedure

## 1. Managed start-up block

Markers (all shells): first line `# >>> agentguard:update-check >>>`, last line `# <<< agentguard:update-check <<<`. Written/removed only by the AgentGuard binary (`setup claude`, `uninstall claude`, `update startup enable|disable`). Every install/remove/status call updates `state.json` (`startup_hook.installed_files`, `installed_at`, `blocked_by_policy`) under `state.lock` and appends a `hook_installed`/`hook_removed`/`hook_blocked` history event. Rules: never duplicated; removal leaves the rest of the file byte-identical; block references the stable executable path (feature 002 `SelfExecutablePath`); the block MUST NOT do anything else (no PATH changes — those belong to the installer's block).

Placement: zsh `~/.zshrc`; bash `~/.bashrc` (+ macOS `~/.bash_profile` when it does not source `.bashrc`); fish `~/.config/fish/conf.d/agentguard-update.fish`; PowerShell `~\Documents\WindowsPowerShell\profile.ps1` and `~\Documents\PowerShell\profile.ps1` (created if missing). Shell detection: `$SHELL`, existing rc files/dirs, `Get-Command pwsh/powershell` on Windows.

Guards inside the block (must all hold to run `update --startup`): interactive shell (`$-` contains `i` / fish `status is-interactive` / PS `[Environment]::UserInteractive`), stdin and stdout are TTYs (POSIX `[ -t 0 ] && [ -t 1 ]`), `AGENTGUARD_NO_UPDATE_CHECK` unset, `AGENTGUARD_STARTUP_CHECKED` unset (then exported to `1`), executable exists.

Windows execution policy: if `Get-ExecutionPolicy -Scope CurrentUser` (or effective policy) is `Restricted`/`AllSigned` for a host, `setup`/`hook status` report `blocked_by_policy=true` with the fix `Set-ExecutionPolicy -Scope CurrentUser RemoteSigned`; the block is still written (harmless while blocked).

## 2. Background checker

- Owners: daemon ticker (every 60 min: run when `check due`), `update --background-check` (detached from start-up when no daemon), `update --check` (explicit).
- Sources: stable → `HEAD/GET https://github.com/<repo>/releases/latest` following redirect → tag; prerelease → `GET https://github.com/<repo>/releases.atom` first `<entry><id>`/`<title>` → tag. Overrides for tests: `AGENTGUARD_LATEST_URL`, `AGENTGUARD_RELEASES_ATOM_URL`, `AGENTGUARD_DOWNLOAD_BASE`.
- Network: HTTPS only (unless overridden to a local test server), 5 s total timeout, proxies from environment, `User-Agent: agentguard-updater/<version>`, no query parameters, no cookies.
- Result handling: success → `latest_known`, `last_check_ok=true`, `check_failures=0`, `next_check_after=now+check_interval`, clear `skipped_version` if the new latest is newer than it; failure → `check_failures++`, `next_check_after = now + {1h,6h,24h}[min(failures-1,2)]`, `last_check_error`.
- Never runs on: hook entry point, daemon request path, `--json` commands, when `updates.check=false` or `AGENTGUARD_NO_UPDATE_CHECK=1`.

## 3. Self-update procedure (channel `script`/`manual`)

1. Acquire `prompt.lock` (or fail with exit 7 "update in progress"); write `update_in_progress`.
2. Determine `<os>_<arch>` (runtime), target version, asset name and URLs; print plan.
3. Download archive + `checksums.txt` to `<DataDir>/update/tmp/<random>/` (cleaned on any exit); verify SHA-256 (exit 3 on mismatch, nothing replaced).
4. Extract only `agentguard[.exe]`; sanity-run `<tmp>/agentguard version` and require it to print the target version (exit 2 otherwise).
5. Replace: POSIX — write `<dir>/.agentguard.new`, `chmod 0755`, `rename` over `<dir>/agentguard`; Windows — `MoveFileEx(agentguard.exe → agentguard.exe.old)`, `MoveFileEx(new → agentguard.exe)`; a failed second move restores the old name. Every binary start deletes stale `agentguard.exe.old` beside itself (best-effort).
6. `agentguard daemon restart` if a daemon is registered/running (managed or unmanaged); wait ≤ 5 s for `ping` = target version.
7. Verify (inside `internal/updater`): the service definition (launchd plist / systemd unit / Run key) still references an existing executable at the stable path, and `ping` reports the target version; print old → new, notes link; append `update_ok`. The Claude hook-path check is performed afterwards by the CLI layer using the adapter's doctor helper (core packages never import `adapter/claude`); it prints `agentguard setup claude` guidance on drift.
8. On any failure after step 5: print exit 6 guidance (`agentguard daemon restart`, `agentguard doctor`); the new binary stays.

Channel `homebrew` / `winget`: steps 1–2 then run the delegated command (research R-07) with inherited stdio, then steps 6–7. Channel `unknown`: print detected path and ask for confirmation (never with `--yes`).

## 4. Start-up latency budget

`agentguard update --startup` with nothing to show: ≤ 50 ms wall clock on CI runners (asserted by test); it MUST NOT open the SQLite database, connect to the daemon (except an optional 100 ms ping only when a check is due and state is stale), initialize file logging, or resolve Claude settings.
