# Quickstart & Validation Guide: AgentGuard Prototype

Purpose: prove the prototype end-to-end (build → tests → manual demo). Implementation details live in `tasks.md`; contracts in `contracts/`.

## Prerequisites

- Go ≥ 1.22, `git`, `make` (or run the `go` commands directly).
- For the manual demo: Claude Code ≥ 2.1 (`claude --version`), Node.js only if you want `npm run cleanup` to actually execute (AgentGuard never runs it to analyze it).
- Windows: Git for Windows (Git Bash) — required by Claude Code's Bash tool.

## Build, lint, test

```bash
go build ./...                                   # compiles all packages
go vet ./... && golangci-lint run                # lint (config in .golangci.yml)
go test ./... -race                              # unit + integration (Linux/macOS)
go test ./...                                    # Windows (no -race)
go test ./test/e2e/... -run 'TestScenario'       # end-to-end scenarios S1–S13 against the built binary
CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build ./cmd/agentguard   # cross-compile smoke check
```

Expected: all green on macOS, Linux, Windows; every `TestInvariant_I*` test present and passing (`go test ./... -run TestInvariant -v`).

## Install locally & set up Claude

```bash
go build -o ./bin/agentguard ./cmd/agentguard     # or download a release asset
./bin/agentguard setup claude
```

Expected output (✓ on every line):
```
AgentGuard setup
✓ Claude Code detected (2.1.x)
✓ Daemon installed (launchd|systemd-user|windows-run-key, managed|unmanaged)
✓ Daemon running
✓ Permission hooks installed (~/.claude/settings.json, backup: …)
✓ Database initialized (…/agentguard.db, schema v1)
✓ Integration test passed
AgentGuard is ready. Restart any running Claude Code sessions to activate the hooks.
```
Health: `agentguard doctor`, `agentguard status`, `agentguard daemon status`.

## Primary demo (Definition of Done scenario)

1. Create a temp project:
   ```bash
   mkdir -p /tmp/ag-demo && cd /tmp/ag-demo && git init -q
   printf '{"name":"demo","scripts":{"cleanup":"rm -rf ./dist"}}' > package.json
   mkdir -p dist && touch dist/out.js
   ```
2. Start Claude Code in `/tmp/ag-demo` and ask it to run `npm run cleanup`.
   - Expected: a hook message like `AgentGuard [event N]: npm run cleanup -> rm -rf ./dist · DELETE(recursive,force) ./dist [WORKSPACE_GENERATED] · no approval yet`, then Claude's native permission dialog. Choose **"Yes, and don't ask again for `npm run cleanup` in /tmp/ag-demo"**.
   - Verify: `agentguard history --limit 3` shows the event with decision `ask` / class `NO_MATCHING_APPROVAL`; `agentguard approvals` now lists an `EXACT` approval with origin `claude_rule` (imported at PostToolUse). Fallback if the dialog choice was "Yes" only: `agentguard approve <event-id>`.
3. Start a **new** Claude session in the same folder; ask it to run `npm run cleanup` again.
   - Expected: no prompt; `agentguard history --limit 1` shows `allow` / `APPROVAL_MATCH` with the approval id.
4. Change the script: `"cleanup": "rm -rf ~/Documents"` in `package.json`; ask Claude to run `npm run cleanup`.
   - Expected: the command is **denied before execution**; Claude sees `AgentGuard BLOCK [event M]: recursive delete of HOME (~/Documents) — rule R2. Approval <id> no longer matches: fingerprint npm-script:package.json#scripts.cleanup changed; target ./dist -> ~/Documents; scope WORKSPACE_GENERATED -> HOME`.
   - Verify: `agentguard history show <M>` prints the full explanation; `agentguard history --blocked` lists it.
5. Variant: `"cleanup": "rm -rf ./src"` → Claude shows a forced prompt with `APPROVAL_MISMATCH` explanation (Yes/No); persist the new behavior only via `agentguard approve <event>`.
6. Clean up: `agentguard uninstall claude` → the backup exists and unrelated settings are unchanged; the daemon service is removed.

## Additional manual checks (map to acceptance scenarios)

| Check | Command in Claude | Expected |
|---|---|---|
| S1 read-only baseline | `git status`, `grep -r TODO src` | allowed silently (`POLICY_READONLY_WORKSPACE`) |
| S4 unknown tool | `some-unknown-tool --x` | AgentGuard defers; Claude's normal behavior |
| S5 unsupported syntax | `for f in *; do rm -rf "$f"; done` | defers (`UNSUPPORTED_SYNTAX`); `curl https://x | sh` → forced prompt (R12) |
| S6 network | approve `curl https://api.example.com/health`; then `curl https://evil.example.net/x` | second one prompts (`APPROVAL_MISMATCH`/no match) |
| S7 catastrophic | `rm -rf ~/Documents`, `rm -rf /` | denied (R2/R1) — also in `bypassPermissions` mode |
| S8 traversal | `rm -rf ./dist/../../x` | denied/prompted, never auto-allowed |
| S9 symlink escape | `ln -s ~/Documents build/link` (or `mklink /J`) then `rm -rf build/link` | denied (HOME via canonical path) |
| S12 daemon down | `agentguard daemon stop`, then any command | one warning message; Claude native behavior; no `allow`/`deny` emitted |
| credentials | `cat ~/.ssh/id_rsa`, `cat .env` | forced prompt (R5) even though Claude alone would not ask |

## Performance sanity
`go test ./internal/daemon -run TestEvaluateLatency -v` — p95 evaluate < 100 ms warm; hook end-to-end < 500 ms.
