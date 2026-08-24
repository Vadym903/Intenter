# Contract: Installer Scripts (`install.sh`, `install.ps1`)

Both scripts are the public, stable entry points behind the documented one-liners. Behavior below is normative; wording of messages may vary but the listed lines MUST appear.

## Invocation forms

| Purpose | macOS / Linux | Windows (PowerShell 5.1+ / 7) |
|---|---|---|
| Install latest | `curl -fsSL INSTALL_URL/install.sh \| sh` | `irm INSTALL_URL/install.ps1 \| iex` |
| Install + Claude setup | `curl -fsSL INSTALL_URL/install.sh \| sh -s -- --setup claude` | `& ([scriptblock]::Create((irm INSTALL_URL/install.ps1))) -Setup claude` |
| Pin a version | `curl -fsSL INSTALL_URL/install.sh \| sh -s -- --version 0.2.0` (or `AGENTGUARD_VERSION=0.2.0 …`) | `& ([scriptblock]::Create((irm INSTALL_URL/install.ps1))) -Version 0.2.0` (or `$env:AGENTGUARD_VERSION`) |
| Uninstall (keep data) | `curl -fsSL INSTALL_URL/install.sh \| sh -s -- --uninstall` | `& ([scriptblock]::Create((irm INSTALL_URL/install.ps1))) -Uninstall` |
| Uninstall + purge data | `… --uninstall --purge` | `… -Uninstall -Purge` |
| Dry run | `… --dry-run` | `… -DryRun` |
| Custom dir / keep PATH | `--prefix DIR`, `--no-modify-path` | `-InstallDir DIR`, `-NoModifyPath` |
| Help | `… --help` | `… -Help` |

`INSTALL_URL` = `https://raw.githubusercontent.com/agentguard/agentguard/main` until a vanity domain exists (research R-01).

## Behavior (install mode)

1. Detect OS/arch → `darwin|linux|windows` × `amd64|arm64`; anything else → exit 1 with build-from-source pointer.
2. Resolve version: pinned, else follow `https://github.com/<repo>/releases/latest` redirect → `vX.Y.Z` (no GitHub API). Failure → exit 2.
3. Download `agentguard_<ver>_<os>_<arch>.<ext>` and `checksums.txt` from `<base>/v<ver>/` over HTTPS into a private temp dir. Failure → exit 2.
4. Verify SHA-256 of the archive against `checksums.txt`; mismatch → delete temp, exit 3, message `checksum verification failed`. Print `verified sha256 …` on success.
5. Extract only the `agentguard[.exe]` entry; create `<install_dir>`; if a previous binary exists record its version (`<old> version --json` if possible); atomically replace (`tmp` + rename); POSIX mode `0755`; Windows `Unblock-File`.
6. PATH: POSIX — if `<install_dir>` not on PATH and `modify_path`, write marker block to detected shell rc files; always print `export PATH="<install_dir>:$PATH"` for the current shell when it was not already on PATH. Windows — add to user `Path` if absent, broadcast `WM_SETTINGCHANGE`, update `$env:Path`.
7. If a daemon is registered/running (`agentguard daemon status` succeeds), run `agentguard daemon restart` (failure → exit 6 after printing the command to run manually).
8. If `--setup claude`: run `agentguard setup claude` (its exit code propagates; a failure here → exit 6). When `--no-modify-path`/`-NoModifyPath` was given, pass `--no-startup-check` as well — feature 003 adds a managed block to the user's shell start-up files, and somebody who declined one edit to those files did not ask for another.
9. Print summary:
   ```
   AgentGuard <ver> installed to <install_dir>/agentguard   (upgraded from <old>)   ← second part only on upgrade
   Next step: agentguard setup claude                       ← unless --setup ran
   To be told about new releases when you open a terminal:  ← only with --setup and --no-modify-path
     agentguard update startup enable
   Open a new terminal to pick up PATH changes.             ← only if PATH was modified
   ```
10. Never: use `sudo`, write outside `<install_dir>`, temp dir and shell rc files (POSIX) / user `Path` (Windows); prompt for input (stdin is the script under `curl | sh`); leave temp files behind (trap/finally).

## Behavior (uninstall mode)

1. If `agentguard` exists at `<install_dir>`: run `agentguard uninstall claude [--purge]` (removes hooks + service, keeps or purges data); if it fails, print and continue with a warning (exit 5 at the end).
2. Remove the binary; remove the PATH marker block(s) / user `Path` entry the installer wrote (never other content).
3. Print what was removed and where data was kept (unless purged).

## Environment / flags precedence

Flag > environment variable > default. Unknown flags → exit 1 with help. `--yes` is accepted and ignored (there are no prompts).

Environment variables (both scripts): `AGENTGUARD_VERSION`, `AGENTGUARD_INSTALL_DIR`, `AGENTGUARD_NO_MODIFY_PATH=1`, `AGENTGUARD_REPO` (default `agentguard/agentguard`), `AGENTGUARD_DOWNLOAD_BASE` (default `https://github.com/<repo>/releases/download`), `AGENTGUARD_LATEST_URL` (default `https://github.com/<repo>/releases/latest`; must answer with a redirect to `…/releases/tag/vX.Y.Z` — used by hermetic tests and the pre-publish verification job to point at a local release server), `HTTPS_PROXY`/`https_proxy` (honored by `curl`/`Invoke-WebRequest`; when set and a download fails, the error message names the proxy).

## Idempotency

Re-running install with the same version is a no-op (`already installed`, exit 0). Re-running uninstall when nothing is installed exits 0 with `nothing to remove`.

## Lint & tests (see plan)

`shellcheck -s sh install.sh` clean; `Invoke-ScriptAnalyzer install.ps1` clean (Warning level); install-test workflow exercises install/upgrade/pin/uninstall on ubuntu/macos/windows runners.
