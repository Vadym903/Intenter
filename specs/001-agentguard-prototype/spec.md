# Feature Specification: AgentGuard Prototype — Semantic Runtime Permission Layer for AI Coding Agents

**Feature Branch**: `001-agentguard-prototype`

**Created**: 2026-08-15

**Status**: Draft

**Input**: User description: product brief "AgentGuard — cross-platform local developer tool: runtime permission/control layer for AI coding agents (Claude Code first)". The full brief (38 numbered requirement sections) is preserved verbatim in [`brief.md`](./brief.md). The implementation-ready technical contract derived from it is [`PROTOTYPE_SPEC.md`](./PROTOTYPE_SPEC.md), which is the source of truth for architecture and behavior; this document captures the user-facing scope, requirements, and success criteria in Spec Kit form.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Approve intent once, stop being asked for equivalent actions (Priority: P1)

A developer using Claude Code is asked for permission when Claude runs `npm run cleanup`. AgentGuard shows what the command actually does (it resolves to deleting the project's generated `./dist` folder) and the developer approves that behavior permanently. In later Claude sessions, the same behavior — regardless of whether Claude types `npm run cleanup`, `npm run cleanup --`, or an equivalent form that resolves to the same action — proceeds without a prompt.

**Why this priority**: This is half of the core hypothesis (fewer repeated prompts). Without it there is no product value.

**Independent Test**: In a temporary project with `"cleanup": "rm -rf ./dist"`, submit `npm run cleanup` from a first session (expect a prompt with the resolved explanation), create the approval, then submit the same action from a new session and observe an automatic allow with a reference to the stored approval.

**Acceptance Scenarios**:

1. **Given** a project whose `package.json` defines `cleanup` as `rm -rf ./dist` and no stored approvals, **When** Claude requests `npm run cleanup`, **Then** AgentGuard shows the resolved action (delete `./dist`, generated-workspace scope) and the user is asked through Claude's native prompt, which offers "Yes, and don't ask again".
2. **Given** the user chose "Yes, and don't ask again" (or ran `agentguard approve <event>`), **When** a new Claude session requests `npm run cleanup` again, **Then** AgentGuard allows it automatically with no prompt and the history shows which approval matched.
3. **Given** the same approval, **When** Claude requests `rm -rf ./dist` directly, **Then** AgentGuard asks (the approval was bound to the script it resolved through, and the direct command is a different, separately approvable action).

---

### User Story 2 - Changed behavior behind an approved command is never silently trusted (Priority: P1)

The same developer's `package.json` is later changed so that `cleanup` becomes `rm -rf ~/Documents`. Claude runs the visually identical `npm run cleanup`. AgentGuard re-resolves the script, notices the script text, target, and scope changed, refuses to reuse the earlier approval, blocks the action under its safety rules, and explains exactly what changed.

**Why this priority**: This is the other half of the hypothesis and the proof that AgentGuard is not a string allowlist.

**Independent Test**: After Story 1, edit `package.json`, submit `npm run cleanup` from a new session, and observe a block (or a prompt for the non-catastrophic variant `rm -rf ./src`) whose explanation names the changed script, old vs. new target, and old vs. new scope.

**Acceptance Scenarios**:

1. **Given** an approval for `npm run cleanup` → delete `./dist`, **When** the script changes to `rm -rf ~/Documents` and Claude runs `npm run cleanup`, **Then** the approval is not reused, the action is blocked, and the explanation states that the resolved script, target, and scope changed (generated workspace → home).
2. **Given** the same approval, **When** the script changes to `rm -rf ./src` (source folder, not catastrophic), **Then** the approval is not reused and the user is asked again with the same kind of explanation.
3. **Given** any approval, **When** the resolved behavior gains a new network destination, becomes recursive/wildcard, or moves to a broader scope, **Then** the approval does not match.

---

### User Story 3 - Catastrophic actions are stopped regardless of approvals or agent mode (Priority: P2)

Whatever the developer has approved before, and even when Claude runs in a broad/bypass permission mode, AgentGuard blocks recursive deletion of system locations, broad deletion of the home directory, recursive/wildcard/directory deletion outside the project, and modification of credential material; it forces confirmation for credential reads, force-pushes to protected branches, elevated commands, and paths that escape the project through `..` or symlinks.

**Why this priority**: Safety floor. The product must be strictly safer than a string allowlist.

**Independent Test**: Submit `rm -rf ~/Documents`, `rm -rf /`, a symlink-escape delete, and a traversal delete in a temporary environment and observe blocks with named rules; repeat with a stored approval that appears to cover the command and in bypass mode.

**Acceptance Scenarios**:

1. **Given** any state of approvals, **When** Claude requests `rm -rf ~/Documents` (or the PowerShell/cmd equivalents), **Then** AgentGuard blocks it before execution and names the rule.
2. **Given** a symlink `build/link` inside the project pointing to a folder in the home directory, **When** Claude requests deletion under `build/link`, **Then** the target is classified by its real location and never treated as safe workspace cleanup.
3. **Given** Claude runs in bypass mode, **When** a blocked action is requested, **Then** it is still denied; when an unapproved but non-catastrophic action is requested, AgentGuard does not add prompts (the user's mode choice stands).

---

### User Story 4 - When AgentGuard is unsure, the user's normal flow decides — never an automatic allow (Priority: P2)

For commands AgentGuard cannot understand (unknown executables, unsupported shell syntax, ambiguous paths, unresolvable scripts) or when its background service is down, AgentGuard neither allows nor blocks on its own: Claude's normal permission mechanism decides, exactly as it would without AgentGuard, and the user is warned (at most once per session per hour) if the service is unavailable.

**Why this priority**: The fail-safe principle protects the hypothesis' "safer than allowlists" claim and prevents regressions for commands outside prototype scope.

**Independent Test**: Submit an unknown tool, a `for` loop, and a `curl … | sh`; stop the service and submit a normally-blocked command; observe that no automatic allow is ever produced and that decisions defer to the native flow.

**Acceptance Scenarios**:

1. **Given** an unknown executable, **When** it is requested, **Then** the decision is "ask" and it cannot be turned into a stored approval.
2. **Given** the AgentGuard service is unreachable, **When** any command is requested, **Then** AgentGuard emits no allow and no deny, Claude's native flow proceeds, and the user sees a warning at most once per session per hour.

---

### User Story 5 - One-command setup and clean removal (Priority: P3)

A developer installs the single AgentGuard binary and runs `agentguard setup claude`. AgentGuard detects Claude Code, backs up its configuration, installs its hooks without touching unrelated settings, initializes its local storage, registers and starts its background service, self-tests, and prints a checklist. `agentguard uninstall claude` removes only what AgentGuard added.

**Why this priority**: Easy install is a first-class requirement, but the hypothesis can be demonstrated with a manually started service.

**Independent Test**: On a machine (or CI runner) with Claude Code present, run setup and uninstall; diff Claude's settings before/after uninstall (only AgentGuard entries differ) and confirm the checklist output.

**Acceptance Scenarios**:

1. **Given** Claude Code is installed, **When** the user runs `agentguard setup claude`, **Then** all setup steps report success and the user is told to restart Claude sessions.
2. **Given** a Claude configuration containing the user's own hooks and permissions, **When** setup then uninstall run, **Then** the user's own entries are preserved byte-for-byte in meaning and a backup exists.

---

### User Story 6 - Every decision is explainable and manageable (Priority: P3)

The developer can list what is trusted (approvals with scope, project, usage counts, last use), see the full history of decisions with reasons, ask "why was this allowed/blocked?", and disable or revoke approvals.

**Why this priority**: Trust in an automatic system depends on transparency; needed for the demo's final step ("audit history clearly shows why").

**Independent Test**: After Stories 1–2, run the listing/history commands and confirm the invalidation event shows the old approval, the changed fingerprint, and the scope change.

**Acceptance Scenarios**:

1. **Given** decisions have been made, **When** the user opens history for a blocked event, **Then** it shows raw command, resolved chain, targets with scopes, effects, the deciding rule/approval, and what changed relative to related approvals.
2. **Given** an approval, **When** the user revokes it, **Then** it no longer matches but remains visible in history.

---

### User Story 7 - Read-only workspace commands do not nag (Priority: P3)

Commands that only read inside the project (`git status`, `git diff`, `cat`, `grep`, `find` without destructive predicates) proceed without prompts by policy, unless they touch credential files or escape the project.

**Why this priority**: Immediate, visible prompt reduction with negligible risk.

**Independent Test**: Submit `git status` and `grep -r foo src` in a project (allowed), then `cat .env` and `cat ../../etc/passwd` (asked / not auto-allowed).

**Acceptance Scenarios**:

1. **Given** a project, **When** `git status` is requested, **Then** it is allowed by the read-only policy and history says so.
2. **Given** a project, **When** `cat .env` is requested, **Then** the user is asked (credential file) even though it is inside the project.

---

### Edge Cases

- Commands chained with `&&`, `||`, `;`, or pipes: every part is evaluated; `./gradlew test && rm -rf ~` is blocked by the deletion, never trusted by a prefix.
- `cd` inside a command changes where later relative paths resolve; `cd ~ && git status` no longer counts as a workspace read.
- Globs at sensitive roots (`rm -rf ~/*`, `rm -rf *` at the project root) are treated as broad deletions.
- Path traversal (`./dist/../../x`) and symlink/junction escapes are classified by real location and never silently allowed.
- npm/pnpm/yarn `pre`/`post` scripts, script arguments after `--`, nested `npm run` calls, and monorepo package files are included in resolution; workspace-selection flags fall back to "ask".
- On Windows, package-manager scripts may run under `cmd.exe` while Claude's own commands run under Git Bash; scripts are evaluated under both interpretations and the stricter result wins.
- The project root cannot be the home directory or a system root; commands run from such locations are asked about, not auto-allowed.
- Claude's own deny/ask permission rules are never overridden by an AgentGuard allow.
- Multiple Claude sessions or subagents run concurrently; evaluations are independent and storage stays consistent.
- The AgentGuard binary is moved after setup; the health check detects it and setup can be re-run.
- Claude's settings file is malformed JSON; setup and uninstall refuse to write and report the problem.
- Very long commands, deeply nested scripts, or huge build-file sets exceed limits and result in "ask".

## Requirements *(mandatory)*

### Functional Requirements

**Interception and evaluation**

- **FR-001**: The system MUST evaluate every shell command Claude Code is about to execute (Bash tool, and PowerShell tool where present) before it runs and return exactly one of ALLOW, ASK, or BLOCK.
- **FR-002**: The system MUST convert Claude-specific hook input into an agent-independent action request; core policy MUST NOT depend on Claude-specific data.
- **FR-003**: The system MUST parse POSIX (bash/zsh/sh), PowerShell, and cmd.exe command syntax for simple commands, arguments, quoting, `&&`, `||`, `;`, pipelines, redirections, `cd`, and safe home/temp variable expansion; unsupported or ambiguous syntax MUST result in ASK.
- **FR-004**: The system MUST resolve package-manager scripts (npm, pnpm, yarn) to the commands they execute, including pre/post scripts and passthrough arguments, and MUST evaluate the resolved commands recursively within bounded depth.
- **FR-005**: The system MUST classify Gradle tasks and Maven goals into build/test/clean/info operations with declared effects, and MUST treat publishing/deploy tasks and unknown tasks as not auto-allowable.
- **FR-006**: In addition to the wrapper resolution in FR-004/FR-005, the system MUST recognize at least: rm, cp, mv, mkdir, cat, grep, find (and PowerShell/cmd equivalents), git status/diff/add/commit/checkout/switch/reset/push (incl. force push), npm/pnpm/yarn, gradle/gradlew, mvn/mvnw, curl. Unknown executables MUST result in ASK.
- **FR-007**: The system MUST normalize every filesystem target (relative, absolute, `.`/`..`, `~`, separators, drive letters, UNC, globs) and classify it into exactly one scope: WORKSPACE, WORKSPACE_GENERATED, OUTSIDE_WORKSPACE, HOME, SYSTEM — using real (symlink-resolved) locations.
- **FR-008**: The system MUST identify generated/build output locations (e.g., `dist/`, `build/`, `coverage/`, `node_modules/`, Gradle `build/`, Maven `target/`) only in the presence of the corresponding project marker files, never by folder name alone.
- **FR-009**: The system MUST represent each action as semantic operations plus low-level effects (READ, WRITE, CREATE, DELETE, EXECUTE, NETWORK) with targets, scopes, and flags (recursive, wildcard, broad, force, traversal, symlink escape, sensitive, elevated).

**Decisions and safety**

- **FR-010**: Hard safety rules MUST run first and MUST take precedence over stored approvals: SYSTEM deletion/modification → BLOCK; broad or recursive HOME deletion → BLOCK; recursive, wildcard, or directory deletion outside the workspace (except inside the temp directory) → BLOCK; credential/private-key modification → BLOCK; credential reads, force-push to protected/default branch, remote-branch deletion, elevated commands, traversal/symlink escapes, workspace-root or `.git` deletion → ASK that no approval can override.
- **FR-011**: When parsing, resolution, path interpretation, project context, or the background service is uncertain or unavailable, the system MUST NOT return ALLOW.
- **FR-012**: The system MUST allow read-only actions confined to the workspace (no network, no credential paths, no escapes) by policy, and this policy MUST be switchable off.
- **FR-013**: In Claude's bypass permission mode the system MUST still enforce BLOCK decisions and MUST NOT add prompts for non-blocked actions.
- **FR-014**: For understood actions that were never approved, the system MUST leave the decision to Claude's native permission prompt (which offers persistent approval) while showing its own resolution summary; when a stored approval no longer matches or policy requires confirmation, it MUST force a prompt with an explanation even if a Claude string rule would have allowed the command; for actions it cannot judge, it MUST defer to Claude's native mechanism without adding a prompt.
- **FR-015**: The system MUST NOT override Claude's own deny or ask permission rules with an ALLOW.

**Approval memory**

- **FR-016**: The system MUST persist approvals locally, capturing project identity, semantic operations, allowed effects and scopes, target constraints, resolved script/config fingerprints, creation and last-use times, usage count, and state (active/disabled/revoked).
- **FR-017**: The system MUST support an exact resolved-action approval and a project-scoped semantic approval; automatic creation paths MUST produce exact approvals only; semantic approvals require explicit user confirmation.
- **FR-018**: Approval matching MUST be deterministic and MUST fail whenever the current resolved effects exceed the approved envelope, a target or scope broadened, a network destination is new, or a recorded script/config fingerprint is missing or changed; a previous approval MUST NOT be reused in any of these cases, the approval record MUST be preserved, and the mismatch MUST be explained.
- **FR-019**: Users MUST be able to create an approval from an evaluated event, and to list, inspect, disable, enable, and revoke approvals.
- **FR-020**: The system SHOULD import a persistent Claude "always allow" rule for a command as an exact approval only after fully resolving and validating that command, at most once per project/rule/command, and MUST NOT convert broad string rules into broader approvals.

**Setup, lifecycle, transparency**

- **FR-021**: `agentguard setup claude` MUST detect Claude Code, back up its configuration, install AgentGuard hooks without removing unrelated settings, initialize storage, register and start the background service, run a self-test, and report each step; `agentguard uninstall claude` MUST remove only AgentGuard-owned configuration.
- **FR-022**: The background service MUST run per user and be controllable (start/stop/restart/status) from the CLI. It MUST start automatically at login on macOS (launchd) and Windows (per-user autostart), and on Linux where `systemd --user` is available; where no per-user service manager exists, the hook MUST lazy-start the service on first use and the health check MUST report "unmanaged" mode.
- **FR-023**: Every evaluation MUST produce a local audit event (time, agent, session, project, cwd, raw command, resolved action, effects, scopes/targets, decision, reason, matched approval, mismatch information, execution outcome when known) that fully explains the decision.
- **FR-024**: The CLI MUST show approvals, approval details, history (with filters such as blocked-only), the explanation for any event, service status, and a health check.
- **FR-025**: The system MUST run on macOS, Linux, and Windows with the same core behavior, and all core behavior MUST be covered by automated tests that never touch real user data.

### Key Entities

- **Action Request**: one agent tool invocation to evaluate — agent, session, tool, shell dialect, raw command, working directory, project hint.
- **Resolved Action**: what the command will actually do — ordered commands after wrapper/script resolution, semantic operations, effects, targets, resolution status, fingerprints of mutable inputs, explanation chain.
- **Target / Scope**: a filesystem or network destination with its real location and classification (WORKSPACE, WORKSPACE_GENERATED, OUTSIDE_WORKSPACE, HOME, SYSTEM) and flags.
- **Effect**: READ/WRITE/CREATE/DELETE/EXECUTE/NETWORK with target/scope/flags; semantic operations (e.g., RUN_TESTS, BUILD, CLEAN, FS_DELETE, GIT_PUSH, HTTP_REQUEST) group effects.
- **Project (Workspace)**: the root that scopes approvals; identified deterministically from the working directory.
- **Approval**: a persisted grant — project, kind (exact/semantic), semantic operations, effect envelope, targets, fingerprints, origin, state, usage statistics.
- **Approval Condition / Approval Event**: the fingerprint conditions attached to an approval (which must still hold for it to match) and the audited lifecycle events of an approval (created, matched, not matched, disabled, enabled, revoked).
- **Consent Import Record**: the once-only link between a Claude persistent "always allow" rule, a raw command, a project, and the approval it produced.
- **Audit Event**: the persisted record of one evaluation and its outcome, including execution result and prompt information.
- **Decision**: ALLOW/ASK/BLOCK with a class and reason, optional matched approval or hard rule, and mismatch reports.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In the primary demonstration, after a single approval, 100% of subsequent semantically equivalent actions across new sessions proceed with zero permission prompts.
- **SC-002**: In the acceptance suite, 100% of behavior-change cases (script text, target, scope, new network destination, recursive/wildcard/force flags, build-config change) prevent reuse of the earlier approval; zero false auto-allows.
- **SC-003**: 100% of catastrophic-action cases (system/home/outside-workspace deletion, credential modification, symlink and traversal escapes) are blocked or forced to confirmation regardless of stored approvals and agent mode.
- **SC-004**: Setup completes with one command in under 60 seconds and requires no manual configuration edits, on all three operating systems.
- **SC-005**: Evaluation adds under 500 ms (95th percentile) to a command on a warm system, so the user perceives no delay.
- **SC-006**: For 100% of allow and block decisions, a user can obtain the reason (rule or approval, targets, scopes, what changed) from history without re-running anything.
- **SC-007**: In all setup/uninstall tests, the user's unrelated Claude settings are unchanged after uninstall and a backup exists.
- **SC-008**: In all uncertainty and service-unavailable cases, zero automatic allows are produced.
- **SC-009**: The end-to-end acceptance suite passes on macOS, Linux, and Windows in continuous integration.

## Assumptions

- Claude Code (reference version 2.1.233) exposes `PreToolUse`, `PermissionRequest`, and `PostToolUse` hooks with the fields verified in `PROTOTYPE_SPEC.md` §11; a minimum supported version will be pinned during implementation.
- Claude's native permission dialog remains the user-facing prompt; AgentGuard supplies the explanation and never builds a separate UI. A minimal CLI command creates approvals from evaluated events for non-interactive and edge cases.
- Approvals are scoped to a project (workspace root) and are agent-independent; the prototype has one agent.
- Read-only-in-workspace auto-allow is on by default and configurable.
- Unknown executables and unresolvable wrappers are not approvable in the prototype; they defer to Claude's native flow (no regression, no improvement).
- The user's own tests, build scripts, and hook scripts run within declared envelopes when RUN_TESTS/BUILD/CLEAN are approved; that trust boundary is explicit and documented.
- Windows uses per-user login autostart (Task Scheduler or Run key) rather than a Windows Service, avoiding elevation.
- Tests use temporary workspaces and a redirected home directory; no destructive command is ever executed by tests.

### Out of scope for the prototype (see PROTOTYPE_SPEC.md §5)

Other agent adapters (Codex, OpenCode, Cursor, VS Code, JetBrains); any GUI/tray UI; cloud sync, accounts, teams/RBAC, enterprise policies, remote approval; LLM-based decisions; automatic semantic generalization without user confirmation; Docker/Kubernetes/SQL deep analysis; backups, rollback, snapshots, Git worktrees; a full shell interpreter; static analysis of arbitrary programs or source code; browser automation; mobile apps; gating of non-shell agent tools (file write/edit tools).

### Mandated technical constraints (from the product brief; detailed in PROTOTYPE_SPEC.md §6–§10)

Go single binary; SQLite via a pure-Go driver; structured logging; optional TOML config with working defaults; Unix domain socket / Windows named pipe IPC (no HTTP); launchd / systemd --user / Windows per-user autostart; Claude Code hooks; GitHub Actions + GoReleaser; six OS/arch build targets. These are constraints the brief imposes on the solution, recorded here for completeness rather than as behavioral requirements.
