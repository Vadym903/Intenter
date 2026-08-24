# Data Model: One-Line Installation & User Documentation

**Feature**: `002-one-line-install-docs` · **Date**: 2026-08-16

This feature has no database; its "data" are release artifacts, installer inputs/state, on-disk install layouts, and the documentation set. Entities are described with fields, validation rules and states so the installer scripts, workflows and docs stay consistent.

## 1. Release

| Field | Rules |
|---|---|
| `tag` | `vMAJOR.MINOR.PATCH[-rc.N]`; pre-release suffix ⇒ GitHub pre-release, never "latest" |
| `version` | `tag` without leading `v`; equals `agentguard version` output |
| `assets[]` | exactly six archives `agentguard_<version>_<os>_<arch>.<ext>` with `os ∈ {darwin, linux, windows}`, `arch ∈ {amd64, arm64}`, `ext = tar.gz` (darwin/linux) or `zip` (windows); each contains `agentguard[.exe]`, `README.md`, `LICENSE` |
| `checksums` | `checksums.txt`: one line per asset `"<sha256>  <asset name>"` (GoReleaser format) |
| `notes` | body generated from the matching `CHANGELOG.md` section + GoReleaser commit list |
| `extras` | `winget-manifest.zip` when automatic winget submission is not configured |
| `state` | `draft` (never for tags) → `published` (visible to installers) → `latest` (GitHub's non-pre-release newest) |

Validation: a release is valid for the one-liners iff all six assets and `checksums.txt` exist and every asset's checksum matches (asserted by the install-test workflow).

## 2. Installer invocation (both scripts)

| Field | POSIX flag / env | PowerShell param / env | Default | Rules |
|---|---|---|---|---|
| `mode` | `--uninstall` | `-Uninstall` | install | install / uninstall |
| `version` | `--version X.Y.Z`, `AGENTGUARD_VERSION` | `-Version`, `$env:AGENTGUARD_VERSION` | latest | accepts `X.Y.Z` or `vX.Y.Z`; must exist as a release |
| `install_dir` | `--prefix DIR`, `AGENTGUARD_INSTALL_DIR` | `-InstallDir`, `$env:AGENTGUARD_INSTALL_DIR` | `~/.local/bin` / `%LOCALAPPDATA%\AgentGuard\bin` | must be creatable; warn if not writable |
| `modify_path` | `--no-modify-path`, `AGENTGUARD_NO_MODIFY_PATH=1` | `-NoModifyPath` | true | when false, only print instructions |
| `setup` | `--setup claude` | `-Setup claude` | none | runs `agentguard setup claude` after install; only value `claude` accepted |
| `purge` | `--purge` (with `--uninstall`) | `-Purge` | false | passes `--purge` to `agentguard uninstall claude` |
| `dry_run` | `--dry-run` | `-DryRun` | false | print plan, change nothing |
| `repo` | `AGENTGUARD_REPO` | `$env:AGENTGUARD_REPO` | `agentguard/agentguard` | test/enterprise mirror override |
| `base_url` | `AGENTGUARD_DOWNLOAD_BASE` | `$env:AGENTGUARD_DOWNLOAD_BASE` | `https://github.com/<repo>/releases/download` | mirror override |
| `latest_url` | `AGENTGUARD_LATEST_URL` | `$env:AGENTGUARD_LATEST_URL` | `https://github.com/<repo>/releases/latest` | must redirect to `…/releases/tag/vX.Y.Z`; hermetic-test / pre-publish-verification override |

Exit codes (both scripts): `0` ok · `1` usage/unsupported platform · `2` download or "latest" resolution failure · `3` checksum mismatch · `4` install location not writable · `5` uninstall step failed · `6` post-install step (daemon restart / setup) failed after a successful install (binary is installed; message says what to run).

## 3. Install layout (per OS)

| Item | macOS / Linux | Windows |
|---|---|---|
| Binary | `<install_dir>/agentguard` (0755) | `<install_dir>\agentguard.exe` (unblocked) |
| PATH registration | marker block `# >>> agentguard >>> … # <<< agentguard <<<` in shell rc files of the detected shell(s) | user `Path` entry `<install_dir>` (registry, no duplicates) + session `$env:Path` |
| Temp during install | `mktemp -d`, removed on exit/interrupt | `[IO.Path]::GetTempPath()\agentguard-<rand>`, removed in `finally` |
| Existing daemon/hooks | untouched by the installer; `agentguard daemon restart` invoked if a daemon is registered; hooks keep pointing at `<install_dir>/agentguard` (stable) | same |
| Data (kept on uninstall unless purge) | AgentGuard DataDir/ConfigDir (feature 001 §8.2) | same |

State transitions for an installation: `absent → installed(vN)` (install), `installed(vN) → installed(vM)` (re-run; atomic replace; daemon restarted), `installed → absent` (uninstall: `agentguard uninstall claude [--purge]` → remove binary → revert PATH block/entry).

## 4. Distribution channel

| Channel | Command (documented) | Produced by | Update cadence |
|---|---|---|---|
| Script (primary) | `curl -fsSL INSTALL_URL/install.sh \| sh` / `irm INSTALL_URL/install.ps1 \| iex` | repository files | every commit to `main` (scripts) + every release (binaries) |
| Homebrew tap | `brew install agentguard/tap/agentguard` | GoReleaser `brews` → `agentguard/homebrew-tap` | every release |
| winget | `winget install AgentGuard.AgentGuard` | GoReleaser `winget` (PR to `microsoft/winget-pkgs` or attached manifest) | every release; availability after upstream review |
| Manual / air-gapped | download archive + `checksums.txt`, verify, place binary | release page | every release |
| From source | `go build ./cmd/agentguard` | repository | — |

Rule: after any channel upgrade the daemon serves the new version within one hook round-trip (installer restart, or daemon self-refresh on newer client version — research R-06).

## 5. Documentation set

| Page | Path | Required content | Checked by |
|---|---|---|---|
| README | `README.md` | what/why, the four one-liners, package-manager one-liners, setup, demo, CLI overview, docs links, limitations, uninstall; **no `TODO(` markers** | placeholder grep, link check |
| Install | `docs/install.md` | all channels, per-OS notes (PS 5.1, WSL, PATH, proxies), pin/upgrade/uninstall/purge, air-gapped, unsupported platforms, verifying checksums manually | link check, docs smoke |
| Getting started | `docs/getting-started.md` | setup → first allowed → first blocked → inspect (`approvals`, `history show`) → uninstall; every command copy-pasteable | docs smoke e2e |
| How it works | `docs/how-it-works.md` | parse → resolve → classify → policy → approvals; invalidation; explanations; consent import; decision classes | link check |
| Security model | `docs/security-model.md` | from feature 001 `PROTOTYPE_SPEC.md` §27 + Appendix B resolutions; what is/isn't protected | link check |
| CLI reference | `docs/cli/*.md` (generated) | every command/flag with usage and examples | regen diff in CI |
| Configuration | `docs/configuration.md` | `config.toml` keys, env overrides, file locations per OS | link check |
| Troubleshooting | `docs/troubleshooting.md` | symptom → `agentguard doctor` check → fix (daemon down, hooks inactive, PATH, unsupported platform, permission modes, upgrade mismatch) | link check |
| FAQ | `docs/faq.md` | prompts, approvals, bypass mode, privacy (local-only), performance | link check |
| Contributing | `CONTRIBUTING.md` | build/test/lint/e2e, layout, specs location, PR expectations | link check |
| Release process | `docs/release-process.md` | tag → release → tap/winget → install-test → announce; checklist (`go mod tidy`, changelog, docs regen) | link check |
| Changelog | `CHANGELOG.md` | Keep-a-Changelog sections per version | release job reads it |
| Validation record | `docs/validation-<date>.md` | per-OS manual run results (extends 001-T079) | manual |
