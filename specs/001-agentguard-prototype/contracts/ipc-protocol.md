# Contract: Daemon IPC Protocol (v1)

Transport: Unix domain socket (macOS/Linux) or named pipe (Windows). One request and one response per connection, each a single JSON object terminated by `\n`, ≤ 1 MiB. Client timeouts: connect 1 s (+ ≤ 2 s lazy start), request 5 s.

## Envelope

Request
```json
{"protocol_version": 1, "request_id": "<uuid>", "method": "<name>", "params": {}}
```
Response (success)
```json
{"protocol_version": 1, "request_id": "<uuid>", "ok": true, "result": {}}
```
Response (error)
```json
{"protocol_version": 1, "request_id": "<uuid>", "ok": false, "error": {"code": "UNSUPPORTED_PROTOCOL|BAD_REQUEST|NOT_FOUND|INTERNAL|BUSY", "message": "..."}}
```

Unknown `method` → `BAD_REQUEST`. Unsupported `protocol_version` → `UNSUPPORTED_PROTOCOL` (client treats as daemon failure).

## Methods

### `ping`
- params: `{}`
- result: `{"version": "0.1.0", "engine_version": 1, "protocol_version": 1, "uptime_s": 123, "pid": 4242}`

### `evaluate`
- params: `{"dry_run": false, "request": ActionRequest}` — `ActionRequest` per `data-model.md` §1.1:
```json
{
  "agent": "claude", "agent_version": "2.1.233",
  "session_id": "abc123", "tool_use_id": "toolu_01…",
  "tool": "Bash", "dialect": "posix",
  "raw_command": "npm run cleanup",
  "cwd": "/Users/u/proj", "project_hint": "/Users/u/proj",
  "agent_consent": null,
  "adapter_context": {"permission_mode": "default", "hook_event": "PreToolUse"}
}
```
- result (`EvaluationResult`):
```json
{
  "audit_event_id": 1207,
  "decision": "allow|ask|block",
  "class": "APPROVAL_MATCH",
  "reason": "matched approval 42: …",
  "approval_id": 42,
  "hard_rule": null,
  "mismatch_reports": [{"approval_id": 42, "differences": ["fingerprint npm-script:package.json#scripts.cleanup changed", "target ./dist -> ~/Documents", "scope WORKSPACE_GENERATED -> HOME"]}],
  "resolution_status": "RESOLVED|DECLARED|UNRESOLVED|PARSE_FAILED|CONTEXT_FAILED",
  "explanation": ["resolved: npm run cleanup -> rm -rf ./dist", "targets: ./dist [WORKSPACE_GENERATED]", "effects: DELETE(recursive,force) WORKSPACE_GENERATED"],
  "user_message": "AgentGuard: …",
  "imported_approval_id": null
}
```
- `dry_run: true` → no audit row, no approval creation/import, `audit_event_id` absent.

### `record_prompt`
- params: `{"agent": "claude", "session_id": "…", "tool": "Bash", "raw_command": "…", "suggestions": [ {"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"npm run cleanup"}],"behavior":"allow","destination":"localSettings"} ]}`
- result: `{"audit_event_id": 1207}` (correlated by session_id + raw_command within 60 s, else a new row with `hook_event=PermissionRequest`)

### `record_adapter_action`
- params: `{"audit_event_id": 1207, "agent": "claude", "action": "allow|deny|prompt|defer"}`
- result: `{}`
- Records what the adapter emitted to the agent after mapping the decision (§11.3), on the `adapter_action` column of §23.2. The decision and its delivery are separate facts: a never-approved understood action is ASK and is *deferred* to the agent's own dialog, while an approval mismatch is also ASK and *forces* the prompt. Without this, the audit log cannot tell a user why one "ask" produced a prompt and the other produced nothing.
- Called by the hook **after** its response has been written, so a slow or unreachable daemon delays only the annotation. The adapter MUST treat every failure as non-fatal (INVARIANT I-12), and MUST NOT call it for a `dry_run` evaluation, which has no audit row.
- `action` is validated against the four documented values; anything else is `BAD_REQUEST`. An `audit_event_id` that names no row is `NOT_FOUND`.

### `report_execution`
- params: `{"agent":"claude","session_id":"…","tool_use_id":"toolu_01…","status":"completed|failed|unknown","response_summary":"…≤1KiB","agent_consent": {"kind":"persistent_rule","rule_keys":["local:Bash(npm run cleanup)"],"exact":true} }`
- result: `{"imported_approval_id": 43}` (null when no import happened)

### `agent_config_changed` (optional hook)
- params: `{"agent":"claude","source":"local_settings","file_path":"…"}` → result `{}`

### `list_approvals`
- params: `{"project_id": "…"|null, "include_inactive": false, "limit": 200}`
- result: `[{"id":42,"kind":"EXACT","semantic_ops":["RUN_SCRIPT","FS_DELETE"],"summary":"DELETE ./dist [WORKSPACE_GENERATED]","project_root":"/Users/u/proj","use_count":7,"last_used_at":"…","state":"ACTIVE","origin":"claude_rule","created_at":"…"}]`

### `get_approval`
- params: `{"id": 42}` → result: full `Approval` (data-model §2.2) + `conditions` + `recent_events` (≤10) ; `NOT_FOUND` if missing.

### `set_approval_state`
- params: `{"id": 42, "state": "DISABLED|ACTIVE|REVOKED"}` → result: updated `Approval`. Rules: `REVOKED` is terminal (`BAD_REQUEST` on further changes).

### `create_approval`
- params: `{"audit_event_id": 1207, "kind": "EXACT|SEMANTIC", "note": "…"}`
- result: created `Approval`; errors: `NOT_FOUND` (event), `BAD_REQUEST` with message when the event is not approvable (status not RESOLVED/DECLARED, hard outcome BLOCK/ASK_ALWAYS, ambiguous target).

### `list_history`
- params: `{"decision": "allow|ask|block"|null, "project_id": null, "session_id": null, "since": "<RFC3339>"|null, "limit": 100}`
- result: `[{"id":1207,"at":"…","decision":"block","class":"HARD_RULE_R2","raw_command":"npm run cleanup","resolved_summary":"rm -rf ~/Documents","reason":"…","approval_id":null,"adapter_action":"deny"}]`

### `get_history_event`
- params: `{"id": 1207}` → result: full `AuditEvent` (data-model §2.5) including `resolved`, `mismatch_report`, `explanation`.

### `status`
- result: `{"daemon": {"version":"…","uptime_s":…,"endpoint":"…","db_path":"…","service_mode":"managed|unmanaged"}, "counts": {"approvals": {"active":…,"disabled":…,"revoked":…}, "events_24h": {"allow":…,"ask":…,"block":…}}, "integration": {"claude": {"hooks_installed": true, "settings_path":"…","claude_version":"…"}}}`

### `shutdown`
- result `{}`; daemon exits after in-flight requests (≤ 2 s).

## Versioning
`protocol_version` is bumped only for incompatible changes; additive optional fields and new methods do not bump. Clients MUST ignore unknown result fields. A method a daemon does not implement answers `NOT_FOUND`, so a caller that adds one MUST tolerate that reply — which is why `record_adapter_action` is best effort on the adapter side.

### Optional `client_version` (added by feature 002)

The request envelope carries an optional `client_version` — the release the caller was built from — alongside `protocol_version`. It is additive and the protocol stays v1; a daemon that does not read it is unaffected, and a client that does not send it is treated as saying nothing.

It exists because an upgrade replaces the binary while the daemon is still running the previous one, and nothing else tells the daemon that happened. When the value is a strictly newer semver than the daemon's own **and** the daemon's own executable has been replaced on disk since it started, the daemon serves the request normally, logs `newer client detected; restarting`, and exits with code `75` once in-flight requests finish; the service manager starts it again on the new binary.

The second condition is not in the original design and is required: without it, a newer binary installed *elsewhere* on `PATH` would make every request restart the daemon into the same old code, so the gate would spend its life starting up. When the executable is unchanged the daemon logs the mismatch once and keeps serving, and `agentguard doctor` reports it with the command that fixes it.
