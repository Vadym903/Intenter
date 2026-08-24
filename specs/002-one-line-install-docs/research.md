# Research: One-Line Installation & User Documentation (Phase 0)

**Feature**: `002-one-line-install-docs` · **Date**: 2026-08-16 · **Inputs**: `spec.md`, current repository state (`install.sh` skeleton, `README.md` with TODOs, `.goreleaser.yaml`, `.github/workflows/ci.yml`, `packaging/*`, `internal/platform/self.go`, `internal/cli/*`), feature 001 `PROTOTYPE_SPEC.md` (§8, §9, §11.7, §12).

Format: **Decision / Rationale / Alternatives**. Items marked **[repo]** were verified against the implemented code.

---

## R-01 Distribution home and installer URLs

- **Decision**: Releases live on the project's public GitHub repository (placeholder `agentguard/agentguard`, matching the Go module path); the installer scripts are served from the repository itself at stable raw URLs — `https://raw.githubusercontent.com/agentguard/agentguard/main/install.sh` and `…/install.ps1`. An optional short domain (e.g. `https://get.agentguard.dev` → GitHub Pages redirect/proxy) is a later cosmetic addition; all documentation is written with a single `INSTALL_URL` placeholder so switching is a search-and-replace.
- **Rationale**: Zero infrastructure, works the moment the repository is public, versioned with the code; raw URLs are the norm for `curl | sh` installers.
- **Alternatives**: vanity domain from day one (needs DNS + hosting; deferred), attaching the scripts only to releases (URL changes per release; breaks the "one stable command" goal).

## R-02 The four one-line commands (documented forms)

- **Decision**:
  - macOS/Linux install: `curl -fsSL INSTALL_URL/install.sh | sh`
  - Windows install (Windows PowerShell 5.1 or PowerShell 7): `irm INSTALL_URL/install.ps1 | iex`
  - macOS/Linux uninstall: `curl -fsSL INSTALL_URL/install.sh | sh -s -- --uninstall` (data kept; `--purge` to remove data)
  - Windows uninstall: `& ([scriptblock]::Create((irm INSTALL_URL/install.ps1))) -Uninstall`
  - Options: `--version X.Y.Z` / `-Version X.Y.Z` (pin), `--setup claude` / `-Setup claude` (opt-in run of `agentguard setup claude` right after install), `--prefix DIR` / `-InstallDir DIR`, `--no-modify-path` / `-NoModifyPath`, `--dry-run` / `-DryRun`, `--yes` (accept prompts, non-interactive default anyway). Environment equivalents: `AGENTGUARD_VERSION`, `AGENTGUARD_INSTALL_DIR`, `AGENTGUARD_NO_MODIFY_PATH=1`, `AGENTGUARD_REPO` (test override).
- **Rationale**: These are the shapes users already know (rustup, uv, Deno, Bun, Scoop); `sh -s --` and the PowerShell scriptblock form keep uninstall/pin one line without a second download; opt-in `--setup claude` matches the spec default (install only, print next step).
- **Alternatives**: `wget -qO- … | sh` documented as fallback text only; separate `uninstall.sh` (extra file to keep in sync — rejected).

## R-03 Install locations and PATH registration

- **Decision**:
  - macOS/Linux: `~/.local/bin/agentguard` (override `--prefix`). If `~/.local/bin` is not on PATH, append an idempotent, marker-delimited block (`# >>> agentguard >>> … # <<< agentguard <<<`) exporting it to the rc files of the detected login shell(s): zsh → `~/.zshrc` (and `~/.zprofile` on macOS), bash → `~/.bashrc` plus `~/.bash_profile`/`~/.profile` when they exist, fish → `~/.config/fish/conf.d/agentguard.fish` (`fish_add_path`). Always print the `export PATH=…` line for the current session. `--uninstall` removes exactly the marker blocks it wrote.
  - Windows: `%LOCALAPPDATA%\AgentGuard\bin\agentguard.exe`; add the directory to the **user** `Path` via `[Environment]::SetEnvironmentVariable('Path', …, 'User')` (no duplicates), broadcast `WM_SETTINGCHANGE` so new shells see it, and update `$env:Path` for the current session; `-Uninstall` removes only that entry.
- **Rationale**: Per-user, no elevation, stable across upgrades (a hard requirement because hooks and service definitions embed the binary path — `PROTOTYPE_SPEC.md` §11.7, §12.1). Marker blocks are reversible and idempotent.
- **Alternatives**: `/usr/local/bin` (needs sudo on many systems), `~/.agentguard/bin` (non-standard, one more PATH entry), modifying `/etc/paths` (system-wide).

## R-04 "Latest" resolution without API rate limits

- **Decision**: Resolve the latest tag by following the redirect of `https://github.com/<repo>/releases/latest` (`curl -fsSLI -o /dev/null -w '%{url_effective}'`; PowerShell `Invoke-WebRequest -MaximumRedirection 0` and read `Location`), extract `vX.Y.Z`, then download `https://github.com/<repo>/releases/download/vX.Y.Z/agentguard_X.Y.Z_<os>_<arch>.<tar.gz|zip>` and `checksums.txt` from the same release. Pre-releases are excluded from "latest" by GitHub automatically.
- **Rationale**: The current skeleton **[repo]** calls `api.github.com` (60 requests/hour per IP unauthenticated) — shared office/CI IPs would fail; the redirect trick needs no API and no token.
- **Alternatives**: `releases/latest/download/<asset>` (needs a version-less asset name; GoReleaser names include the version), a `latest.json` on Pages (extra publish step; keep as future option).

## R-05 Verification and download hygiene

- **Decision**: HTTPS only; download archive + `checksums.txt` to a temp dir; verify with `sha256sum -c` / `shasum -a 256 -c` (POSIX) or `Get-FileHash -Algorithm SHA256` (PowerShell) against the exact archive line; any mismatch → abort before touching the install dir; temp dir removed on exit/interrupt (`trap`/`try/finally`); atomic install (write to `agentguard.tmp` then rename over the old binary); honor `HTTPS_PROXY`/`https_proxy` (curl and `Invoke-WebRequest` do by default); require `curl` (print `wget` fallback text if missing); PowerShell forces `Tls12` when the default protocol set lacks it and runs `Unblock-File` on the extracted executable (mark-of-the-web).
- **Rationale**: FR-003/FR-008/FR-009; matches the existing skeleton's approach and extends it to Windows.
- **Alternatives**: cosign/minisign signature verification of `checksums.txt` — deferred (signing out of scope), but the checksums file format is kept compatible.

## R-06 Upgrade coherence (binary replaced while daemon/hooks are live)

- **Decision** (three complementary measures):
  1. **Stable path recording** — change `platform.SelfExecutablePath()` **[repo: currently `EvalSymlinks`]** to prefer the PATH-visible entry that is the *same file* (`os.SameFile`) as the running binary (`~/.local/bin/agentguard`, `/opt/homebrew/bin/agentguard`, `%LOCALAPPDATA%\Microsoft\WinGet\Links\agentguard.exe`), falling back to the resolved path. Hooks and service definitions then survive Homebrew/winget upgrades that move the real file.
  2. **Installer restarts the daemon** — after replacing the binary the scripts run `agentguard daemon restart` when a service/daemon is registered (ignore failure, print hint), so the new version serves immediately.
  3. **Daemon self-refresh** — the hook/CLI client sends its version in every request (`client_version` in the envelope, additive field); when the daemon sees a newer client version than its own it finishes the request and exits with a dedicated code; launchd `KeepAlive` / systemd restart policy (switch to `Restart=always`) / hook lazy start bring up the new binary. This covers package-manager upgrades that never call our installer.
- **Rationale**: Without (1) a `brew upgrade` silently disables AgentGuard (hooks point at a deleted Cellar path and Claude treats a missing hook as a non-blocking error); (2)+(3) keep FR-005 true for every channel.
- **Alternatives**: telling users to re-run `setup claude` after each upgrade (fragile), Homebrew `post_install` (cannot reach the user session).

## R-07 Release automation

- **Decision**: New workflow `.github/workflows/release.yml` on `push: tags: ['v*']` with a **verify-before-publish** pipeline: `build` (`goreleaser release --skip=publish`, upload `dist/`) → `verify-installers` (3-OS matrix runs the real `install.sh`/`install.ps1` against the built artifacts served by a local `tools/releaseserve` static server: install, upgrade, uninstall, < 60 s, `winget validate`) → `publish` (`goreleaser release --clean` with `release.draft: false` (**[repo: currently `draft: true`]**), `prerelease: auto` so `-rc`/`-beta` tags are pre-releases skipped by "latest") → `post-verify` (documented one-liners against the public URLs; on failure the release is demoted to pre-release with `gh release edit --prerelease` so "latest" rolls back). Changelog from the `CHANGELOG.md` section, `checksums.txt`, archives for the six targets (already configured). GoReleaser `brews:` publishes the formula to `agentguard/homebrew-tap` (needs `HOMEBREW_TAP_GITHUB_TOKEN`); GoReleaser `winget:` generates the manifest and, when `WINGET_GITHUB_TOKEN` is configured, opens the PR to `microsoft/winget-pkgs`; otherwise the manifest is attached to the release as `winget-manifest.zip` (spec FR-013 fallback). Keep the snapshot job in `ci.yml`. Release checklist includes `go mod tidy` (**[repo: `go.mod` marks all deps `// indirect`]**), CHANGELOG section, docs regeneration.
- **Rationale**: FR-011/FR-013 with tooling already chosen in feature 001; GoReleaser v2 has first-class Homebrew and winget publishers.
- **Alternatives**: hand-written formula/manifest updates (drift), separate publish workflows (more moving parts).

## R-08 Installer verification in automation

- **Decision**: New workflow `.github/workflows/install-test.yml` with a 3-OS matrix that (a) on PRs/pushes touching `install.sh`/`install.ps1`/the workflow runs the **local** scripts through the same entry points (`sh install.sh`, `pwsh -File install.ps1`) against the latest published release plus a pinned older release (upgrade path) and `--uninstall`; (b) on `release: published` and nightly runs the **exact documented one-liners** (remote URL); assertions: `agentguard version` equals the expected tag in a fresh shell (`bash -lc`, `pwsh -NoProfile` reading the user PATH), checksums verified (log line), no leftover temp files, uninstall removes binary + PATH block, `setup claude --dry-run` works after install (Claude shim). Also ShellCheck (`shellcheck install.sh`) and PSScriptAnalyzer (`Invoke-ScriptAnalyzer install.ps1`) as lint gates.
- **Rationale**: FR-012 / SC-005; a broken installer must be caught before users hit it.
- **Alternatives**: manual QA only (unacceptable for a one-liner promise).

## R-09 Package-manager channels

- **Decision**: Homebrew tap `agentguard/homebrew-tap` (formula `agentguard`), documented one-liner `brew install agentguard/tap/agentguard` (fully-qualified name needs no separate `brew tap`); winget package id `AgentGuard.AgentGuard` documented as "available once accepted upstream". Scoop is out of scope (spec).
- **Rationale**: Both are one line; both are generated by GoReleaser; winget availability is not under our control (spec assumption).
- **Alternatives**: `brew tap` + `brew install` (two lines), Homebrew core (requires notability), Scoop bucket (deferred).

## R-10 Windows installer specifics

- **Decision**: `install.ps1` targets Windows PowerShell 5.1 and PowerShell 7 (`#requires -Version 5.1`), detects arch via `$env:PROCESSOR_ARCHITECTURE`/`RuntimeInformation.OSArchitecture` (x64/arm64), extracts with `Expand-Archive`, installs to `%LOCALAPPDATA%\AgentGuard\bin`, `Unblock-File`, user PATH update + broadcast, prints next step; refuses elevation-only paths; supports `-Version`, `-InstallDir`, `-NoModifyPath`, `-Setup claude`, `-Uninstall`, `-Purge`, `-DryRun`. Because `irm | iex` cannot receive parameters, the documented parameterized form is `& ([scriptblock]::Create((irm URL))) -Version 0.2.0`; env-var equivalents (`$env:AGENTGUARD_VERSION`) also work.
- **Rationale**: FR-009; the scriptblock invocation is the established pattern for parameterized `irm | iex` installers.
- **Alternatives**: `.cmd`/`.bat` wrapper (no checksum verification tooling), MSI/MSIX (elevation, signing).

## R-11 Supported platforms statement

- **Decision**: macOS 12+ (arm64, amd64), Linux (x86_64, arm64; static binary — any distro incl. Alpine and WSL2), Windows 10 1809+/11 (x64, arm64) with PowerShell 5.1+. Unsupported combinations print the build-from-source instructions.
- **Rationale**: Matches the six release targets and static Go binaries.

## R-12 Documentation toolchain

- **Decision**: Markdown in-repo: rewritten `README.md`; `docs/install.md`, `docs/getting-started.md`, `docs/how-it-works.md`, `docs/security-model.md`, `docs/configuration.md`, `docs/troubleshooting.md`, `docs/faq.md`, `docs/cli/` (**generated** from cobra command tree via `github.com/spf13/cobra/doc` — already a dependency — through `go run ./tools/gendocs docs/cli`), `CONTRIBUTING.md`, `docs/release-process.md`, `CHANGELOG.md` (Keep a Changelog format). CI job `docs`: regenerate CLI docs and fail on diff (`git diff --exit-code docs/cli`), `lychee` link check (offline mode for relative links, online for external with retries), `markdownlint` (relaxed rule set), and a `README` placeholder check (`grep -c "TODO(" README.md` must be 0).
- **Rationale**: FR-014…FR-019 / SC-007/SC-008 without a website; generation keeps the CLI reference truthful by construction.
- **Alternatives**: hand-written CLI docs (drift), MkDocs/Docusaurus site (out of scope), man pages (nice-to-have; `cobra/doc` can emit them later).

## R-13 Newcomer walkthrough validation

- **Decision**: `docs/getting-started.md` mirrors the DoD demo from feature 001 `quickstart.md`; SC-006 is validated by a scripted "docs smoke" e2e (`test/e2e/docs_smoke_test.go`) that runs every fenced command tagged `console` in getting-started against the built binary + Claude shim where feasible, plus one recorded manual run per OS (`docs/validation-<date>.md`, extending feature 001 T079).
- **Rationale**: Turns "commands are copy-pasteable" into a check instead of a promise.

## R-14 Versioning & tags

- **Decision**: SemVer tags `vX.Y.Z`; first public release `v0.1.0`; pre-releases `vX.Y.Z-rc.N` (GitHub pre-release, excluded from latest); `agentguard version` prints the tag without `v`; `CHANGELOG.md` sections keyed by version.

## R-15 Interaction with feature 001

- **Decision**: This feature supersedes 001-T075 (README) and 001-T076 (install.sh/packaging); 001-T077 (perf), T078 (GoReleaser snapshot — already green), T079 (manual validation), T080 (DoD review) remain in feature 001 and are referenced, not duplicated.
