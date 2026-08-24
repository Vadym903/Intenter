# Implementation Plan: One-Line Installation & User Documentation

**Branch**: `002-one-line-install-docs` | **Date**: 2026-08-16 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/002-one-line-install-docs/spec.md`

**Progress tracking**: `tasks.md` (generate with `/speckit-tasks`; tick `- [x]` as work completes)

## Summary

Turn the implemented AgentGuard prototype into something anyone can install with one pasted command on macOS, Linux and Windows, keep it upgradeable and removable through the same mechanism, and document it well enough that a newcomer reaches a first allowed/blocked decision in minutes. Concretely: finish `install.sh` and add `install.ps1` (detect → verify checksums → install to a stable per-user path → PATH → restart daemon → print next step; `--version`, `--uninstall`, `--setup claude`), publish releases automatically on tags (GoReleaser: non-draft releases, Homebrew tap, winget manifest), test the exact one-liners on fresh 3-OS runners, fix upgrade coherence in the binary (stable executable path for hooks/services; daemon self-refresh on newer client), and write the documentation set (README rewrite, `docs/*`, generated CLI reference, CONTRIBUTING, CHANGELOG) with automated link/placeholder/regeneration checks.

## Technical Context

**Language/Version**: POSIX `sh` (installer), PowerShell 5.1+/7 (installer), Go 1.22 (small binary changes + docs generator), YAML (GitHub Actions, GoReleaser v2), Markdown

**Primary Dependencies**: GoReleaser v2 (`brews`, `winget` publishers), `github.com/spf13/cobra/doc` (already in module — CLI docs generation), `lychee` (link check), `markdownlint-cli2`, ShellCheck, PSScriptAnalyzer; GitHub Releases as the artifact host; Homebrew tap repo `agentguard/homebrew-tap`; `microsoft/winget-pkgs` (external)

**Storage**: none (release assets on GitHub; per-user install dir; shell rc marker blocks / user PATH registry value)

**Testing**: install-test workflow on ubuntu/macos/windows (local-script mode on PRs; remote one-liner mode on release/nightly); ShellCheck/PSScriptAnalyzer; docs job (regen diff, links, placeholders, markdownlint); `test/e2e/docs_smoke_test.go`; unit tests for `SelfExecutablePath` stable-path logic and daemon self-refresh

**Target Platform**: macOS 12+ (arm64/amd64), Linux any distro incl. Alpine/WSL2 (amd64/arm64), Windows 10 1809+/11 (x64/arm64) with PowerShell 5.1+

**Project Type**: CLI distribution + documentation (scripts, CI workflows, small Go changes, Markdown)

**Performance Goals**: install < 60 s on broadband; release published < 30 min after tag; docs job < 5 min

**Constraints**: no elevation, HTTPS only, checksum verification mandatory (fail closed), no GitHub API in installers (rate limits), install path stable across upgrades, scripts non-interactive & idempotent, no telemetry, README zero placeholders

**Scale/Scope**: 2 scripts, 2 new workflows (`release.yml`, `install-test.yml`) + `ci.yml` modified (docs, shellcheck, PSScriptAnalyzer jobs), 2 small Go tools (`tools/gendocs`, `tools/releaseserve`), ~4 Go files touched (`platform/self.go`, ipc envelope `client_version`, daemon exit-on-newer-client, `doctor` checks), ~12 docs pages + generated CLI reference (~25 files)

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

`.specify/memory/constitution.md` is still the unfilled template → no ratified gates. Effective gates: feature-001 principles P1 (local-first), P4/P12 (fail-safe, no hidden fallback), P10 (one binary), P11 (easy install), I-9 (setup/uninstall never destroy user settings), I-12 (no failure path allows).

| Gate | Pre-research | Post-design |
|---|---|---|
| P11 easy install: one command per OS, no manual download | PASS (design R-02) | PASS |
| Fail-safe installer: verify or install nothing; no partial state | PASS (R-05) | PASS |
| P10 one binary; stable path so hooks/services keep working | PASS (R-03, R-06) | PASS |
| I-9 uninstall touches only what we created (marker blocks, PATH entry, hooks via `uninstall claude`) | PASS (contract) | PASS |
| P1 local-first: no telemetry, no accounts | PASS | PASS |
| Scope: no website/signing/self-update | PASS | PASS |

No violations → Complexity Tracking empty.

## Project Structure

### Documentation (this feature)

```text
specs/002-one-line-install-docs/
├── spec.md
├── plan.md              # This file
├── research.md          # R-01…R-15 decisions
├── data-model.md        # release, installer invocation, install layout, channels, docs set
├── quickstart.md        # validation guide
├── contracts/
│   ├── installer.md         # install.sh / install.ps1 flags, behavior, exit codes
│   ├── release-artifacts.md # tag→release, assets, "latest", upgrade coherence, brew/winget
│   └── docs-and-checks.md   # docs files, README sections, CI checks, install-test workflow
├── checklists/requirements.md
└── tasks.md             # /speckit-tasks output
```

### Source Code (repository root — additions/changes only)

```text
install.sh                          # finish: PS-parity flags, redirect-based latest, PATH marker blocks, daemon restart, --setup, --uninstall/--purge, --dry-run, exit codes
install.ps1                         # NEW: Windows installer (see contracts/installer.md)
.goreleaser.yaml                    # draft:false, prerelease:auto, brews:, winget:, footer/changelog
.github/workflows/release.yml       # NEW: tag → build → verify-installers (3 OS, local release server) → publish → post-verify (demote on failure)
.github/workflows/install-test.yml  # NEW: 3-OS installer verification (PR/local, release/remote, nightly, workflow_call)
.github/workflows/ci.yml            # + docs job (gendocs diff, lychee, markdownlint, placeholder grep), shellcheck + PSScriptAnalyzer jobs
tools/gendocs/main.go               # NEW: cobra/doc → docs/cli
tools/releaseserve/main.go          # NEW: static fake-release server (archives, checksums, /releases/latest 302) for tests and pre-publish verification
internal/platform/self.go           # stable-path preference (PATH entry with same file identity; symlink-into-Cellar rule)
internal/ipc/protocol.go            # + optional client_version in envelope
internal/ipc/client.go              # send client_version
internal/daemon/router.go|daemon.go # newer-client → graceful exit code 75; systemd unit Restart=always (platform/service_linux.go)
internal/cli/doctor.go              # checks: daemon≠CLI version, hook/service path ≠ stable path
README.md                           # rewrite (contracts/docs-and-checks.md sections)
CONTRIBUTING.md, CHANGELOG.md       # NEW
docs/{install,getting-started,how-it-works,security-model,configuration,troubleshooting,faq,release-process}.md   # NEW
docs/cli/*.md                       # GENERATED
test/e2e/docs_smoke_test.go         # NEW
packaging/homebrew, packaging/winget # reference-only templates (header comment says so); GoReleaser generates the real formula/manifest
Makefile                            # + docs, docs-check, lint-scripts targets
```

**Structure Decision**: Distribution and docs are repository-level assets; the only Go changes are the upgrade-coherence fixes and the docs generator, all inside the existing layout and dependency direction from feature 001 (`tools/gendocs` imports `internal/cli` — allowed for a build tool; add it to the depguard allow-list).

## Implementation Phases (roadmap)

| # | Roadmap step | Delivers | Exit criterion | `tasks.md` phase(s) |
|---|---|---|---|---|
| 1 | Release plumbing & upgrade coherence | `release.yml` (verify-before-publish), GoReleaser changes (non-draft, prerelease auto), `go mod tidy`, CHANGELOG skeleton, stable `SelfExecutablePath`, `client_version`, daemon self-refresh, systemd `Restart=always`, doctor checks, hermetic harness + `tools/releaseserve` | pre-release tag on a fork publishes 6 assets + checksums after passing installer verification; coherence unit tests green | 1 Setup, 2 Foundational |
| 2 | Installers | `install.sh` completion + `install.ps1`; contracts/installer.md behaviors; ShellCheck/PSScriptAnalyzer clean; uninstall/pin | hermetic tests green on 3 OSes; local runs install/upgrade/pin/uninstall | 3 US1, 4 US2, 5 US3 |
| 3 | Documentation | README rewrite, docs/*, generated CLI reference (`tools/gendocs`), docs smoke test, docs CI job | docs job green; zero placeholders; SC-006 manual run recorded | 6 US4 |
| 4 | Installer verification automation | `install-test.yml` (PR local mode with hermetic fallback, release/nightly remote mode, `workflow_call` for post-verify) with 60-second timing | green on ubuntu/macos/windows | 7 US5 |
| 5 | Package managers & contributor docs | GoReleaser `brews`/`winget`, tap repo bootstrap, Homebrew upgrade regression, CONTRIBUTING, release-process doc | brew one-liner verified; winget manifest generated/validated each release | 8 US6, 9 US7 |
| 6 | Release & verify | tag `v0.1.0-rc.1` then `v0.1.0`, verify one-liners from the README on fresh machines, publish tap, submit winget | quickstart §1–§7 all pass | 10 Polish & release |

## Complexity Tracking

> No constitution violations to justify.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| — | — | — |
