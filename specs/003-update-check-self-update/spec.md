# Feature Specification: Update Check & Guided Self-Update

**Feature Branch**: `003-update-check-self-update`

**Created**: 2026-08-16

**Status**: Draft

**Input**: User description: "we need to think about updates, when user run terminal we need to check if there is no newer releases in the github, if there is - we need to update our application (ask user whether he'd like to update or not or skip this version)"

**Clarification recorded (2026-08-16)**: the check and prompt happen **every time a new interactive terminal session starts** (option B) — a lightweight, cached, time-boxed start-up hook in the shell start-up files AgentGuard already manages (bash/zsh/fish on macOS and Linux; the PowerShell profile on Windows). Interactive `agentguard` commands do not prompt on their own; the explicit update command remains available everywhere.

**Context**: AgentGuard is installed by the one-line installers or package managers (feature `002-one-line-install-docs`) and runs as a per-user daemon plus Claude Code hooks (feature `001-agentguard-prototype`). Today a user learns about a new release only by re-running the installer or checking the release page. This feature makes AgentGuard notice newer published releases itself and, when the user opens a terminal, ask whether to update now, be reminded later, or skip that version — then perform the update safely, in a way that keeps the daemon and Claude hooks working. Feature 002 listed self-update as out of scope; this feature supersedes that exclusion.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Get asked about a new version when opening a terminal, and update in one step (Priority: P1)

A developer with AgentGuard installed opens a new terminal. Start-up is not slowed down noticeably; if a newer stable release is known (from a cached, anonymous check), AgentGuard prints the current and new version with a release-notes link and asks: **update now**, **not now**, or **skip this version**. Choosing "update now" downloads the correct build for their machine, verifies it, replaces the installed executable in place, restarts the background service so it runs the new version, confirms the Claude integration still works, prints what changed, and hands the terminal back.

**Why this priority**: The core ask — users must not fall behind on fixes without ever seeing a prompt; the prompt-and-update flow is the whole value.

**Independent Test**: With a fake newer release available, open a new interactive shell (bash, zsh, fish, PowerShell) with the AgentGuard start-up hook installed → the three-way prompt appears once; answering "update now" results in `agentguard version` showing the new version, the daemon reporting the new version, and `agentguard doctor` clean.

**Acceptance Scenarios**:

1. **Given** AgentGuard 0.1.0 is installed and a stable 0.2.0 release is published, **When** the user opens a new interactive terminal, **Then** they see "AgentGuard 0.2.0 is available (you have 0.1.0)" with a release-notes link and the prompt `Update now / Not now / Skip this version`, and the shell start-up added no perceptible delay beyond the prompt itself.
2. **Given** the prompt is shown, **When** the user chooses "update now", **Then** within 60 seconds the new build is downloaded and checksum-verified, the executable is replaced at its existing path, the daemon runs the new version, Claude hooks and approvals are intact, the output ends with the new version and a release-notes pointer, and the shell prompt appears normally.
3. **Given** the download or verification fails, **When** the user chose "update now", **Then** the installed version is untouched, the error explains the cause and the manual fallback, and the shell start-up completes normally.
4. **Given** the user chose "update now" but the binary was installed by a package manager, **When** the update runs, **Then** AgentGuard does not overwrite the package-managed file; it runs (with the user's consent) or prints the package manager's upgrade command instead and reports the result.
5. **Given** no newer version is known, **When** the user opens a terminal, **Then** nothing is printed and start-up is unaffected.

---

### User Story 2 - Decline or defer without being nagged (Priority: P1)

The user can answer "not now" and not be asked again for a while (at least a day), or "skip this version" and never be asked again for that particular version — while still being told about the *next* release. Opening many terminals or tabs never produces more than one prompt per interval. They can also see the update status and update on demand with an explicit command.

**Why this priority**: A start-up prompt is only acceptable if it respects the user's decision; otherwise it becomes noise and gets disabled.

**Independent Test**: Answer "not now" → new terminals within 24 hours show no prompt; answer "skip this version" → no prompt for that version ever, prompt reappears for a newer version; open five terminals at once → one prompt; `agentguard update --check` reports status; `agentguard update` performs the update on demand.

**Acceptance Scenarios**:

1. **Given** the user answered "not now", **When** they open further terminals within the reminder interval (default 24 hours), **Then** no prompt is shown; after the interval it is shown again (at most once per interval).
2. **Given** the user answered "skip this version" for 0.2.0, **When** they open terminals later, **Then** 0.2.0 is never offered again, but 0.2.1 (or any newer version) is offered.
3. **Given** the prompt is shown and the user does not answer within the prompt timeout (default 30 seconds) or presses an unexpected key, **When** the timeout elapses, **Then** it counts as "not now" and the shell continues.
4. **Given** several terminals start at the same time, **When** an update is available, **Then** exactly one of them shows the prompt; the others start silently.
5. **Given** any state, **When** the user runs the explicit update command, **Then** the check runs immediately (ignoring cache and skips) and the update proceeds after confirmation (or without confirmation with an explicit "yes" option).
6. **Given** the user disabled update checks (configuration or environment variable) or removed the start-up hook, **When** they open terminals or run commands, **Then** no network check happens and no prompt or notice is shown; the explicit update command still works.

---

### User Story 3 - Never disturb non-interactive or time-critical paths (Priority: P1)

Update checks and prompts must never slow down or interfere with Claude Code hooks, the background service, scripts, non-interactive shells (cron, IDE-spawned command shells, `sh -c`, CI), or machine-readable output. In those contexts AgentGuard is silent, and any network check runs in the background with a strict time limit and cached results.

**Why this priority**: The hooks are the product's safety gate and shell start-up runs everywhere; a prompt or a slow network call in the wrong place would break Claude sessions, scripts and editors.

**Independent Test**: Start non-interactive shells (`bash -c`, `zsh -c`, `pwsh -NonInteractive -Command`, no TTY), run the hook entry point, `--json` commands, and the daemon while a newer release exists → no prompt, no output, no waiting on the network, hook latency unchanged.

**Acceptance Scenarios**:

1. **Given** a newer release exists, **When** Claude Code invokes the hook or the daemon evaluates a command, **Then** neither performs a network check nor prints anything about updates.
2. **Given** a shell starts without an interactive terminal (piped, cron, IDE task runner, CI), **When** the start-up hook runs, **Then** it exits immediately with no output and no network access.
3. **Given** the network is unavailable or slow, **When** a check is due, **Then** the start-up hook never waits for it: the check runs in the background with a bounded time limit, its failure is remembered so it is not retried on every terminal, and the prompt (if any) is based on the last known result.
4. **Given** the check has never succeeded (fresh install, offline), **When** the user opens terminals, **Then** nothing is shown until a check succeeds; the explicit command explains the last failure.

---

### User Story 4 - The start-up check is installed, removed and configured with the tool (Priority: P2)

The start-up hook is added for the shells the user actually uses when AgentGuard is set up (by the installer or by `agentguard setup claude`), regardless of install channel, and removed when AgentGuard is uninstalled. Users who don't want their shell start-up files touched can opt out (the installer's existing "do not modify my shell files" choice is respected) and still update on demand.

**Why this priority**: The prompt can only appear at terminal start if the hook is present; installation and clean removal are what make option B trustworthy.

**Independent Test**: Install → the start-up files of the detected shells contain the AgentGuard-managed lines; uninstall → the lines are gone and the files are otherwise byte-identical; opt-out → no lines are written; PowerShell profile handling verified on Windows PowerShell 5.1 and PowerShell 7.

**Acceptance Scenarios**:

1. **Given** a fresh install through the script installer or a package manager followed by `agentguard setup claude`, **When** setup completes, **Then** the start-up hook is present for bash/zsh/fish (macOS/Linux) or the PowerShell profile (Windows), inside the same managed, marker-delimited block the installer uses, and setup reports it.
2. **Given** the user uninstalls AgentGuard, **When** uninstall completes, **Then** the managed lines are removed and no other content changed.
3. **Given** the user chose not to modify shell files, **When** they install or set up, **Then** no start-up hook is written, the output explains how to enable it later, and `agentguard update` still works.
4. **Given** Windows PowerShell blocks profile scripts (restrictive execution policy), **When** setup runs, **Then** it does not break the shell, tells the user that the start-up check cannot run under the current policy and how to enable it, and everything else keeps working; `cmd.exe` is not supported for the start-up prompt (documented).

---

### User Story 5 - Trust the update (Priority: P2)

Every update installs exactly what the release published: the correct build for the OS/architecture, verified against the published checksums, over a secure connection, never a downgrade, never a pre-release unless the user is already on a pre-release or opted in, and never overwriting a package-manager-owned file. The user can always see what will happen before it happens.

**Why this priority**: A self-updating security tool must be at least as trustworthy as the installer it replaces.

**Independent Test**: Corrupt the checksum of the fake release → update refuses and leaves the current version; publish only a pre-release → stable users are not offered it; run with a "plan only" option → nothing changes.

**Acceptance Scenarios**:

1. **Given** the downloaded archive does not match the published checksum, **When** the update runs, **Then** nothing is replaced and the user is told the verification failed.
2. **Given** the newest published release is a pre-release, **When** a user on a stable version checks for updates, **Then** they are not offered it (unless they opted in to pre-releases).
3. **Given** the update is requested with the "plan only" option, **When** it runs, **Then** it prints the current version, target version, install location, install channel and the actions it would take, and changes nothing.

---

### Edge Cases

- Shell start-up must stay fast: the hook reads cached state only; a due check is launched in the background (never awaited); when nothing is to be shown it adds no perceptible delay.
- Non-interactive or non-TTY shells (`bash -c`, `zsh -c`, cron, `pwsh -NonInteractive`, IDE task runners, `ssh host cmd`): the hook exits silently and instantly.
- Login vs. non-login shells, `.zshrc` vs `.zprofile`, `.bashrc` vs `.bash_profile`, fish `conf.d`, PowerShell 5.1 vs 7 profiles (`CurrentUserAllHosts`): the hook must be reachable in interactive sessions of each without running twice per session.
- Windows PowerShell restrictive execution policy prevents profile scripts: no breakage, clear guidance; `cmd.exe` has no start-up hook.
- Many terminals or tabs opened at once: exactly one prompt per reminder interval (shared state with locking); the others start silently.
- Unanswered prompt (user walks away, prompt inside a terminal opened by an IDE): counts as "not now" after the timeout; the shell is never left hanging indefinitely.
- The daemon is running while the executable is replaced: replacement must be atomic, the daemon restarted afterwards, and hooks keep pointing at the same stable path (feature 002 upgrade-coherence contract).
- On Windows the running executable cannot be overwritten: the update must still succeed (swap and clean up the old file later) and leave no stale copies behind after the next start.
- Multiple installations on PATH (script + package manager): the update targets the one that is running and warns about the shadowing copy.
- Network problems, rate limits, captive portals, corporate proxies: bounded background wait, cached failure with back-off, no repeated slowdowns; proxies honored.
- Two updates started concurrently (two prompts answered "yes" in different terminals): only one proceeds; the other reports that an update is already in progress and re-checks afterwards.
- Clock changes or missing state: the "not now" interval degrades gracefully (at most one prompt per interval, never a burst).
- The installed version is newer than the latest published (developer builds, yanked release): no prompt, no downgrade.
- Read-only or shared install location: the update explains that it cannot write there and how to update instead.
- Users who opted out of shell-file modification: no start-up hook; explicit command still works; the option to enable later is documented.
- The update check must remain anonymous: it fetches only the public release location, sends no identifiers, and can be fully disabled.

## Requirements *(mandatory)*

### Functional Requirements

**Checking**

- **FR-001**: The system MUST discover the newest published stable release for the currently installed channel (stable, or pre-release when opted in), at most once per check interval (default 24 hours), and remember the result, the check time and any failure locally.
- **FR-002**: The check MUST be anonymous (public release location only, no identifiers or telemetry), MUST use only secure connections, MUST honor standard proxy settings, and MUST run in the background with a bounded time limit; no user-facing path MUST ever wait for it.
- **FR-003**: The check MUST NOT run on the request path of Claude Code hooks or inside the daemon's decision path, and MUST NOT run at all when the user has disabled update checks (configuration or environment); the daemon MAY perform the periodic check in the background so that cached results are usually fresh.
- **FR-004**: The user MUST be able to trigger an immediate check and see the update status (installed version, latest version, install channel, skipped version, last check time/outcome, next check time, start-up hook state) with an explicit command.

**Start-up hook and prompting**

- **FR-005**: When a new interactive terminal session starts (bash, zsh or fish on macOS/Linux; PowerShell 5.1 or 7 on Windows) and a newer eligible version is known from cached results, the system MUST show a prompt with exactly three choices — update now, not now, skip this version — including current version, new version and a release-notes link.
- **FR-006**: The start-up hook MUST add no perceptible delay when there is nothing to show and MUST never wait for the network; the contexts in which it must stay silent are defined by FR-011.
- **FR-007**: "Not now" MUST suppress further prompts for the reminder interval (default 24 hours, configurable); "skip this version" MUST suppress prompts for that version permanently while still offering newer versions; an unexpected key or no answer within the prompt timeout (default 30 seconds, configurable) MUST count as "not now".
- **FR-008**: The prompt MUST appear at most once per reminder interval across all terminals and tabs (shared state with locking); concurrent terminals MUST NOT produce more than one prompt or update at a time.
- **FR-009**: The start-up hook MUST be installed by `agentguard setup claude` (and offered by the installers) for the shells detected on the machine, inside the marker-delimited block AgentGuard already manages, MUST be removed on uninstall leaving other content byte-identical, MUST respect the user's choice not to modify shell files, and MUST be enable-able/disable-able later with an explicit command.
- **FR-010**: On Windows the hook MUST be placed in the profile shared by Windows PowerShell 5.1 and PowerShell 7; if profile scripts are blocked by the execution policy the system MUST NOT break the shell and MUST tell the user how to enable the check; `cmd.exe` is explicitly unsupported for the start-up prompt.
- **FR-011**: The system MUST NOT prompt or print update notices when there is no interactive terminal (non-TTY or non-interactive shells such as `sh -c`, cron, IDE task runners), when machine-readable output is requested, in the Claude Code hook entry point, in the daemon, in scripts, or in CI-like environments; in those contexts the start-up hook MUST exit silently and immediately.

**Updating**

- **FR-012**: "Update now" and the explicit update command MUST download the release build matching the running OS/architecture, verify it against the published checksums over a secure connection, and replace the running executable at its existing path atomically; on any failure nothing MUST change and the reason and manual fallback MUST be shown.
- **FR-013**: The update MUST preserve local data (approvals, history, configuration) and the Claude integration, MUST restart the background service on the new version, MUST verify the integration afterwards, and MUST report old→new version with the release-notes link.
- **FR-014**: The system MUST detect the install channel (script/manual vs. package manager) and MUST NOT overwrite a package-manager-owned executable; for package-manager installs it MUST run (with the user's consent) or print the corresponding package-manager upgrade command instead.
- **FR-015**: The update MUST never downgrade, MUST skip pre-releases unless the installed version is a pre-release or the user opted in, and MUST support installing an explicitly requested version.
- **FR-016**: The explicit update command MUST offer a "plan only" mode that prints what would happen (versions, path, channel, actions) without changing anything, and a "yes" mode that skips confirmation for scripted use.
- **FR-017**: On Windows the update MUST succeed while the executable is in use and MUST clean up any temporary old copies automatically.
- **FR-018**: Only one update MUST run at a time; a second attempt MUST report the update in progress and re-check afterwards.

**Configuration & control**

- **FR-019**: The user MUST be able to disable update checks (and thereby prompts) via configuration and via an environment variable, change the check and reminder intervals and the prompt timeout, opt in to pre-releases, and enable/disable the start-up hook; defaults MUST work without any configuration.
- **FR-020**: All update decisions (prompt shown, choice, skip, update start/result, hook installed/removed) MUST be recorded locally so support can answer "why was I / wasn't I asked" and "what changed when".

### Key Entities

- **Update state**: installed version, latest known version and channel, last check time and outcome (with back-off), skipped version, deferred-until time, last prompt time, in-progress lock, install channel — persisted locally and shared by all terminals.
- **Release (remote)**: version, channel (stable/pre-release), assets per OS/architecture, checksums, notes link — the same artifacts feature 002 publishes.
- **Update decision**: one of update-now / not-now / skip-version (or timeout) with timestamp and version, plus the outcome of an attempted update.
- **Start-up hook**: the managed lines in each supported shell's start-up file (or PowerShell profile) that run the cached check and prompt; installed/removed with the tool; state (present/absent/blocked by policy) is reported.
- **Install channel**: script/manual (self-managed path) or package manager (Homebrew, winget), which determines whether the tool may replace its own executable.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: When a newer stable release exists, 100% of new interactive terminal sessions within the check interval show the update prompt — exactly once per reminder interval across all terminals — and never in non-interactive shells, hooks, the daemon or machine-readable contexts.
- **SC-002**: "Skip this version" is honored 100% of the time (zero prompts for the skipped version; prompt shown again for a newer version); "not now" and timeouts produce zero prompts within the reminder interval.
- **SC-003**: Shell start-up cost of the hook is under 50 ms when nothing is to be shown (measured on the project's CI runners) and the hook never blocks on the network.
- **SC-004**: Choosing "update now" completes (download, verify, replace, daemon restarted, integration verified) in under 60 seconds on the project's CI runners; approvals, history and hooks are 100% preserved.
- **SC-005**: 100% of updates verify checksums; corrupted or mismatched downloads leave the installed version untouched.
- **SC-006**: Package-manager installs are never overwritten by self-update (0 occurrences in tests); the correct package-manager command is offered instead.
- **SC-007**: Install → uninstall leaves shell start-up files byte-identical apart from the managed block, in 100% of tests across bash/zsh/fish/PowerShell.
- **SC-008**: With checks disabled or the hook removed, zero network requests and zero prompts are produced by the update feature.

## Assumptions

- Releases continue to be published as in feature 002 (public release page with per-OS/arch archives and a checksums file; "latest" excludes pre-releases). The check reuses that source; no separate update server or manifest is required.
- The start-up hook lives in the same marker-delimited block that feature 002's installer manages for PATH; on Windows a managed block in the PowerShell profile shared by 5.1 and 7 plays the same role. `agentguard setup claude` installs the block for all channels (script, Homebrew, winget), so package-manager users get the prompt too.
- The check interval, reminder interval and prompt timeout default to 24 h / 24 h / 30 s; "skip" is per version.
- The daemon performs the periodic background check when it is running; when it is not, the start-up hook launches a detached background check and prompts on a later terminal start; the start-up hook itself only reads cached state.
- Install channel is detected from the executable's location and the marker left by the installer (script/manual) versus package-manager-managed locations (Homebrew Cellar/links, winget package/links); unknown channels are treated as manual with a confirmation.
- Self-update replaces the executable at the same stable path the hooks and service reference, and relies on the upgrade-coherence behavior from feature 002 (stable path, daemon restart / self-refresh).
- Automatic, unattended updates (applying without asking) are out of scope; the user is always asked unless they explicitly pass a "yes" option to the update command.
- Notices inside Claude Code sessions are not part of this feature (option A/C were not chosen); they can be added later without changing the design.

### Out of scope

- Fully automatic background updates without user consent; scheduled updates; notices inside Claude Code sessions.
- Update servers, manifests, differential/delta updates, rollback UI (manual reinstall of a specific version via the installer or the update command with a pinned version remains available).
- Updating Claude Code itself or other tools; updating package-manager-installed builds by any means other than the package manager.
- Start-up prompts in `cmd.exe` or shells other than bash/zsh/fish/PowerShell.
- Signature/notarization verification beyond the published checksums.
