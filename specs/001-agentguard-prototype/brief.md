# AgentGuard — Original Product Brief (verbatim input to `/speckit-specify`, 2026-08-15)

> This file preserves the requirements exactly as supplied. `PROTOTYPE_SPEC.md` is derived from it and is the source of truth for implementation; where the two appear to differ, `PROTOTYPE_SPEC.md` Appendix C records the resolution.

---

You are a senior systems architect and security-focused product engineer.

Your task is to produce a complete but concise technical specification named:

PROTOTYPE_SPEC.md

for a new cross-platform local developer tool currently called "AgentGuard".

Do NOT implement any code yet.
Do NOT create a development plan yet.
Do NOT invent additional product features.
Your only task is to turn the product requirements below into a precise prototype specification that will later be used as the source of truth for implementation.

The specification must be explicit enough that another coding agent can implement the prototype step-by-step without guessing major architectural or behavioral decisions.

==================================================
1. PRODUCT PURPOSE
==================================================

AgentGuard is a local runtime permission/control layer for AI coding agents.

The initial integration is Claude Code only, but the architecture must allow future adapters for:

- Codex
- OpenCode
- Cursor
- VS Code integrations
- JetBrains integrations
- other AI coding agents

The core problem:

AI coding agents repeatedly ask users for permission to execute commands.

Users usually approve many of these commands repeatedly, so they often either:

1. suffer from permission fatigue, or
2. give the agent broad/unrestricted permissions.

Traditional allowlists are unsafe because they usually remember command strings or command prefixes rather than what the command actually does.

Example:

A user approves:

npm run cleanup

because package.json currently contains:

"cleanup": "rm -rf ./dist"

Later the script changes to:

"cleanup": "rm -rf ~/Documents"

A normal command allowlist may still allow:

npm run cleanup

AgentGuard must not.

AgentGuard should remember the semantic action/effects that were approved, not blindly trust the original command string.

Core product concept:

"Approve intent/effects, not command strings."

AgentGuard acts as a local runtime firewall between an AI coding agent and the system.

==================================================
2. PROTOTYPE GOAL
==================================================

The prototype must prove one key hypothesis:

AgentGuard can significantly reduce repeated permission prompts while remaining safer than a traditional string/prefix command allowlist.

The key demo scenario is:

1. Claude wants to execute:

   npm run cleanup

2. AgentGuard resolves it to:

   rm -rf ./dist

3. AgentGuard classifies it as:

   operation: DELETE
   target: ./dist
   scope: WORKSPACE_GENERATED

4. The user permanently approves this type of operation.

5. In a later Claude session, the same semantically equivalent action is automatically allowed.

6. package.json is then modified:

   "cleanup": "rm -rf ~/Documents"

7. Claude executes the exact same visible command:

   npm run cleanup

8. AgentGuard resolves the new underlying behavior.

9. The previous approval MUST NOT match.

10. AgentGuard must return BLOCK or ASK according to the safety policy, with an explanation that the resolved script/target/scope changed.

This scenario is the primary proof that AgentGuard is not just another command allowlist.

==================================================
3. TARGET PLATFORMS
==================================================

The prototype must work from the start on:

- macOS
- Linux
- Windows

Do not design a macOS-only prototype that is intended to be rewritten later.

The core architecture must be OS-independent.

Platform-specific code must live behind clear abstractions.

Supported CPU targets should eventually include:

- macOS arm64
- macOS amd64
- Linux arm64
- Linux amd64
- Windows arm64
- Windows amd64

==================================================
4. TECHNOLOGY STACK
==================================================

Primary language:

Go

Reasons:

- single native executable
- easy cross-compilation
- no JVM/Python/Node runtime required
- fast startup
- strong support for filesystem/process/IPC work
- suitable for a local daemon
- easy distribution for macOS/Linux/Windows

Storage:

SQLite

Requirements:

- local only
- no cloud
- no account
- no external database
- prefer a pure-Go SQLite driver to avoid CGO/cross-compilation complexity

Logging:

Go structured logging, preferably slog.

Configuration:

Use a small human-readable local config format if necessary, such as YAML or TOML.

Defaults must work without requiring users to edit configuration manually.

Testing:

Go test
Table-driven tests where appropriate.
Cross-platform integration tests.

Build/release:

GitHub Actions
GoReleaser or equivalent cross-platform Go release tooling.

==================================================
5. INSTALLATION UX
==================================================

Installation simplicity is a major product requirement.

The user must NOT need to:

- install Python
- install Node
- install Java
- install Docker
- manually edit Claude JSON configuration
- manually configure hooks
- manually configure a daemon
- create a cloud account

Target UX:

macOS:

brew install agentguard
agentguard setup claude

Windows:

winget install AgentGuard.AgentGuard
agentguard setup claude

Linux:

initially either:
- downloadable binary
- install script

followed by:

agentguard setup claude

The exact package repositories do not need to exist in the prototype, but the architecture must support this distribution model.

The setup command must perform configuration automatically.

==================================================
6. SINGLE BINARY DESIGN
==================================================

Use one executable:

agentguard

Do not require separate large executables for CLI and daemon.

The same binary should support commands/modes such as:

agentguard daemon
agentguard setup claude
agentguard uninstall claude
agentguard hook claude
agentguard daemon status
agentguard daemon start
agentguard daemon stop
agentguard daemon restart
agentguard approvals
agentguard history
agentguard approval show <id>
agentguard approval revoke <id>
agentguard approval disable <id>

Exact CLI syntax may be refined, but this should remain one binary.

==================================================
7. DAEMON ARCHITECTURE
==================================================

The prototype must include a local daemon from the start.

Reason:

The long-term product will support multiple agents, IDEs and UI clients.

The daemon becomes the central local policy/control plane.

Architecture:

Claude Code
    |
Claude Adapter / Hook
    |
local IPC
    |
AgentGuard daemon
    |
+---------------------+
| Parser              |
| Resolver            |
| Normalizer          |
| Policy Engine       |
| Approval Memory     |
| Audit               |
| Context / Cache     |
+---------------------+
    |
SQLite

The daemon owns:

- policy evaluation
- approval matching
- approval persistence
- audit history
- normalization
- resolution
- caches
- runtime context

Adapters should remain thin.

The core daemon must NOT depend directly on Claude-specific data structures.

==================================================
8. DAEMON LIFECYCLE
==================================================

The daemon should run as a per-user background service.

Preferred platform mechanisms:

macOS:
- launchd user service

Linux:
- systemd --user where available
- design must not tightly couple core logic to systemd

Windows:
- appropriate user-level background/service mechanism

The specification should explicitly separate:

- daemon process logic
- service installation/lifecycle logic

If the daemon crashes or is unavailable:

AgentGuard MUST fail safely.

It MUST NOT return ALLOW by default.

Preferred fallback:

ASK / allow Claude's native permission flow to take over.

A daemon failure must never silently become a security bypass.

==================================================
9. IPC
==================================================

Adapters communicate with the daemon through local IPC.

Preferred design:

macOS/Linux:
- Unix Domain Socket

Windows:
- Named Pipe

Create a common transport abstraction.

Do not use a public network service.

Avoid localhost HTTP as the default prototype transport unless there is a compelling technical reason.

The protocol should be simple and versionable.

JSON messages are acceptable for the prototype.

Example logical request:

{
  "protocol_version": 1,
  "agent": "claude",
  "session_id": "...",
  "cwd": "...",
  "tool": "Bash",
  "input": {
    "command": "npm run cleanup"
  }
}

Example logical response:

{
  "decision": "allow",
  "reason": "matched approval 42",
  "approval_id": 42
}

The exact schema should be specified.

==================================================
10. CLAUDE CODE INTEGRATION
==================================================

Claude Code is the ONLY agent adapter required by this prototype.

However, adapter boundaries must make future agent integrations possible without rewriting the core.

Use Claude Code hooks.

The prototype should investigate/use at least:

- PreToolUse
- PermissionRequest
- PostToolUse where useful
- ConfigChange where useful

Important behavior:

PreToolUse should be considered the actual runtime safety gate because AgentGuard must be able to deny dangerous actions even if Claude runs in a broad/bypass permission mode.

PermissionRequest should be used for integration with Claude's normal approval flow and persistent user approvals where appropriate.

The final specification should clearly define the role of each hook.

Claude-specific hook input must be converted into an internal generic ActionRequest.

Core policy code must not consume raw Claude hook JSON directly.

==================================================
11. AUTOMATIC CLAUDE SETUP
==================================================

Command:

agentguard setup claude

must:

1. detect Claude Code installation/configuration
2. locate relevant Claude configuration
3. safely back up configuration before modification
4. add AgentGuard hooks without deleting unrelated user hooks/settings
5. initialize AgentGuard local storage
6. register/start the daemon
7. run a self-test
8. report success/failure clearly

Desired result:

AgentGuard setup

✓ Claude Code detected
✓ Daemon installed
✓ Daemon running
✓ Permission hooks installed
✓ Database initialized
✓ Integration test passed

AgentGuard is ready.

Uninstall:

agentguard uninstall claude

must remove only AgentGuard-owned Claude configuration.

It must not destroy unrelated user settings.

==================================================
12. INTERNAL ACTION MODEL
==================================================

Create an OS/agent-independent internal model.

Raw command strings must not be the primary policy representation.

Define something conceptually similar to:

ActionRequest
NormalizedAction
ResolvedAction
Effect
Target
Scope
Context
Decision

A normalized action should be capable of representing:

- source agent
- source session
- raw command
- executable/tool
- semantic operation
- targets
- resolved targets
- filesystem effects
- network effects
- project/repository
- current working directory
- relevant dependency/script fingerprints
- confidence/resolution status

Do not over-engineer the exact type hierarchy, but the prototype specification should define a clear canonical model.

==================================================
13. DECISIONS
==================================================

Prototype decisions:

ALLOW
ASK
BLOCK

Semantics:

ALLOW:
The action may proceed without asking the user again.

ASK:
AgentGuard does not have sufficient safe authority to auto-allow the action.
The user/native Claude permission mechanism should decide.

BLOCK:
The action violates a hard safety rule and must not execute.

Do not introduce BACKUP/SNAPSHOT/ROLLBACK states in this prototype.

They may be future functionality.

==================================================
14. FAIL-SAFE PRINCIPLE
==================================================

The prototype must follow:

"When uncertain, ASK. Never guess ALLOW."

Examples that result in ASK:

- parser failure
- unsupported shell construct
- ambiguous path
- incomplete script resolution
- unknown executable
- unknown semantic effect
- unsupported command
- context cannot be established reliably
- dependency/script resolution fails

Do not classify unknown operations as harmless.

==================================================
15. SHELL SUPPORT
==================================================

The product is cross-platform from the start.

Required parser architecture:

parser/
  posix/
  powershell/
  cmd/

POSIX covers at least:

- bash
- zsh
- sh-like syntax

Windows covers:

- PowerShell
- cmd.exe

The parsers should convert different shell syntaxes into a shared representation.

Minimum constructs to recognize where feasible:

- simple commands
- arguments
- quoting
- environment/path expansion where safely possible
- &&
- ||
- ;
- pipelines

Do NOT implement a complete shell interpreter.

Unsupported or ambiguous syntax must result in ASK.

Never use naive prefix checks such as:

strings.HasPrefix(command, "./gradlew test")

because this would incorrectly trust commands such as:

./gradlew test && rm -rf ~

==================================================
16. PATH RESOLUTION
==================================================

All filesystem targets must be normalized/resolved before policy evaluation where possible.

Handle:

- relative paths
- absolute paths
- ..
- .
- ~
- symlinks
- workspace root
- platform path separators
- Windows drive letters
- UNC paths where applicable

Path traversal and symlink escape must be considered explicitly.

Example:

workspace/build/link -> /Users/user/Documents

must not be considered WORKSPACE_GENERATED simply because the textual path starts under the workspace.

==================================================
17. SCOPE MODEL
==================================================

Minimum scopes:

WORKSPACE
WORKSPACE_GENERATED
OUTSIDE_WORKSPACE
HOME
SYSTEM

The specification must define each scope precisely enough to implement deterministic classification.

WORKSPACE_GENERATED should cover known generated/build output locations where reasonably identifiable.

Examples:

Gradle:
build/

Maven:
target/

Node:
dist/
build/
coverage/

Do not assume every folder named build is automatically safe without project/context awareness.

==================================================
18. EFFECT MODEL
==================================================

Minimum effects:

READ
WRITE
CREATE
DELETE
EXECUTE
NETWORK

Also allow semantic operations such as:

RUN_TESTS
BUILD
GIT_OPERATION

Semantic operations may produce one or more low-level effects.

Example:

RUN_TESTS

may imply:

READ workspace
EXECUTE test process
WRITE WORKSPACE_GENERATED

==================================================
19. COMMAND SUPPORT FOR PROTOTYPE
==================================================

Initial recognizers/resolvers should include:

Filesystem:
- rm
- cp
- mv
- mkdir

Windows equivalents where applicable:
- Remove-Item
- Copy-Item
- Move-Item
- New-Item

Read/search:
- cat
- grep
- find
- reasonable Windows equivalents where applicable

Git:
- status
- diff
- add
- commit
- checkout/switch
- reset
- push
- force push

Package/build tools:
- npm
- pnpm
- yarn
- Gradle
- Maven

Network:
- curl

Do not try to understand every executable.

Unknown executables default to ASK.

==================================================
20. SCRIPT / WRAPPER RESOLUTION
==================================================

This is a critical differentiator.

For commands such as:

npm run cleanup

AgentGuard should resolve the actual configured script from package.json.

Example:

"cleanup": "rm -rf ./dist"

AgentGuard should recursively analyze that resolved command far enough to determine its effects.

Approval identity must include relevant dependency/script fingerprints.

If package.json changes in a meaningful way, approval matching must be reevaluated.

For prototype scope:

Strong support:
- npm
- pnpm
- yarn scripts

Reasonable support:
- Gradle tasks
- Maven goals

Do not pretend to statically understand arbitrary application code.

If resolution reaches an arbitrary program whose effects cannot be determined safely:

ASK.

==================================================
21. APPROVAL MEMORY
==================================================

Approvals must be stored persistently in SQLite.

An approval is NOT simply:

command_string = allow

It should capture at least:

- project/repository identity
- semantic action
- allowed effects
- allowed scope
- target constraints
- relevant resolved dependency/script fingerprints
- creation time
- last used time
- usage count
- enabled/disabled state

Possible approval levels for future design:

- exact
- semantic/action-level
- effect-based

For the prototype, support at least:

1. exact resolved action approval
2. safe semantic action approval within a project

Do not implement dangerous automatic broad generalization.

==================================================
22. APPROVAL MATCHING
==================================================

Approval matching must evaluate current resolved behavior against the original permitted behavior.

Example approved rule:

RUN_TESTS
project = project-X
writes <= WORKSPACE_GENERATED
network = false

A future test command may match even if the raw command differs, provided:

- semantic action is equivalent
- project matches
- resulting effects stay inside allowed constraints
- required context/fingerprints remain valid

Approval matching must be deterministic.

No runtime LLM call should decide whether an action is safe.

==================================================
23. APPROVAL INVALIDATION
==================================================

This is one of the most important prototype requirements.

Existing approval must no longer auto-allow an action if relevant underlying behavior changed.

Potential invalidation signals include:

- resolved script changed
- target changed
- target scope became broader
- new network effect appeared
- write became delete
- generated-file write became source/outside-workspace write
- project identity changed
- meaningful dependency/config fingerprint changed

The specification should distinguish:

"approval no longer matches"

from:

"approval record is permanently deleted"

Prefer preserving historical approvals/events for audit.

==================================================
24. SAFETY BASELINE
==================================================

Some actions must remain restricted regardless of learned approvals.

Prototype safety rules should include at least:

- recursive deletion of SYSTEM scope -> BLOCK
- broad recursive deletion of HOME -> BLOCK
- dangerous deletion outside workspace -> BLOCK
- obvious credential/private-key access -> ASK or BLOCK
- force push to protected/default branch -> ASK unless explicitly supported by stronger future policy
- path traversal escaping intended workspace -> not silently allowed
- symlink escape -> not silently allowed
- unknown executable -> ASK
- unresolved shell construct -> ASK

The specification should clearly explain that persistent approvals cannot silently override hard safety rules.

==================================================
25. CLAUDE USER APPROVAL FLOW
==================================================

Avoid creating a completely separate permission UX if Claude's native approval prompt can be reused.

Desired behavior:

AgentGuard evaluates first.

If:
ALLOW -> Claude proceeds without unnecessary repeated prompt where integration allows.

If:
ASK -> Claude's native permission request should be used.

If:
BLOCK -> AgentGuard denies before execution.

When a user chooses an appropriate persistent/native "always allow" option, AgentGuard should, where technically reliable, capture/import enough information to create its own semantic approval.

Existing Claude permissions should be investigated/imported during setup where safe.

However:

Never blindly convert broad Claude string permissions into stronger semantic AgentGuard permissions without validation.

==================================================
26. AUDIT LOG
==================================================

Every evaluated action should produce a local audit event.

At minimum:

- timestamp
- agent
- session id where available
- project
- cwd
- raw command
- resolved command/action
- effects
- scopes/targets
- decision
- reason
- matching approval id if any
- whether approval was invalidated/not matched
- execution result where PostToolUse information is available

The user must be able to answer:

"Why did AgentGuard auto-approve this?"

and:

"Why did AgentGuard block this?"

==================================================
27. SQLITE DATA MODEL
==================================================

Define an initial schema conceptually covering:

approvals
approval_conditions or equivalent
approval_events
executions / audit_events
schema_version

The exact normalization may be chosen by implementation, but the spec should list what data must survive restarts.

Database migrations/versioning should be considered from the start, even if only version 1 exists.

==================================================
28. CLI MANAGEMENT
==================================================

Prototype CLI should allow:

agentguard approvals
agentguard approval show <id>
agentguard approval revoke <id>
agentguard approval disable <id>
agentguard history
agentguard history --blocked

Optional useful commands:

agentguard doctor
agentguard status

CLI output should prioritize explaining:

- what is trusted
- scope
- project
- last used
- usage count
- why something was blocked/allowed

==================================================
29. SECURITY BOUNDARIES
==================================================

The specification must explicitly document:

- AgentGuard is local
- it does not sandbox arbitrary programs by itself
- it is a policy/control gate around supported AI agent tool actions
- unsupported/unresolved behavior fails to ASK
- it does not claim perfect static analysis
- it does not execute arbitrary commands merely to discover their effects
- hard safety policy takes precedence over approval memory
- daemon failure does not default to ALLOW

Do not oversell the prototype as a complete host security sandbox.

==================================================
30. OUT OF SCOPE FOR PROTOTYPE
==================================================

Explicitly exclude:

- Codex adapter
- OpenCode adapter
- Cursor adapter
- VS Code extension
- JetBrains plugin
- desktop GUI
- tray/menu-bar UI
- cloud sync
- accounts
- teams/RBAC
- enterprise policies
- remote approval
- LLM-based security decisions
- automatic semantic generalization without user confirmation
- Docker deep analysis
- Kubernetes deep analysis
- SQL/database safety engine
- backups
- rollback
- filesystem snapshots
- Git snapshots/worktrees
- full shell interpreter
- arbitrary program static analysis
- arbitrary Python/Node/Java source analysis
- browser automation
- mobile apps

Mention these as possible future directions only if useful, but do not include them in prototype acceptance criteria.

==================================================
31. PROJECT STRUCTURE
==================================================

Recommend a Go project layout roughly following:

cmd/
  agentguard/

internal/
  daemon/
  ipc/
  adapter/
    claude/
  parser/
    posix/
    powershell/
    cmd/
  resolver/
  action/
  scope/
  policy/
  approval/
  storage/
  audit/
  platform/
  config/

The exact structure can be improved if justified.

Keep dependencies pointing inward toward generic core abstractions.

Claude adapter must depend on core interfaces, not the opposite.

==================================================
32. CROSS-PLATFORM SERVICE ABSTRACTION
==================================================

Define interfaces for platform-specific behavior:

- application data directory
- configuration directory
- IPC endpoint
- service installation
- service start/stop/status
- executable discovery
- shell type
- path handling

Avoid scattered runtime.GOOS checks throughout business logic.

Platform differences should be isolated.

==================================================
33. CROSS-PLATFORM TESTING
==================================================

CI must eventually test:

- macOS
- Linux
- Windows

At minimum test:

- parser behavior
- path normalization
- scope classification
- symlink/path escape
- resolver behavior
- approval matching
- invalidation
- policy decisions
- daemon/client IPC
- SQLite persistence

Integration test:

Claude hook-format request
    ->
Claude adapter
    ->
daemon
    ->
resolver/policy
    ->
SQLite
    ->
decision response

Tests must not perform destructive actions on real user data.

Use temporary workspaces/filesystems.

==================================================
34. REQUIRED END-TO-END SCENARIOS
==================================================

The prototype specification must define acceptance tests including at least:

SAFE:

1.
git status
-> allowed after appropriate trust/policy

2.
Gradle/Maven/npm test execution
-> approved once
-> equivalent future test actions may auto-allow

3.
npm cleanup resolving to ./dist
-> approved

ASK:

4.
unknown executable
-> ASK

5.
unsupported shell syntax
-> ASK

6.
new unexpected network target
-> ASK

BLOCK:

7.
rm -rf ~/Documents
-> BLOCK

8.
path traversal escaping workspace
-> BLOCK or ASK according to hard policy, never auto-allow

9.
symlink inside workspace targeting outside workspace
-> never treated as safe workspace delete

INVALIDATION:

10.
npm run cleanup
initially:
rm -rf ./dist

approved

package.json changes to:
rm -rf ~/Documents

same raw command:
npm run cleanup

must NOT reuse old approval
must produce safety decision based on new resolved behavior

==================================================
35. PROTOTYPE DEFINITION OF DONE
==================================================

The prototype is DONE only when the following works on:

- macOS
- Linux
- Windows

Scenario:

1. Install AgentGuard.
2. Run:

   agentguard setup claude

3. Daemon is installed and running automatically.
4. Claude hooks are installed automatically.
5. Claude executes:

   npm run cleanup

6. AgentGuard resolves it to:

   rm -rf ./dist

7. AgentGuard classifies:

   DELETE
   target = ./dist
   scope = WORKSPACE_GENERATED

8. User creates a persistent approval through the intended permission flow.

9. Restart Claude / start a new Claude session.

10. Same action occurs again.

11. AgentGuard auto-allows it using stored semantic approval.

12. Modify package.json:

   "cleanup": "rm -rf ~/Documents"

13. Claude runs:

   npm run cleanup

14. AgentGuard detects:

   underlying script changed
   target changed
   scope changed from WORKSPACE_GENERATED to HOME

15. Previous approval is NOT reused.

16. AgentGuard blocks or requires explicit permission according to hard policy.

17. Audit history clearly shows why.

This end-to-end test is the primary prototype success criterion.

==================================================
36. DOCUMENT REQUIREMENTS
==================================================

Generate PROTOTYPE_SPEC.md with sections similar to:

1. Overview
2. Problem Statement
3. Product Goal
4. Prototype Scope
5. Non-Goals
6. Architecture
7. Technology Choices
8. Cross-Platform Design
9. Daemon
10. IPC
11. Claude Adapter
12. Setup / Installation
13. Internal Domain Model
14. Parsing
15. Resolution
16. Scope Classification
17. Effect Model
18. Policy Engine
19. Approval Memory
20. Approval Matching
21. Approval Invalidation
22. Safety Baseline
23. Storage
24. Audit
25. CLI
26. Failure Modes
27. Security Model / Limitations
28. Testing Strategy
29. End-to-End Acceptance Scenarios
30. Definition of Done

Use MUST / SHOULD / MAY language where useful.

Be explicit about invariants.

For example:

INVARIANT:
An approval MUST never match an action whose resolved effects exceed the effects originally permitted by that approval.

INVARIANT:
Parser/resolution uncertainty MUST never result in ALLOW.

INVARIANT:
A daemon failure MUST never result in ALLOW.

INVARIANT:
Hard safety BLOCK rules MUST take precedence over stored approvals.

INVARIANT:
Raw command equality alone MUST never be sufficient evidence for semantic approval reuse when the command references a mutable script/wrapper.

Add other important invariants you identify from the requirements.

==================================================
37. IMPORTANT DESIGN PRINCIPLES
==================================================

Keep these principles explicit throughout the specification:

1. Local-first.
2. Deterministic security decisions.
3. No runtime LLM dependency.
4. Fail safe.
5. Approve semantic effects, not strings.
6. Re-resolve mutable wrappers/scripts before reuse.
7. Cross-platform core.
8. Thin agent adapters.
9. One daemon / one policy engine.
10. One executable for distribution.
11. Easy install is a first-class requirement.
12. No hidden unrestricted fallback.
13. Explain every ALLOW/BLOCK decision.
14. Prototype only what is necessary to prove the core hypothesis.

==================================================
38. FINAL INSTRUCTION
==================================================

Before writing the final PROTOTYPE_SPEC.md:

- inspect the requirements for contradictions
- resolve minor architectural ambiguities conservatively
- explicitly mention any significant unresolved technical question
- do NOT silently expand scope
- do NOT turn the document into an implementation roadmap
- do NOT produce source code
- do NOT introduce an LLM into the AgentGuard runtime
- do NOT remove cross-platform requirements to make implementation easier

The resulting PROTOTYPE_SPEC.md will become the source of truth for the implementation phase.

Make it rigorous, implementation-ready, security-conscious, but not unnecessarily verbose.
