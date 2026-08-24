# Contract: Claude Code Hook Integration

Entry point: `agentguard hook claude` — reads one JSON object from stdin, writes at most one JSON object to stdout, **always exits 0**. Verified against Claude Code 2.1.233 (research R-10).

## Settings entries written by `agentguard setup claude` (user scope `~/.claude/settings.json`)

macOS/Linux (shell form):
```json
{
  "hooks": {
    "PreToolUse":        [{"matcher": "Bash|PowerShell", "hooks": [{"type": "command", "command": "\"/abs/path/agentguard\" hook claude", "timeout": 10}]}],
    "PermissionRequest": [{"matcher": "Bash|PowerShell", "hooks": [{"type": "command", "command": "\"/abs/path/agentguard\" hook claude", "timeout": 10}]}],
    "PostToolUse":       [{"matcher": "Bash|PowerShell", "hooks": [{"type": "command", "command": "\"/abs/path/agentguard\" hook claude", "timeout": 10}]}]
  }
}
```
Windows (exec form; avoids Git-Bash/PowerShell quoting divergence):
```json
{"type": "command", "command": "C:\\Users\\u\\AppData\\Local\\AgentGuard\\bin\\agentguard.exe", "args": ["hook", "claude"], "timeout": 10}
```
Optional (`claude.hook_config_change=true`): `ConfigChange` with matcher `user_settings|project_settings|local_settings`.
Ownership rule: an entry is AgentGuard-owned iff its `command` resolves to the AgentGuard executable and its arguments are `hook claude`. Setup replaces stale owned entries and never touches other entries. Existing sessions must be restarted (hooks are snapshotted at session start).

## Input (stdin) — fields consumed

| Event | Fields used |
|---|---|
| all | `hook_event_name`, `session_id`, `cwd`, `permission_mode`, `tool_name`, `tool_input.command` |
| `PreToolUse` | `tool_use_id` |
| `PermissionRequest` | `permission_suggestions` (verbatim, optional) |
| `PostToolUse` | `tool_use_id`, `tool_response` (summarized ≤ 1 KiB) |
| env | `CLAUDE_PROJECT_DIR` (project hint and settings discovery) |

Non-`Bash`/`PowerShell` tools or unknown events → exit 0, no output.

## Output (stdout) by event and daemon outcome

### PreToolUse

| Daemon outcome / class | Interactive modes | `bypassPermissions` |
|---|---|---|
| ALLOW | `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"AgentGuard [event N]: <reason>"}}` | same |
| BLOCK | `{"systemMessage":"AgentGuard BLOCK [event N]: <reason>","hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"AgentGuard BLOCK [event N]: <reason>"}}` | same |
| ASK / `NO_MATCHING_APPROVAL` | `{"systemMessage":"AgentGuard [event N]: <resolution summary>; no approval yet — Claude will ask. To approve permanently: agentguard approve N"}` (**no** `permissionDecision` → Claude's native dialog with "don't ask again") | no output |
| ASK / `APPROVAL_MISMATCH`, `POLICY_REQUIRES_CONFIRMATION` | `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":"AgentGuard [event N]: <reason>. To approve permanently: agentguard approve N"}}` | no output |
| ASK / other classes (`UNRESOLVED_COMMAND`, `UNSUPPORTED_SYNTAX`, `AMBIGUOUS_PATH`, `CONTEXT_UNAVAILABLE`, `AGENT_RULE_CONFLICT`, `ENGINE_ERROR`) | no output (defer) | no output |
| daemon unreachable / timeout / protocol error | `{"systemMessage":"AgentGuard: daemon unavailable — native permissions in effect (agentguard daemon status)"}` at most once per session per hour, else no output | same |

Never: exit code ≠ 0, `permissionDecision: "defer"`, `updatedInput`, `updatedPermissions`.

### PermissionRequest

Evaluate (cached by `session_id`+`raw_command` ≤ 60 s) and `record_prompt`; then:
- ALLOW → `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`
- BLOCK → `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":"AgentGuard BLOCK [event N]: <reason>","interrupt":false}}}`
- ASK → no output (dialog proceeds)

### PostToolUse
`report_execution` with `tool_use_id`, `status`, `response_summary`, freshly computed `agent_consent`. No output.

## Consent detection (adapter → `agent_consent`)

Sources (in order): managed policy settings, `~/.claude/settings.json`, `<git root>/.claude/settings.json`, `<git root>/.claude/settings.local.json` (git root = nearest `.git` ancestor of `CLAUDE_PROJECT_DIR`). Only `permissions.allow` entries for the tool. Matching implements Claude's grammar: exact; `*` any sequence incl. spaces at any position; trailing ` *` word boundary; `:*` ≡ ` *`; bare `Bash`/`Bash(*)`; command split on `&&`, `||`, `;`, `|`, `|&`, `&`, newline with every subcommand required to match; wrappers `timeout time nice nohup stdbuf command builtin noglob` and bare `xargs` stripped; any leading env assignment ⇒ uncertain ⇒ no consent. Result: `{"kind":"persistent_rule","rule_keys":[...],"exact":true|false}` or `null`.

## Failure behavior
Any adapter-side error (bad JSON, daemon down, timeout, panic recovered) ⇒ defer (exit 0; optional `systemMessage`). Errors are logged to `<DataDir>/logs/hook.log`.
