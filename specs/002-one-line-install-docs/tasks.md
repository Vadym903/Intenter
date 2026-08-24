# Tasks: One-Line Installation & User Documentation

**Input**: Design documents from `/specs/002-one-line-install-docs/` — `plan.md`, `spec.md`, `research.md` (R-01…R-15), `data-model.md`, `contracts/installer.md`, `contracts/release-artifacts.md`, `contracts/docs-and-checks.md`, `quickstart.md`

**Prerequisites**: feature `001-agentguard-prototype` Phases 1–8 implemented (binary, daemon, `setup/uninstall claude`, `doctor`); GitHub repository public before Phase 7 remote-mode tests and Phase 8 publishing.

**Tests**: The specification requires automated verification (FR-012 installer tests on three OSes, FR-016 CLI-reference sync, FR-018 link checks, docs smoke); test tasks are therefore included. Hermetic installer tests use a local fake release server so they run in CI without a published release.

**Organization**: Setup → Foundational (release plumbing + upgrade coherence, blocking) → user stories in priority order (US1/US2 P1 → US3/US4/US5 P2 → US6/US7 P3) → polish. Tick a task by changing `- [ ]` to `- [x]`.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel with neighbours (different files, no unmet dependency)
- **[Story]**: US1 macOS/Linux one-liner · US2 Windows one-liner · US3 uninstall/pin · US4 newcomer docs · US5 release publishing & installer verification · US6 package managers · US7 contributor docs
- Paths are repository-relative; `INSTALL_URL` = `https://raw.githubusercontent.com/agentguard/agentguard/main` until a vanity domain exists (research R-01)

> **Reconciled with feature 005 (2026-08-19).** Done in 005 and ticked below: T027 (PSScriptAnalyzer + PowerShell 5.1/7 parse jobs in CI, 005 T039) and T046 (help texts reviewed, reference regenerated, 005 T054). Tracked in `specs/005-make-product-usable/tasks.md` rather than here: T047 → 005 T055, T055 → 005 T028, T058/T059 → 005 T056/T057, T067 → 005 T030, T068 → 005 T036/T037, T069 → 005 T062, T070 → 005 T066. The product was renamed to Intenter in feature 005; the tap is `Vadym903/homebrew-tap`, the winget id `Intenter.Intenter`.

## Progress summary

| Phase | Tasks | Done |
|---|---|---|
| 1 Setup | T001–T005 | 5/5 |
| 2 Foundational | T006–T014 | 9/9 |
| 3 US1 macOS/Linux installer | T015–T022 | 8/8 |
| 4 US2 Windows installer | T023–T029 | 6/7 |
| 5 US3 uninstall & pin | T030–T034 | 5/5 |
| 6 US4 newcomer documentation | T035–T047 | 11/13 |
| 7 US5 release publishing & installer verification | T048–T053 | 6/6 |
| 8 US6 package managers | T054–T059 | 3/6 |
| 9 US7 contributor docs | T060–T063 | 4/4 |
| 10 Polish & release | T064–T070 | 3/7 |

### Blocked on a public repository, a published release, or another person

Prepared but not run — the artifacts each one needs are in place:

| Task | Needs | Prepared |
|---|---|---|
| T047 | A second person, one OS | `docs/validation-template.md` |
| T055 | The tap repository and its token | `packaging/homebrew/TAP-SETUP.md` |
| T058 | A published release + brew | Steps 5 of `TAP-SETUP.md`; regression covered by `TestStablePathAcrossTwoCellarVersions` |
| T059 | — | Documented in `docs/install.md` §Package managers and the README |
| T067 | A public repository | `.github/workflows/release.yml`; tag `v0.1.0-rc.1` |
| T068 | Fresh macOS/Linux/Windows machines | `docs/validation-template.md` |
| T069 | T067 + T068 green | `docs/release-process.md` §Cutting a release |
| T070 | The manual runs above | Feature 001's DoD review is done and recorded in `docs/definition-of-done.md`, including the SC-00x performance numbers; what remains is confirming SC-001/SC-004/SC-006 on real machines |

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: repository hygiene and shared tooling every story relies on.

- [x] T001 Run `go mod tidy` so `go.mod` lists direct dependencies as direct (currently every module is `// indirect`); commit `go.mod`/`go.sum`; add a `tidy-check` target to `Makefile` (`go mod tidy && git diff --exit-code go.mod go.sum`)
- [x] T002 [P] Add Makefile targets in `Makefile`: `docs` (`go run ./tools/gendocs docs/cli`), `docs-check` (gendocs diff + lychee + markdownlint + placeholder grep), `lint-scripts` (`shellcheck -s sh install.sh` and `pwsh -NoProfile -Command "Invoke-ScriptAnalyzer -Path install.ps1 -Severity Warning -EnableExit"`), `install-test` (`go test ./test/install/... -count=1`)
- [x] T003 [P] Create `CHANGELOG.md` in Keep-a-Changelog format with an `## [Unreleased]` section and a `## [0.1.0] - YYYY-MM-DD` placeholder listing the prototype scope (feature 001) as the first entry
- [x] T004 [P] Add two jobs to `.github/workflows/ci.yml`: `shellcheck` (ubuntu, `shellcheck -s sh install.sh`) and `psscriptanalyzer` (windows, `Invoke-ScriptAnalyzer -Path install.ps1 -Severity Warning -EnableExit`); both gate merges (no separate workflow file — keeps the workflow count at `ci.yml` modified + `release.yml` + `install-test.yml` new)
- [x] T005 [P] Pin the distribution constants (repo slug `agentguard/agentguard`, `INSTALL_URL`, download base, latest URL) as defaults at the top of `install.sh` and `install.ps1` and in the README install section (identical strings, search-and-replace friendly); the environment overrides incl. `AGENTGUARD_LATEST_URL` are already specified in `contracts/installer.md` §"Environment / flags precedence" and `data-model.md` §2

**Checkpoint**: `make tidy-check lint-scripts` runnable (scripts may still be skeletons).

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: releases must exist for the one-liners to fetch, and upgrades must not break hooks/services. ⚠️ Blocks all user stories.

- [x] T006 Update `.goreleaser.yaml`: `release.draft: false`, `release.prerelease: auto`, `release.footer` linking `CHANGELOG.md#<version>`, `changelog.use: github` kept, keep six targets/archives/checksums; add `release.extra_files` for `dist/winget-manifest.zip` when present (used in Phase 8); validate with `goreleaser check`
- [x] T007 Add `.github/workflows/release.yml` per `contracts/release-artifacts.md` §"Tag → release" (verify-before-publish): trigger `push: tags: ['v[0-9]+.[0-9]+.[0-9]+*']`; jobs `build` (checkout `fetch-depth: 0`, setup-go 1.22, `make tidy-check lint-scripts docs-check test`, `goreleaser release --clean --skip=publish`, upload `dist/**` artifact) → `verify-installers` (matrix ubuntu/macos/windows: download `dist/`, `go run ./tools/releaseserve dist/ --tag ${{ github.ref_name }}` in the background, run `install.sh`/`install.ps1` with `AGENTGUARD_LATEST_URL`/`AGENTGUARD_DOWNLOAD_BASE` at the local server, assert version in a new shell, upgrade from a fake older version, `setup claude --dry-run` with a Claude shim, uninstall, install duration < 60 s; windows leg additionally runs `winget validate` on the generated manifest with `continue-on-error: true`) → `publish` (needs verify: `goreleaser release --clean` with `GITHUB_TOKEN`, `HOMEBREW_TAP_GITHUB_TOKEN`, `WINGET_GITHUB_TOKEN`) → `post-verify` (calls `install-test.yml` remote mode via `workflow_call` with `version: ${{ github.ref_name }}`; on failure `gh release edit <tag> --prerelease` and append a "verification failed" line to the notes, then fail); permissions `contents: write`; concurrency group per tag
- [x] T008 Stable executable path: change `internal/platform/self.go` `SelfExecutablePath()` per `contracts/release-artifacts.md` (prefer PATH entry that is `os.SameFile` with the resolved executable; else the unresolved `os.Executable()` when it is a symlink into `Cellar/`, `versions/`, or WinGet `Packages/`; else resolved path); tests `internal/platform/self_test.go` (temp PATH with symlink into a `Cellar/agentguard/1.2.3/bin/` fixture → stable symlink path chosen; direct install → same path; Windows junction case)
- [x] T009 [P] IPC `client_version`: add optional `client_version` to the request envelope in `internal/ipc/protocol.go`, send it from `internal/ipc/client.go` (from `internal/version`), ignore when absent (protocol stays v1); tests `internal/ipc/protocol_test.go`
- [x] T010 Daemon self-refresh: in `internal/daemon/router.go`/`daemon.go` compare `client_version` (semver) with own version; when the client is newer, complete the request, log `newer client detected; restarting`, then exit with code `75` after in-flight requests; unit test with a fake clock/version pair `internal/daemon/refresh_test.go`; document exit code in `internal/daemon/doc.go`
- [x] T011 [P] Service supervision for self-refresh: `internal/platform/service_linux.go` unit template `Restart=always` + `RestartSec=1`; verify launchd plist has `KeepAlive: true` (`internal/platform/service_darwin.go`); confirm hook lazy start path covers Windows (`internal/adapter/claude/lazystart.go`) — tests updated in `internal/platform/service_test.go`
- [x] T012 [P] `doctor` checks in `internal/cli/doctor.go`: (a) daemon version (from `ping`) ≠ CLI version → fix `agentguard daemon restart`; (b) hook command path in Claude settings and service definition path ≠ current stable path → fix `agentguard setup claude`; tests `internal/cli/doctor_test.go`
- [x] T013 Hermetic installer test harness `test/install/harness_test.go` plus a reusable static release server `tools/releaseserve/main.go` (serves a directory of archives + `checksums.txt` and answers `/releases/latest` with a 302 to `/releases/tag/<tag>`; used by the harness in-process via `httptest` and by the release workflow as a background process): the harness builds the binary once, packages fake release assets for the current OS/arch (`agentguard_<ver>_<os>_<arch>.tar.gz|zip` + `checksums.txt`), and provides helpers to run `sh install.sh` / `pwsh -NoProfile -File install.ps1` with `AGENTGUARD_LATEST_URL`, `AGENTGUARD_DOWNLOAD_BASE`, temp `HOME`/`USERPROFILE`, `AGENTGUARD_INSTALL_DIR`, and to inspect PATH blocks / user PATH (Windows via a temp registry-free mode: `-NoModifyPath` plus assertion of printed instruction, and a separate opt-in real-registry test behind `AGENTGUARD_INSTALL_REGISTRY_TESTS=1`)
- [x] T014 Update feature 001 contracts for the additive changes: `specs/001-agentguard-prototype/contracts/ipc-protocol.md` (optional `client_version` field), `specs/001-agentguard-prototype/PROTOTYPE_SPEC.md` §8.1 (`SelfExecutablePath` stable-path rule) and §9.4 (systemd `Restart=always`, daemon exit 75 self-refresh)

**Checkpoint**: tagging `v0.1.0-rc.1` on a fork/test repo produces a published pre-release with six assets + `checksums.txt`; `go test ./internal/platform ./internal/ipc ./internal/daemon` green on 3 OSes.

---

## Phase 3: User Story 1 — Install with one command on macOS or Linux (Priority: P1) 🎯 MVP

**Goal**: `curl -fsSL INSTALL_URL/install.sh | sh` installs the current release to `~/.local/bin`, verifies checksums, fixes PATH, restarts the daemon on upgrade, prints the next step.

**Independent Test**: `go test ./test/install -run 'TestInstallSh' ` on macOS/Linux (hermetic fake release) + manual run of the one-liner on a fresh machine once a release exists (`quickstart.md` §2).

- [x] T015 [US1] Rewrite argument/env handling in `install.sh` per `contracts/installer.md`: flags `--version`, `--prefix`, `--no-modify-path`, `--setup claude`, `--uninstall`, `--purge`, `--dry-run`, `--yes`, `--help`; env `AGENTGUARD_VERSION`, `AGENTGUARD_INSTALL_DIR`, `AGENTGUARD_NO_MODIFY_PATH`, `AGENTGUARD_REPO`, `AGENTGUARD_DOWNLOAD_BASE`, `AGENTGUARD_LATEST_URL`; flag > env > default; unknown flag → exit 1 with usage
- [x] T016 [US1] Replace API-based "latest" in `install.sh` with the redirect method (`curl -fsSLI -o /dev/null -w '%{url_effective}' "$AGENTGUARD_LATEST_URL"` → parse `/tag/vX.Y.Z`); accept `vX.Y.Z`/`X.Y.Z` pins; failure → exit 2 with the manual URL; keep and harden `detect_platform` (`uname -s`/`uname -m` → `darwin|linux` × `amd64|arm64` accepting `x86_64|amd64|arm64|aarch64`; anything else → exit 1 naming the build-from-source docs, no changes made)
- [x] T017 [US1] Download + verify + atomic install in `install.sh`: temp dir with `trap` cleanup on EXIT/INT/TERM, HTTPS only, `sha256sum -c`/`shasum -a 256 -c` on the exact archive line (exit 3 on mismatch, print `verified sha256 <hash>`), extract only `agentguard`, record previous version if present (`agentguard version` of the old binary), write `agentguard.tmp` then `mv -f` over the target, `chmod 0755`; `wget` fallback message when `curl` is missing; when `HTTPS_PROXY`/`https_proxy` is set and a download fails, the error names the proxy value
- [x] T018 [US1] PATH registration in `install.sh`: detect login shell(s) (`$SHELL`, existing rc files); write idempotent marker block `# >>> agentguard >>> … # <<< agentguard <<<` to `~/.zshrc` (+ `~/.zprofile` on macOS), `~/.bashrc` (+ `~/.bash_profile`/`~/.profile` if present), fish `~/.config/fish/conf.d/agentguard.fish`; skip when `--no-modify-path` or already on PATH; always print `export PATH="<dir>:$PATH"` hint for the current shell when it was missing
- [x] T019 [US1] Post-install steps in `install.sh`: if `agentguard daemon status` reports a registered/running daemon → `agentguard daemon restart` (failure → exit 6 with the manual command); `--setup claude` → run `agentguard setup claude` (propagate failure as exit 6); print summary lines exactly as in `contracts/installer.md` §"Behavior (install mode)" step 9; `--dry-run` prints the plan and changes nothing; same-version re-run prints `already installed` and exits 0
- [x] T020 [P] [US1] ShellCheck cleanliness for `install.sh` (`shellcheck -s sh`, POSIX only: no bashisms, no arrays, no `[[ ]]`); verify under `dash`, `bash --posix`, `zsh --emulate sh`, and BusyBox `sh` (Alpine container in CI)
- [x] T021 [US1] Hermetic tests `test/install/install_sh_test.go` (uses T013 harness; skipped on Windows): fresh install → binary present, `verified sha256` printed, PATH block written to a temp rc file, `Next step` printed; upgrade (older fake version installed first) → `upgraded from` printed, binary replaced, daemon restart invoked (fake `agentguard` daemon in unmanaged mode with temp DataDir); pinned version; checksum mismatch → exit 3 and no binary; unsupported arch (spoofed `uname`) → exit 1; interrupted download (server closes mid-stream) → no temp files left; unreachable proxy (`https_proxy=http://127.0.0.1:9`) → exit 2 and the message names the proxy; `--dry-run` → no changes; `--setup claude` → invokes setup (Claude shim on PATH)
- [x] T022 [US1] Document the macOS/Linux one-liners in `docs/install.md` §"macOS and Linux" (install, pin, upgrade, PATH note per shell, proxies, WSL note, unsupported platforms → build from source) — content only; the full docs pass is Phase 6

**Checkpoint**: MVP — the macOS/Linux one-liner works against the fake release in tests and against a real pre-release manually.

---

## Phase 4: User Story 2 — Install with one command on Windows (Priority: P1)

**Goal**: `irm INSTALL_URL/install.ps1 | iex` installs to `%LOCALAPPDATA%\AgentGuard\bin`, verifies checksums, updates the user PATH, unblocks the binary, restarts the daemon on upgrade, prints the next step; works in Windows PowerShell 5.1 and PowerShell 7.

**Independent Test**: `go test ./test/install -run 'TestInstallPs1'` on Windows (hermetic) + manual one-liner on a fresh Windows machine (`quickstart.md` §2).

- [x] T023 [US2] Create `install.ps1` skeleton with `#requires -Version 5.1`, `[CmdletBinding()]` params `-Version`, `-InstallDir`, `-NoModifyPath`, `-Setup`, `-Uninstall`, `-Purge`, `-DryRun`, `-Help`, env equivalents (`$env:AGENTGUARD_*`), `Set-StrictMode -Version 2`, `$ErrorActionPreference='Stop'`, TLS 1.2 enablement when missing from `[Net.ServicePointManager]::SecurityProtocol`, exit-code mapping per `contracts/installer.md` (use `exit <n>` at top level so `iex` callers see it via `$LASTEXITCODE`)
- [x] T024 [US2] Platform detection and version resolution in `install.ps1`: arch from `[System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture` (fallback `$env:PROCESSOR_ARCHITECTURE`) → `amd64|arm64`, else exit 1; latest via `Invoke-WebRequest -Uri $LatestUrl -MaximumRedirection 0 -ErrorAction SilentlyContinue` reading the `Location` header (works on 5.1 and 7), pin accepts `v`-prefixed or plain
- [x] T025 [US2] Download + verify + install in `install.ps1`: temp dir under `[IO.Path]::GetTempPath()`, `try/finally` cleanup, download zip + `checksums.txt`, `Get-FileHash -Algorithm SHA256` compare (exit 3 on mismatch), `Expand-Archive` to temp, copy `agentguard.exe` atomically (`Move-Item -Force` to `agentguard.exe.new` then rename over), `Unblock-File`, record previous version, same-version no-op
- [x] T026 [US2] PATH and post-install in `install.ps1`: add `<InstallDir>` to user `Path` via `[Environment]::SetEnvironmentVariable('Path', $new, 'User')` without duplicates, broadcast `WM_SETTINGCHANGE` (P/Invoke `SendMessageTimeout` via `Add-Type`), update `$env:Path` for the session; `-NoModifyPath` prints instruction; daemon restart when registered; `-Setup claude` runs setup; summary lines identical to POSIX; `-DryRun`
- [x] T027 [P] [US2] (done in 005 T039) PSScriptAnalyzer cleanliness (`Invoke-ScriptAnalyzer -Severity Warning`) and compatibility check under both `powershell.exe` 5.1 and `pwsh` 7 in CI (`windows-latest` has both)
- [x] T028 [US2] Hermetic tests `test/install/install_ps1_test.go` (Windows only; runs both `powershell.exe -NoProfile -File` and `pwsh -NoProfile -File`): fresh install, upgrade with daemon restart, pin, checksum mismatch, unsupported arch (spoofed env), `-DryRun`, `-NoModifyPath` instruction, `Unblock-File` applied (no `Zone.Identifier` stream), unreachable proxy → exit 2 naming the proxy, user-PATH update in the opt-in registry test (FR-004 on Windows is proven end-to-end by the new-shell check in T049)
- [x] T029 [US2] Document the Windows one-liners in `docs/install.md` §"Windows" (install via `irm | iex`, parameterized scriptblock form, PowerShell 5.1 vs 7, PATH/new-terminal note, SmartScreen/mark-of-the-web note, arm64)

**Checkpoint**: Both P1 one-liners pass hermetic tests on their OSes; manual runs succeed against a real pre-release.

---

## Phase 5: User Story 3 — Remove or pin with the same mechanism (Priority: P2)

**Goal**: `--uninstall`/`-Uninstall` removes hooks + service (via `agentguard uninstall claude`), the binary, and installer-made PATH changes; `--purge` removes data; version pinning is documented and tested end-to-end.

**Independent Test**: hermetic uninstall/purge/pin tests on all OSes; manual `quickstart.md` §3.

- [x] T030 [US3] Uninstall mode in `install.sh`: `--uninstall [--purge]` → run `agentguard uninstall claude [--purge]` if the binary exists (warn and continue on failure → exit 5 at the end), remove the binary, remove exactly the marker blocks the installer wrote from every rc file (leave other content byte-identical), print what was removed and where data was kept; nothing installed → `nothing to remove`, exit 0
- [x] T031 [US3] Uninstall mode in `install.ps1`: `-Uninstall [-Purge]` mirroring T030; remove only the installer's user-PATH entry; broadcast `WM_SETTINGCHANGE`
- [x] T032 [P] [US3] Hermetic uninstall/purge tests added to `test/install/install_sh_test.go` and `install_ps1_test.go`: uninstall after install removes binary + PATH block/entry, keeps DataDir; `--purge` removes DataDir; uninstall when nothing installed exits 0; rc files with unrelated content are byte-identical after install→uninstall
- [x] T033 [P] [US3] Pin/upgrade matrix test in `test/install/pin_test.go`: install pinned `0.1.0` (fake) → latest → assert `upgraded from 0.1.0`; downgrade attempt with `--version` prints `downgrading from` and proceeds; invalid version → exit 2
- [x] T034 [US3] Document uninstall/purge/pin in `docs/install.md` §"Upgrade, pin, uninstall" and add the uninstall one-liners to `README.md` §"Uninstall" (final README rewrite happens in T035; keep this section aligned)

**Checkpoint**: full lifecycle install → upgrade → pin → uninstall covered by tests on 3 OSes.

---

## Phase 6: User Story 4 — A newcomer goes from zero to a first decision using only the docs (Priority: P2)

**Goal**: README + `docs/` let a newcomer install, set up Claude, see an allow and a block, inspect history, and troubleshoot; the CLI reference is generated from the binary and verified in CI.

**Independent Test**: `make docs-check` green; `go test ./test/e2e -run TestDocsSmoke`; a timed manual walkthrough < 10 min recorded in `docs/validation-<date>.md`.

- [x] T035 [US4] Rewrite `README.md` per `contracts/docs-and-checks.md` §"Required README sections" (one-liners for both platforms, brew/winget one-liners with availability note, setup, 5-step demo, CLI table linking `docs/cli/`, how-it-works/security summary, uninstall, docs index, contributing, license); remove every `TODO(` marker
- [x] T036 [P] [US4] Write `docs/getting-started.md`: prerequisites, install (link), `agentguard setup claude` output walkthrough, create the demo project, first prompt → "Yes, and don't ask again", second session auto-allow, change `package.json` → block with explanation, `agentguard history show <id>`, `agentguard approvals`, uninstall; every runnable block fenced as ```console; hermetically runnable blocks carry `<!-- smoke -->` + `<!-- expect: "…" -->` markers — namely: `agentguard version`, `agentguard setup claude --dry-run`, the demo-project creation, `agentguard approve <event>` (event created by piping a hook payload in the smoke harness), `agentguard approvals`, `agentguard history --limit 3`, `agentguard history show <id>`, `agentguard uninstall claude --dry-run`; the interactive Claude steps (real prompt, "don't ask again") are marked `<!-- manual -->`
- [x] T037 [P] [US4] Write `docs/how-it-works.md`: pipeline (parse → resolve scripts/wrappers → normalize targets/scopes → hard rules → approvals), EXACT vs SEMANTIC approvals, fingerprints & invalidation, decision classes and what the user sees (deferred native prompt vs forced prompt vs deny), consent import from Claude's "don't ask again"; link to `specs/001-agentguard-prototype/PROTOTYPE_SPEC.md` for depth
- [x] T038 [P] [US4] Write `docs/security-model.md` from `PROTOTYPE_SPEC.md` §27 + Appendix B resolutions: what is protected, what is not (Write/Edit tools, sandboxing), fail-safe behaviors, local-only/no telemetry, threat model summary
- [x] T039 [P] [US4] Write `docs/configuration.md`: `config.toml` keys with defaults (from `internal/config`), env overrides (`AGENTGUARD_*`), file locations per OS (DataDir/ConfigDir/RuntimeDir), log locations
- [x] T040 [P] [US4] Write `docs/troubleshooting.md`: symptom → `agentguard doctor` check → fix for: daemon not running / unmanaged mode, hooks not active (restart Claude), PATH not updated (new terminal / export line), unsupported platform, permission modes (bypass, plan, `-p`), daemon version ≠ CLI version after upgrade, hook path drift after Homebrew upgrade, Windows PS 5.1/TLS/SmartScreen, corporate proxy, "why was this asked/blocked" (`history show`)
- [x] T041 [P] [US4] Write `docs/faq.md`: what happens on the first prompt, why some commands defer, bypass mode behavior, privacy/local-only, performance, uninstall/reinstall keeps approvals, how to reset an approval
- [x] T042 [US4] Create `tools/gendocs/main.go` (uses `github.com/spf13/cobra/doc` on the root command from `internal/cli`; disables auto-generated timestamps; writes `docs/cli/*.md` with a header "generated — do not edit"); add `tools/gendocs` to the depguard allow-list in `.golangci.yml`; run it and commit `docs/cli/`
- [x] T043 [US4] Docs CI job in `.github/workflows/ci.yml` (`docs` job on ubuntu): `go run ./tools/gendocs docs/cli && git diff --exit-code -- docs/cli`; `lycheeverse/lychee-action` (offline for local links, online with retries for external, accept 200/429); `markdownlint-cli2` with `.markdownlint-cli2.yaml` (MD013 off, MD033 allow); `! grep -RIn "TODO(" README.md docs`
- [x] T044 [US4] Docs smoke e2e `test/e2e/docs_smoke_test.go`: parse `docs/getting-started.md`, extract ```console blocks preceded by `<!-- smoke -->`, run each with the built binary, a `claude` shim and temp HOME/DataDir; assert exit codes and expected substrings noted in HTML comments (`<!-- expect: "APPROVAL_MATCH" -->`)
- [x] T045 [US4] Cross-link pass: every doc page links back to README and forward to the next step; `docs/install.md` sections from T022/T029/T034 merged into one coherent page (all channels, air-gapped/manual verification instructions with `sha256sum -c` / `Get-FileHash`, corporate proxy/custom-CA section with `HTTPS_PROXY` examples and how the error looks, unsupported platforms)
- [x] T046 [US4] (done in 005 T054) `agentguard --help`/subcommand `Long` texts reviewed for accuracy and examples (`internal/cli/*.go`), since they feed the generated reference; regenerate `docs/cli/`
- [ ] T047 [US4] Newcomer walkthrough dry-run by a second person on one OS; fix friction; record time and notes in `docs/validation-<date>.md` (SC-006)

**Checkpoint**: docs job green; README has zero placeholders; walkthrough < 10 min.

---

## Phase 7: User Story 5 — Maintainer publishes a release that the one-liners pick up (Priority: P2)

**Goal**: tag → published release (Phase 2) plus automated verification of the exact one-liners on fresh 3-OS runners, before merge (local script mode) and after each release/nightly (remote mode).

**Independent Test**: `install-test.yml` green in PR mode; after tagging a pre-release, the `release: published` run passes on ubuntu/macos/windows (`quickstart.md` §1).

- [x] T048 [US5] Create `.github/workflows/install-test.yml` per `contracts/docs-and-checks.md`: triggers (`pull_request`/`push` on installer paths, `release: published`, nightly `schedule`, `workflow_dispatch` with `version` input); matrix ubuntu/macos/windows; step "fresh assertion" (`agentguard` absent)
- [x] T049 [US5] Local-script mode job steps: run `sh install.sh` / `pwsh -File install.ps1` against `${{ inputs.version || 'latest' }}` (real GitHub release); guard: if `gh release list --exclude-drafts --exclude-pre-releases` is empty and no `version` input is given, run the hermetic `make install-test` instead and mark the job "neutral (no release yet)"; then new-shell checks (`bash -lc`, `zsh -lc` on macOS, `pwsh -NoProfile` reloading user PATH) asserting `agentguard version` = expected; install duration measured (`date +%s` / `Measure-Command`) and asserted < 60 s (hard fail on PR/release triggers, warning on nightly); upgrade path from a pinned older release; `agentguard setup claude --dry-run` with a Claude shim; uninstall + purge; upload logs on failure
- [x] T050 [US5] Remote mode job steps (release/nightly, and `workflow_call` from `release.yml` post-verify with `version`): execute the exact documented one-liners copied verbatim from `README.md` (a small script extracts them so docs and CI cannot drift), pinned to `version` when provided, same assertions and 60-second timing as T049
- [x] T051 [P] [US5] Wire release notes: `.goreleaser.yaml` `release.footer` and `changelog` header include the `CHANGELOG.md` section for the tag (script `scripts/changelog-section.sh <version>` used by the release workflow to fail if the section is missing)
- [x] T052 [P] [US5] Release workflow gates in `.github/workflows/release.yml`: run `make lint-scripts`, `make docs-check`, `make tidy-check`, and unit tests before `goreleaser release`; on failure no release is created
- [x] T053 [US5] Post-verify job in `release.yml` (see T007): call `install-test.yml` remote mode via `workflow_call` with `version: <tag>`; on failure run `gh release edit <tag> --prerelease` (so "latest" rolls back to the previous stable release), append a "verification failed — do not use" line to the release notes with `gh release edit --notes-file`, and fail the workflow; make `install-test.yml` expose `on: workflow_call` with the `version` input; document the automatic demotion and the manual re-promotion (`gh release edit <tag> --prerelease=false --latest`) in `docs/release-process.md`

**Checkpoint**: a pre-release tag end-to-end: published, tap updated (Phase 8), one-liners verified on 3 OSes automatically.

---

## Phase 8: User Story 6 — Preferred package managers also work with one line (Priority: P3)

**Goal**: `brew install agentguard/tap/agentguard` works on macOS/Linux; winget manifest generated/validated/submitted per release; upgrades through package managers keep hooks/services valid.

**Independent Test**: `quickstart.md` §4–§5; `internal/platform/self_test.go` Cellar case (T008) green.

- [x] T054 [US6] Add `brews:` to `.goreleaser.yaml`: repository `agentguard/homebrew-tap` (token `HOMEBREW_TAP_GITHUB_TOKEN`), formula `agentguard`, `directory: Formula`, `homepage`, `description`, `license`, `install: bin.install "agentguard"`, `test: system "#{bin}/agentguard", "version"`, `caveats` pointing to `agentguard setup claude`; mark `packaging/homebrew/agentguard.rb.tmpl` and `packaging/winget/*.tmpl` reference-only with a header comment ("generated by GoReleaser at release time; this file documents the expected shape")
- [ ] T055 [US6] Bootstrap the tap repository `agentguard/homebrew-tap` (README, empty `Formula/`, branch protection off for the bot); document the required secret in `docs/release-process.md`; dry-run with `goreleaser release --snapshot --skip=publish` to inspect the generated formula in `dist/`
- [x] T056 [P] [US6] Add `winget:` to `.goreleaser.yaml`: publisher `AgentGuard`, `package_identifier: AgentGuard.AgentGuard`, `short_description`, `license`, `publisher_url`, `repository` (fork of `microsoft/winget-pkgs`, token `WINGET_GITHUB_TOKEN`, `skip_upload: auto`), installer type portable with `commands: [agentguard]`; also zip the generated manifests into `dist/winget-manifest.zip` via a `post` hook so `release.extra_files` (T006) attaches them; validation runs in the `verify-installers` windows leg of `release.yml` (T007, `winget validate --manifest <dir>`, `continue-on-error: true` until the package is accepted upstream)
- [x] T057 [P] [US6] Homebrew upgrade coherence regression: `internal/platform/self_test.go` extended with a two-version Cellar fixture (`Cellar/agentguard/0.1.0` → symlink `bin/agentguard`; upgrade to `0.2.0`, relink) asserting the stable path returned before/after is the symlink; plus an e2e note in `test/e2e/s13_hook_binary_test.go` that hooks written by setup use the stable path
- [ ] T058 [US6] Verify on macOS and Linux: `brew install agentguard/tap/agentguard` (or `brew install --build-from-source ./dist/.../agentguard.rb` before the tap is public), `agentguard setup claude`, then `brew upgrade` to a newer pre-release → `agentguard doctor` clean or shows only "daemon restart" fix; record in `docs/validation-<date>.md`
- [ ] T059 [US6] Document package-manager channels in `docs/install.md` §"Package managers" and README (brew one-liner; winget one-liner marked "after upstream review", plus `winget install --manifest` from the release zip as advanced fallback)

**Checkpoint**: brew channel verified; winget manifest generated on every release.

---

## Phase 9: User Story 7 — Contributors can build, test and release from the docs (Priority: P3)

**Goal**: contributor docs and release procedure.

**Independent Test**: fresh clone + `CONTRIBUTING.md` → `make build test lint e2e docs-check` green; release procedure walkthrough matches the workflows.

- [x] T060 [US7] Write `CONTRIBUTING.md`: prerequisites (Go 1.22, golangci-lint, shellcheck, pwsh, lychee, markdownlint), `make` targets, running e2e/install tests, repository layout (from `plan.md` of feature 001), where specs live (`specs/`), PR expectations (tests, docs regen, changelog entry), coding conventions pointer
- [x] T061 [P] [US7] Write `docs/release-process.md`: versioning/tags (research R-14), pre-flight checklist (`make tidy-check docs-check lint-scripts test`, CHANGELOG section, docs regenerated), tagging, what the release workflow does (assets, checksums, tap, winget), post-release install-test verification, how to yank/mark pre-release, secrets required (`HOMEBREW_TAP_GITHUB_TOKEN`, `WINGET_GITHUB_TOKEN`)
- [x] T062 [P] [US7] Add `docs/README.md` (documentation index) and ensure `README.md` §"Documentation" links every page including generated `docs/cli/agentguard.md`
- [x] T063 [US7] Update `.golangci.yml` depguard notes and `Makefile` help target (`make help` lists targets with one-line descriptions) so contributor docs stay accurate; add `make help` output to `CONTRIBUTING.md`

---

## Phase 10: Polish & Release

- [x] T064 Update `specs/001-agentguard-prototype/tasks.md` progress: mark T075/T076 as superseded (already annotated) and add a pointer to this feature's tasks; keep T077–T080 there
- [x] T065 [P] Security pass on installer scripts: no `eval` of remote content beyond the documented `iex`/`sh` entry, quoted variables everywhere, `set -eu`, no world-writable temp, HTTPS-only URLs, no credentials logged; document in `docs/security-model.md` §"Installer"
- [x] T066 [P] Accessibility of messages: all installer output lines are plain ASCII except the summary check marks; `--help`/`-Help` text reviewed; error messages include the manual fallback URL
- [ ] T067 Cut `v0.1.0-rc.1`: run the release workflow on the real repository, watch `install-test.yml` remote mode on 3 OSes, fix and re-tag as needed
- [ ] T068 Run `quickstart.md` §2–§6 manually on fresh macOS, Linux (incl. one Alpine/WSL2 run) and Windows machines; record in `docs/validation-<date>.md`
- [ ] T069 Cut `v0.1.0` (final), verify README one-liners install it, verify `brew install agentguard/tap/agentguard`, submit/verify winget manifest; update `CHANGELOG.md`
- [ ] T070 Final review against `spec.md` SC-001…SC-008 and feature 001 DoD (`PROTOTYPE_SPEC.md` §30 items 7–8); file follow-ups (vanity domain, Scoop, signing) as new features

---

## Dependencies & Execution Order

- **Phase 1 → Phase 2** sequential; Phase 2 blocks all stories (releases + upgrade coherence + test harness).
- **Phase 3 (US1)** and **Phase 4 (US2)** are independent of each other after Phase 2 (different scripts) and can run in parallel; both are P1 and together form the MVP.
- **Phase 5 (US3)** edits both scripts → after Phases 3 and 4.
- **Phase 6 (US4)** can start after Phase 2 (docs content) but T042–T046 depend on the final CLI (feature 001) only; T022/T029/T034 feed T045.
- **Phase 7 (US5)** local-mode tests need Phases 3–5; remote mode needs a public repo with a published pre-release.
- **Phase 8 (US6)** needs Phase 2 (T006/T008) and Phase 7 secrets/repo; **Phase 9 (US7)** can run in parallel with Phase 8.
- **Phase 10** last.

### Parallel opportunities

- Setup: T002–T005 in parallel.
- Foundational: T009/T011/T012 in parallel with T008; T013 in parallel with T006/T007.
- US1 ∥ US2 (different scripts, different test files).
- Docs pages T036–T041 in parallel; T042/T043/T044 in parallel after content exists.
- US6 T054/T056/T057 in parallel; US7 T060–T062 in parallel.

### Parallel example (after Phase 2)

```text
Dev A: T015→T019 (install.sh) → T021 tests → T030 (uninstall) 
Dev B: T023→T026 (install.ps1) → T028 tests → T031 (uninstall)
Dev C: T036–T041 docs pages → T042 gendocs → T043 docs CI → T044 smoke
Dev D: T048–T050 install-test workflow → T054–T056 brew/winget
```

## Implementation Strategy

1. **MVP** = Phases 1–4: both P1 one-liners working against a pre-release with hermetic tests; then Phase 5 (uninstall/pin) completes the lifecycle promise.
2. **Docs in parallel** (Phase 6) — the README rewrite lands with the one-liners so a first public pre-release is already self-explanatory.
3. **Automate before announcing** (Phase 7) — no "latest" is announced until `install-test.yml` remote mode is green on all three OSes.
4. **Package managers last** (Phase 8) — nice-to-have channels; the upgrade-coherence fix (Phase 2) must precede any Homebrew publication.

## Notes

- Behavior questions: `contracts/*.md` first, then `research.md`; conflicts → update the contract before coding around it.
- Never test the real installers against the user's real HOME in automated tests; the hermetic harness uses temp dirs and env overrides.
- Commit after each task; reference the task id (e.g. `T017`) in commit messages.
