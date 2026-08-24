# Data Model: AgentGuard Prototype

**Feature**: `001-agentguard-prototype` · **Date**: 2026-08-16 · **Source**: `PROTOTYPE_SPEC.md` §13, §16, §19, §20, §23, §24

Two layers are described: **in-memory domain entities** (package `internal/action` and friends; agent- and OS-independent) and **persisted entities** (SQLite schema v1). Field types are conceptual; Go representations are chosen in implementation.

---

## 1. Domain entities (in memory)

### 1.1 ActionRequest

Adapter output; input to the daemon's `evaluate`.

| Field | Type | Rules |
|---|---|---|
| `agent` | string | required; `"claude"` |
| `agent_version` | string | optional |
| `session_id` | string | required |
| `tool_use_id` | string | optional (absent for PermissionRequest) |
| `tool` | string | required; `Bash` or `PowerShell` for the Claude adapter |
| `dialect` | enum `posix\|powershell\|cmd` | required |
| `raw_command` | string | required; ≤ 64 KiB, else `BAD_REQUEST` |
| `cwd` | string | required; absolute |
| `project_hint` | string | optional |
| `agent_consent` | AgentConsent? | optional |
| `adapter_context` | map | opaque; stored verbatim in audit |
| `received_at` | timestamp | set by daemon |

**AgentConsent**: `{ kind: "persistent_rule", rule_keys: [string], exact: bool }`.

### 1.2 Context (per request, cached per workspace root)

| Field | Notes |
|---|---|
| `workspace_root` | canonical path or empty when `context_status ≠ OK` |
| `project_id` | `sha256(workspace_root)` hex |
| `home_dir`, `temp_dir` | canonical |
| `platform` | `darwin\|linux\|windows` |
| `generated_roots` | []canonical dir |
| `git` | `{gitdir, default_branch?, current_branch?, remotes: map[name]host, hooks_dir, hooks_present: [name]}` |
| `package_manager` | `{kind: npm\|pnpm\|yarn-classic\|yarn-berry\|unknown, script_shell?: string}` |
| `context_status` | `OK\|WORKSPACE_UNDEFINED\|ERROR` |

Validation: `workspace_root` MUST NOT equal/contain HOME, be `/` or a drive root, or lie under a SYSTEM root → `WORKSPACE_UNDEFINED`.

### 1.3 Parsed command model (parser output)

`ParsedCommand { dialect, commands: [SimpleCommand], operators: [";" | "&&" | "||" | "|"], unsupported: [UnsupportedConstruct] }`
`SimpleCommand { argv: [Word], env_assignments: [{name, value}], redirections: [{op, target: Word}], effective_cwd: string, raw_text: string }`
`Word { text, quoted: bool, contains_glob: bool, contains_unexpanded_var: bool }`
`UnsupportedConstruct { kind, position, text }`

Rule: any `unsupported` entry ⇒ action status `PARSE_FAILED`.

### 1.4 Target

| Field | Type |
|---|---|
| `raw` | string |
| `canonical` | absolute canonical path (empty when unresolvable) |
| `display` | workspace-relative / `~`-relative / absolute |
| `scope` | `WORKSPACE\|WORKSPACE_GENERATED\|OUTSIDE_WORKSPACE\|HOME\|SYSTEM` |
| `exists`, `is_dir`, `is_symlink` | bool |
| `flags` | set ⊆ {`wildcard`,`broad`,`traversal`,`symlink_escape`,`sensitive`,`tool_cache`,`network_path`,`temp`} |
| `status` | `RESOLVED\|AMBIGUOUS` |

### 1.5 Effect

| Field | Type |
|---|---|
| `type` | `READ\|WRITE\|CREATE\|DELETE\|EXECUTE\|NETWORK` |
| `target` | Target? (fs effects) |
| `network` | `NetworkTarget{host, port?, scheme?, method?, declared_kind?}` (NETWORK) |
| `program` | `{name, resolution: DECLARED\|UNRESOLVED, elevated: bool, streamed: bool}` (EXECUTE); `streamed` marks a stage fed by a pipe, which is what R12 fires on |
| `flags` | set ⊆ {`recursive`,`force`,`wildcard`,`discards_changes`,`elevated`,`inline_credential`,`insecure_tls`,`delete`,`broad`} (`delete`/`broad` qualify `git push`, §15.4/R7) |

Envelope pair form used by approvals: `(type, scope, sorted flags)` for fs effects; `NetworkTarget` (host+port+scheme+method or `declared_kind`) for network.

### 1.6 ResolvedCommand / ResolvedAction

`ResolvedCommand { executable, semantic_op, targets: [Target], effects: [Effect], status: RESOLVED|DECLARED|UNRESOLVED, fingerprints: [Fingerprint], resolved_from: [string], children: [ResolvedCommand], git?: {remote, branch, branch_known} }`

`git` carries the ref a git operation targets, for hard rule R7. It is **not** part of `action_key`: R7 is evaluated before approvals (§18.1 steps 1–2), so no approval can bypass it.

`ResolvedAction { request_ref, context_ref, commands: [ResolvedCommand], semantic_ops: [SemanticOp] (ordered, wrappers included), effects: [Effect] (union), fingerprints: [Fingerprint] (union, key-unique), status: RESOLVED|DECLARED|UNRESOLVED|PARSE_FAILED|CONTEXT_FAILED, action_key: sha256 hex, explanation: [string] }`

`Fingerprint { key: string, value: sha256 hex, description: string }` — key formats: `npm-script:<rel path>#scripts.<name>`, `npm-config:.npmrc#script-shell`, `npm-config:package.json#packageManager`, `gradle-config`, `maven-config`.

Semantic ops: `FS_READ FS_CREATE FS_COPY FS_MOVE FS_DELETE GIT_STATUS GIT_DIFF GIT_LOG GIT_SHOW GIT_BRANCH GIT_REV_PARSE GIT_ADD GIT_COMMIT GIT_CHECKOUT GIT_RESET GIT_PUSH RUN_SCRIPT RUN_TESTS BUILD CLEAN BUILD_TOOL_INFO INSTALL_DEPENDENCIES HTTP_REQUEST SHELL_NAVIGATE NOOP UNKNOWN`.

Status ordering for aggregation: `RESOLVED > DECLARED > UNRESOLVED > PARSE_FAILED/CONTEXT_FAILED` (take the weakest).

### 1.7 Decision / EvaluationResult

`Decision { outcome: ALLOW|ASK|BLOCK, class: DecisionClass, reason: string, approval_id?: int, hard_rule?: string, mismatch_reports: [{approval_id, differences: [string]}], engine_version: int }`

`EvaluationResult = Decision + { audit_event_id?: int, resolution_status, explanation: [string], user_message: string, imported_approval_id?: int }`

DecisionClass by outcome — ALLOW: `POLICY_READONLY_WORKSPACE, APPROVAL_MATCH, RULE_IMPORT`; ASK: `NO_MATCHING_APPROVAL, APPROVAL_MISMATCH, POLICY_REQUIRES_CONFIRMATION, UNRESOLVED_COMMAND, UNSUPPORTED_SYNTAX, AMBIGUOUS_PATH, CONTEXT_UNAVAILABLE, AGENT_RULE_CONFLICT, ENGINE_ERROR`; BLOCK: `HARD_RULE_R1..R5`.

Adapter action (recorded, not decided by core): `allow | deny | prompt | defer`.

---

## 2. Persisted entities (SQLite schema v1)

Relationships: `projects 1—* approvals`, `approvals 1—* approval_conditions`, `approvals 1—* approval_events`, `audit_events *—0..1 approvals (matched)`, `agent_rule_imports *—1 approvals`, `approval_events *—0..1 audit_events`.

### 2.1 projects

| Column | Type | Rules |
|---|---|---|
| `id` | TEXT PK | sha256 hex of canonical root |
| `root_path` | TEXT | canonical |
| `remote_url` | TEXT NULL | informational |
| `first_seen_at`, `last_seen_at` | TEXT (RFC3339 UTC) | |

### 2.2 approvals

| Column | Type | Rules |
|---|---|---|
| `id` | INTEGER PK | |
| `project_id` | TEXT FK→projects | required |
| `kind` | TEXT | `EXACT` \| `SEMANTIC` |
| `semantic_ops` | TEXT JSON array | ordered |
| `envelope` | TEXT JSON | array of `{type, scope, flags[]}` |
| `targets` | TEXT JSON NULL | EXACT only: array of display paths |
| `network` | TEXT JSON | array of NetworkTarget |
| `engine_version` | INTEGER | |
| `origin` | TEXT | `claude_prompt` \| `claude_rule` \| `cli` |
| `origin_ref` | TEXT NULL | rule keys / event id |
| `created_from_event_id` | INTEGER NULL | FK→audit_events |
| `created_from_raw_command` | TEXT | |
| `created_by_agent` | TEXT | |
| `state` | TEXT | `ACTIVE` \| `DISABLED` \| `REVOKED` |
| `note` | TEXT NULL | |
| `created_at`, `last_used_at`, `disabled_at`, `revoked_at` | TEXT NULL | |
| `use_count` | INTEGER | default 0 |

**State machine**: `ACTIVE ⇄ DISABLED` (disable/enable), `ACTIVE|DISABLED → REVOKED` (terminal). Rows are never deleted by normal operation. Every transition writes an `approval_events` row.

**Creation validation** (`create_approval` / import): source event exists; source `resolution_status ∈ {RESOLVED, DECLARED}`; source hard outcome `PASS`; no `AMBIGUOUS` target; project of caller = project of event; `kind=SEMANTIC` only via explicit CLI flag.

### 2.3 approval_conditions

| Column | Type | Rules |
|---|---|---|
| `approval_id` | INTEGER FK | |
| `kind` | TEXT | `fingerprint` (v1) |
| `key` | TEXT | fingerprint key |
| `value` | TEXT | sha256 hex |
| PK | (`approval_id`,`kind`,`key`) | |

### 2.4 approval_events

| Column | Type | Rules |
|---|---|---|
| `id` | INTEGER PK | |
| `approval_id` | INTEGER FK | |
| `event_type` | TEXT | `created` \| `matched` \| `not_matched` \| `disabled` \| `enabled` \| `revoked` |
| `audit_event_id` | INTEGER NULL | |
| `at` | TEXT | |
| `details` | TEXT JSON NULL | mismatch differences, note |

### 2.5 audit_events

| Column | Type | Rules |
|---|---|---|
| `id` | INTEGER PK | |
| `at` | TEXT | |
| `agent`, `agent_version`, `session_id`, `tool_use_id`, `hook_event` | TEXT (nullable except agent) | |
| `project_id` | TEXT NULL | FK→projects |
| `cwd`, `tool`, `dialect`, `raw_command` | TEXT | |
| `resolved` | TEXT JSON | full ResolvedAction (commands, targets, effects, fingerprints, chain) |
| `resolution_status` | TEXT | |
| `decision`, `decision_class`, `reason` | TEXT | |
| `hard_rule` | TEXT NULL | |
| `matched_approval_id` | INTEGER NULL | |
| `related_approval_ids` | TEXT JSON NULL | |
| `mismatch_report` | TEXT JSON NULL | |
| `imported_approval_id` | INTEGER NULL | set by consent import path (b) |
| `adapter_action` | TEXT NULL | `allow\|deny\|prompt\|defer` — reported by the adapter with `record_adapter_action` after it has answered the agent (§11.3). NULL until then, and for a `dry_run`, which writes no row at all. |
| `adapter_context` | TEXT JSON NULL | |
| `prompt_shown` | INTEGER | 0/1 (PermissionRequest) |
| `permission_suggestions` | TEXT JSON NULL | verbatim |
| `execution_status`, `execution_at`, `response_summary` | TEXT NULL | PostToolUse |
| `engine_version` | INTEGER | |
| `dry_run` | INTEGER | 0/1 (dry-run rows are not written by default) |

Invariant: written before the `evaluate` response is returned (non-dry-run); write failure ⇒ decision ASK/`ENGINE_ERROR`.

### 2.6 agent_rule_imports

| Column | Type | Rules |
|---|---|---|
| `id` | INTEGER PK | |
| `project_id`, `agent`, `rule_key`, `raw_command` | TEXT | UNIQUE together |
| `approval_id` | INTEGER FK | |
| `imported_at` | TEXT | |

### 2.7 meta / schema_version

`meta(key TEXT PK, value TEXT)` — `agentguard_version`, `hooks_version`, `claude_version`, `claude_settings_path`, `last_backup_path`, `service_mode`, `engine_version`. `schema_version(version INTEGER PK, applied_at TEXT)`.

### 2.8 Indexes

`approvals(project_id, state)`; `audit_events(at)`; `audit_events(session_id, tool_use_id)`; `audit_events(project_id, at)`; `approval_events(approval_id, at)`; `agent_rule_imports(project_id, agent, rule_key, raw_command)` (unique).

---

## 3. Derived/computed values

| Value | Definition |
|---|---|
| `project_id` | `sha256(canonical W)` |
| `action_key` | sha256 over canonical JSON of `{project_id, engine_version, semantic_ops, targets(display, sorted), effects(type,scope,flags sorted), network(sorted), fingerprints(sorted key→value)}` |
| fingerprint value | sha256 of content with `\r\n`→`\n` normalization for text; for aggregate keys (`gradle-config`, `maven-config`) sha256 over sorted `(rel path, sha256)` pairs |
| `rule_key` | `<scope>:<toolName>(<ruleContent>)` where scope ∈ `policy\|user\|project\|local` |

---

## 4. Matching semantics (reference)

An ACTIVE approval `A` matches action `X` iff: engine major equal; `A.semantic_ops == X.semantic_ops`; ∀ fingerprint in `A`: present in `X` with equal value; EXACT: `set(X.effects) == set(A.envelope)` and `set(X.targets.display) == set(A.targets)` and `set(X.network) == set(A.network)`; SEMANTIC: `set(X.effects) ⊆ set(A.envelope)` and `set(X.network) ⊆ set(A.network)`; `X.status ∈ {RESOLVED, DECLARED}` and no `AMBIGUOUS` target. Candidates ordered EXACT→SEMANTIC then ascending id; first match wins.
