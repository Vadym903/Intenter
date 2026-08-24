# Feature Specification: One-Line Installation & User Documentation

**Feature Branch**: `002-one-line-install-docs`

**Created**: 2026-08-16

**Status**: Draft

**Input**: User description: "we've done with the implementation for the previous tasks, now we have to focus on documentation and convenient way to install our tool for each system, for all systems it should be one line command for user, they should not download it themselves, it should be only one line command, it may be an sh script or so on"

**Context**: The AgentGuard prototype (feature `001-agentguard-prototype`, Phases 1–8) is implemented: one binary, daemon, Claude Code hooks, approvals, CLI. Today a user must build from source or manually download a release archive; the README contains TODO placeholders. This feature delivers (a) a copy-paste one-line install command per operating system that fetches, verifies and installs the current release without any manual download, upgrade and removal via the same mechanism, and the release automation that makes those commands work; and (b) complete end-user and contributor documentation. It absorbs the still-open Phase 9 tasks T075 (README) and T076 (install script / packaging) of feature 001.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Install with one command on macOS or Linux (Priority: P1)

A developer on macOS or Linux copies a single command from the README into a terminal. Without visiting a downloads page, choosing an architecture, unpacking anything or editing configuration, the correct build for their machine is fetched over a secure connection, its integrity is verified, the `agentguard` executable is placed in a stable per-user location and made available on their PATH, the installed version is printed, and the next step (`agentguard setup claude`) is shown. Re-running the same command later upgrades to the newest release.

**Why this priority**: The core ask — the whole product depends on frictionless install (brief §5), and macOS/Linux developers are the primary audience.

**Independent Test**: On a fresh macOS or Linux machine (or CI runner) with no AgentGuard present, run the one-line command; open a new terminal; `agentguard version` prints the released version; `agentguard setup claude` is offered as the next step.

**Acceptance Scenarios**:

1. **Given** a fresh macOS (Apple Silicon or Intel) or Linux (x86_64 or arm64) machine with a published release available, **When** the user runs the single documented command, **Then** within one minute on a typical broadband connection the current release is installed for the correct OS/architecture, its checksum is verified before installation, `agentguard version` works in a new terminal, and the output ends with the exact next command to run.
2. **Given** an older AgentGuard is already installed by the same mechanism (daemon running, hooks installed), **When** the user re-runs the same command, **Then** the binary is replaced in place at the same path (hooks and service entries keep working), the running daemon is restarted on the new version, existing approvals/history are preserved, and the output states the old and new versions.
3. **Given** the download or checksum verification fails (network error, tampered file, unsupported architecture), **When** the command runs, **Then** nothing is installed or modified, and a clear message names the cause and the manual fallback.
4. **Given** the install location is not on the user's PATH, **When** installation completes, **Then** the installer either registers the location for future shells or prints the exact line to add, and tells the user to open a new terminal.

---

### User Story 2 - Install with one command on Windows (Priority: P1)

A developer on Windows 10/11 pastes a single command into PowerShell (the default Windows PowerShell 5.1 or PowerShell 7). Without administrator rights or manual downloads, the correct build (x64 or arm64) is fetched securely, verified, installed to a stable per-user location, added to the user's PATH, unblocked so it runs without security prompts, and the next step is printed.

**Why this priority**: Cross-platform parity from day one is a hard product requirement (brief §3, §5); Windows users must not be second-class.

**Independent Test**: On a fresh Windows runner, run the single documented PowerShell command; open a new PowerShell window; `agentguard version` prints the released version.

**Acceptance Scenarios**:

1. **Given** a fresh Windows 10/11 machine with the default PowerShell, **When** the user runs the single documented command, **Then** the current release is installed for the correct architecture without elevation, checksum-verified, available as `agentguard` in a new terminal, and the next step is printed.
2. **Given** the user later re-runs the command, **Then** the installation is upgraded in place with approvals/history preserved and the daemon restarted.
3. **Given** the machine's shell blocks downloaded files or uses only the older security-protocol defaults, **When** the command runs, **Then** the installer still completes (it handles these cases itself) or explains precisely what to change.

---

### User Story 3 - Remove or pin with the same mechanism (Priority: P2)

A user can uninstall AgentGuard completely (Claude hooks, daemon service, binary; optionally data) with one documented command, and can install a specific version instead of the latest.

**Why this priority**: Trust and reversibility; also needed for support ("please try version X").

**Independent Test**: After an install, run the documented uninstall; verify the binary, hooks and service are gone and Claude Code still works; run the documented pinned-version install and confirm the exact version.

**Acceptance Scenarios**:

1. **Given** AgentGuard is installed and integrated with Claude Code, **When** the user runs the documented uninstall command, **Then** the Claude hooks and the background service are removed (via the existing `uninstall claude` behavior), the binary is deleted, PATH changes made by the installer are reverted, local data is kept unless the user asks to purge it, and a summary is printed.
2. **Given** the user requests a specific version, **When** the install command runs with that version, **Then** exactly that version is installed and verified.

---

### User Story 4 - A newcomer goes from zero to a first decision using only the docs (Priority: P2)

A developer who has never seen AgentGuard reads the README/docs and, following them, installs the tool, sets up Claude Code, watches a command get auto-allowed after approval, sees a changed script get blocked with an explanation, and knows how to inspect approvals/history, uninstall, and troubleshoot — without asking anyone.

**Why this priority**: The install one-liner is only useful if users understand what happens next; documentation is the second half of this feature.

**Independent Test**: A person unfamiliar with the project follows the docs on a fresh machine and completes the demo in under 10 minutes; every documented command exists and behaves as described.

**Acceptance Scenarios**:

1. **Given** the published documentation, **When** a new user follows "Install → Set up Claude → First run", **Then** they reach a first auto-allowed and a first blocked decision within 10 minutes and every command they typed came verbatim from the docs.
2. **Given** any CLI command or flag that the binary supports, **When** the user looks it up in the docs, **Then** it is documented with purpose, syntax and an example, and the documentation matches the binary's actual behavior (verified automatically).
3. **Given** a common problem (daemon not running, hooks not active, PATH not updated, unsupported platform, permission mode confusion), **When** the user opens the troubleshooting page, **Then** it lists the symptom, the check (`agentguard doctor` output) and the fix.

---

### User Story 5 - Maintainer publishes a release that the one-liners pick up (Priority: P2)

A maintainer tags a version; a published release with per-OS/arch archives and a checksums file appears automatically; the one-line installers on all three operating systems install that version, and this is verified in automation before users see it.

**Why this priority**: The one-liners are only as good as the release they fetch; without automated, verified publishing they silently break.

**Independent Test**: Tag a pre-release; observe the release appear with all six archives and checksums; the automated install test on macOS/Linux/Windows runners installs it via the one-liners and reports the tagged version.

**Acceptance Scenarios**:

1. **Given** a version tag is pushed, **When** automation runs, **Then** a public release with all six archives, checksums, and release notes is published without manual steps.
2. **Given** a published release, **When** the automated end-to-end install test runs on fresh macOS, Linux and Windows machines using the exact documented one-line commands, **Then** all three succeed and report that version.
3. **Given** the installer scripts themselves change, **When** the change is proposed, **Then** automation exercises them (against the latest release) before merge.

---

### User Story 6 - Preferred package managers also work with one line (Priority: P3)

Users who prefer package managers can install via a single Homebrew command (macOS/Linux) and, once accepted upstream, a single winget command (Windows); the manifests are produced and kept current automatically by the release process.

**Why this priority**: Nice-to-have channels; the scripts already satisfy the one-line requirement, and winget availability depends on an external review process.

**Independent Test**: `brew install <tap>/agentguard` installs the current version on macOS/Linux; the winget manifest validates and is submitted for each release.

**Acceptance Scenarios**:

1. **Given** a published release, **When** a user runs the documented single Homebrew command, **Then** the current version installs and `agentguard version` matches.
2. **Given** a published release, **When** automation runs, **Then** an updated winget manifest is generated, validated, and submitted (or a submission-ready artifact is attached to the release if automatic submission is not yet enabled).

---

### User Story 7 - Contributors can build, test and release from the docs (Priority: P3)

A contributor finds how to build, run tests, run the end-to-end scenarios, cut a release, and where the specification/architecture lives.

**Why this priority**: Sustains the project; low user-facing impact.

**Independent Test**: A contributor follows the contributing docs on a fresh clone and gets a green local build/test run and understands the release procedure.

**Acceptance Scenarios**:

1. **Given** a fresh clone, **When** a contributor follows the contributing guide, **Then** build, lint and tests run successfully with the listed commands, and the release procedure is described step by step.

---

### Edge Cases

- No `curl`/`wget` (minimal Linux images), or PowerShell with only legacy security-protocol defaults: the installer detects and explains, or works around it where possible.
- Unsupported OS/architecture (32-bit, ARMv7, FreeBSD): clear message, no partial install, pointer to build-from-source.
- Behind a corporate proxy or with a custom CA: standard proxy environment variables are honored; failures explain the proxy/CA cause.
- Air-gapped machines: documented manual install path (download archive elsewhere, verify checksum, place binary).
- Version resolution must not depend on rate-limited public endpoints (shared CI IPs, offices) — the "latest" lookup has to work under heavy shared use.
- Existing installation from a different channel (e.g., Homebrew and script both present): the installer detects duplicates on PATH and warns which one wins.
- Upgrade while the daemon is running and while Claude Code sessions are active: binary replaced atomically, daemon restarted, hooks continue to point at a stable path; active sessions keep working (they invoke the same path).
- The install directory is a symlink or read-only, or the user's home is on a network share: clear error and alternative location flag.
- Running the installer as root/administrator: warn and still install for the invoking user (or honor an explicit system-wide prefix).
- Interrupted download (Ctrl-C mid-way): no partial binary left behind (temp files cleaned).
- Shells: bash, zsh, fish (macOS/Linux) and both Windows PowerShell 5.1 and PowerShell 7 — PATH registration works for each.
- Downloaded executable "mark of the web" on Windows: the installer removes it so the binary and the daemon start without security prompts.
- Windows Subsystem for Linux: the Linux one-liner works inside WSL; the docs say which installer to use for which environment.
- Docs drift: a command renamed or a flag added in the binary without a docs update must be caught before release.

## Requirements *(mandatory)*

### Functional Requirements

**One-line install**

- **FR-001**: The project MUST publish exactly one copy-paste command for macOS/Linux shells and exactly one for Windows PowerShell that installs the current release without any manual download, extraction, architecture choice, or configuration editing, and without administrator/root rights.
- **FR-002**: The installer MUST detect the operating system and CPU architecture and fetch the matching build; unsupported combinations MUST fail with a clear message and no changes to the machine.
- **FR-003**: The installer MUST fetch only over secure connections and MUST verify the downloaded archive against the release's published checksums before installing; on any mismatch or download error it MUST install nothing.
- **FR-004**: The installer MUST place the executable at a stable per-user location that does not change across upgrades, MUST make `agentguard` available on PATH for new terminals (registering the location or printing the exact instruction), and MUST print the installed version and the next step (`agentguard setup claude`).
- **FR-005**: Re-running the install command MUST upgrade in place, preserve local data (approvals, history, configuration) and integration (hooks, service), restart the running daemon on the new version, and report old→new versions.
- **FR-006**: The installer MUST support installing a specific version and MUST default to the latest published release; "latest" resolution MUST work under heavy shared use (no dependency on rate-limited endpoints).
- **FR-007**: The installer MUST provide a documented uninstall path that removes the Claude integration and service (using the tool's own uninstall behavior), the executable, and any PATH changes it made; local data MUST be kept unless purge is explicitly requested.
- **FR-008**: The installer MUST be safe to run non-interactively (piped input), MUST clean up temporary files on failure or interruption, MUST honor standard proxy settings, and MUST allow an alternative install location.
- **FR-009**: The Windows installer MUST run under both Windows PowerShell 5.1 and PowerShell 7, MUST enable modern secure-protocol defaults if the shell lacks them, and MUST unblock the downloaded executable so it runs without security prompts.
- **FR-010**: The installer MAY optionally run `agentguard setup claude` when the user explicitly requests it in the same command; by default it installs only and prints the next step.

**Release publishing (prerequisite for the one-liners)**

- **FR-011**: Pushing a version tag MUST publish a public release containing archives for all six OS/architecture targets, a checksums file, and release notes, without manual steps.
- **FR-012**: The published installer commands MUST be exercised automatically against the latest release on fresh macOS, Linux and Windows machines, both when installer scripts change and after each release; failures MUST block the release from being announced as current.
- **FR-013**: The Homebrew formula MUST be regenerated and published to the project's tap on every release so that a single Homebrew command installs the current version; the winget manifest MUST be regenerated and validated on every release and submitted (or attached ready-to-submit).

**Documentation**

- **FR-014**: The README MUST contain, without placeholders: what AgentGuard is and the core idea, the one-line install commands per OS, setup, the demo, the CLI overview, links to the detailed docs, limitations, and how to uninstall.
- **FR-015**: End-user documentation MUST cover: installation (all channels, upgrade, pin, uninstall, manual/air-gapped path), getting started (setup → first decisions), how decisions are made (allow/ask/block, approvals, invalidation, explanations), the security model and limitations, complete CLI reference, configuration options, troubleshooting, and FAQ.
- **FR-016**: The CLI reference MUST match the binary: every command, sub-command and flag exposed by the tool MUST be documented and automation MUST fail when they diverge.
- **FR-017**: Contributor documentation MUST cover building, testing (unit, race, end-to-end), linting, the release procedure, repository layout, and where the specifications live.
- **FR-018**: All documentation links MUST resolve (checked automatically) and every command shown in the docs MUST be exact and copy-pasteable (complements FR-016, which covers the generated CLI reference; this requirement covers prose pages and the README).
- **FR-019**: A changelog MUST list user-visible changes per release and MUST be linked from the release notes.

### Key Entities

- **Release**: a published version with six OS/architecture archives, a checksums file and notes; the thing the installers fetch.
- **Installer**: the per-platform one-line entry point (macOS/Linux shell; Windows PowerShell) with its behaviors: detect, fetch, verify, install, PATH, upgrade, pin, uninstall.
- **Install location**: the stable per-user directory holding the executable; referenced by hooks and the service.
- **Distribution channel**: script (primary), Homebrew tap, winget (external review), each with the version it currently serves.
- **Documentation set**: README, user guides (install, getting started, how it works, security model, CLI reference, configuration, troubleshooting, FAQ), contributor guide, changelog.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On fresh macOS, Linux and Windows machines, one documented command installs AgentGuard with zero manual downloads or edits; `agentguard version` works in a new terminal within 60 seconds of starting the command (measured on the project's CI runners and on any connection of at least 20 Mbit/s; release archives stay under 15 MB).
- **SC-002**: 100% of installs verify the archive checksum; a corrupted or mismatched download results in zero files installed.
- **SC-003**: Re-running the command upgrades in place with 100% preservation of approvals, history, hooks and service, and the daemon runs the new version afterwards.
- **SC-004**: The documented uninstall removes 100% of installer- and integration-created files/entries (data kept unless purge requested), and Claude Code keeps working afterwards.
- **SC-005**: Every release is published within 30 minutes of tagging with all six archives and checksums, and the automated install test passes on all three operating systems for that release.
- **SC-006**: A newcomer following only the docs completes install → setup → first allowed and first blocked decision in under 10 minutes; all commands they run come verbatim from the docs.
- **SC-007**: 100% of CLI commands and flags are documented and match the binary (automatically verified); zero broken links in the docs.
- **SC-008**: The README contains zero placeholder/TODO markers at release time.

## Assumptions

- Releases are hosted on the project's public GitHub repository and the installer scripts are served from a stable URL under the project's control (the raw repository URL is acceptable; a short vanity domain is optional). The GitHub organization/repository name and any domain are still to be finalized; the documented commands will use whatever the final names are.
- The primary one-line channel is a hosted script (`curl … | sh` for macOS/Linux, `irm … | iex` for Windows PowerShell); package managers are secondary channels because winget publication depends on an external review process the project does not control.
- The one-liner installs and prints the next step by default; running the Claude setup as part of the same command is opt-in (the product brief documents install and setup as two commands).
- Per-user install locations are used (no elevation): a hidden per-user binary directory on macOS/Linux and a per-user application directory on Windows; a system-wide prefix is available as an explicit option.
- Existing daemon/hook integration references the executable by absolute path; keeping the install path stable across upgrades keeps them valid (already required by the prototype spec).
- Documentation ships as Markdown in the repository (README + `docs/`), rendered by GitHub; a standalone documentation website is out of scope for this feature.
- Code signing/notarization of binaries is out of scope; the installers avoid the browser-download paths that trigger OS gatekeeping, and the Windows installer clears the "mark of the web".
- The `install.sh` skeleton, README skeleton, and Homebrew/winget templates created in feature 001 (tasks T005/T007) are starting points; feature 001 tasks T075 and T076 are superseded by this feature; T077–T080 remain with feature 001.

### Out of scope

- Documentation website/hosting, localization, video tutorials.
- Linux distribution packages (deb/rpm/AUR), Nix, Docker images, and Scoop (may be added later; not required for one-line install).
- ~~Auto-update inside the running daemon (`agentguard self-update`) — upgrades happen by re-running the one-liner or the package manager.~~ **Superseded by feature `003-update-check-self-update`**, which adds `agentguard update` and a prompt at terminal start-up. Updates are still never applied without the user saying yes, and package-manager installations are still handed to the package manager.
- Binary signing/notarization and Windows SmartScreen reputation.
- Telemetry or install analytics.
