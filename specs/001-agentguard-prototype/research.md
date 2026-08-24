# Research: AgentGuard Prototype (Phase 0)

**Feature**: `001-agentguard-prototype` · **Date**: 2026-08-16 · **Inputs**: `spec.md`, `PROTOTYPE_SPEC.md` (Appendix B open questions), Claude Code docs (hooks, permissions) and the installed Claude Code build 2.1.233 (string/logic inspection).

Every item below is recorded as **Decision / Rationale / Alternatives considered**. Items marked **[verified]** were checked against documentation or the installed build; items marked **[decision]** are choices with no external unknown.

---

## R-01 Language, toolchain, module

- **Decision**: Go ≥ 1.22, `CGO_ENABLED=0` for all six targets, module path `github.com/agentguard/agentguard` (placeholder — rename freely before first tag), single `cmd/agentguard` main.
- **Rationale**: Single static binary, cross-compilation, `log/slog`, strong process/IPC support (brief §4).
- **Alternatives**: Rust (heavier ramp-up, no requirement), Node/Python (violates "no runtime" install requirement).

## R-02 SQLite driver

- **Decision**: `modernc.org/sqlite` (pure Go, `database/sql` driver). WAL, `busy_timeout=5000`, `foreign_keys=ON`, `synchronous=NORMAL`.
- **Rationale**: No CGO → trivial cross-compilation for darwin/linux/windows × arm64/amd64; mature and widely used.
- **Alternatives**: `mattn/go-sqlite3` (CGO; breaks cross-compilation simplicity), `ncruces/go-sqlite3` (WASM/wazero based; viable but larger runtime), `zombiezen.com/go/sqlite` (different API, also built on modernc's port).

## R-03 POSIX shell parsing

- **Decision**: `mvdan.cc/sh/v3/syntax` in *parse-only* mode (`syntax.NewParser`, bash/POSIX variants); AgentGuard walks the AST and **whitelists** node types (§14.2 of the spec). Any non-whitelisted node → `UNSUPPORTED_SYNTAX`.
- **Rationale**: Robust, well-tested bash/zsh-compatible parser; handling quoting/heredocs/operators by hand is error-prone and exactly the "naive prefix check" trap the brief forbids. We never call the interpreter package.
- **Alternatives**: hand-written tokenizer (rejected: correctness risk), executing `bash -n` (rejected: never execute shells to analyze).

## R-04 PowerShell and cmd.exe parsing

- **Decision**: Hand-written minimal tokenizers/parsers in `internal/parser/powershell` and `internal/parser/cmd` covering only §14.2 constructs; everything else → `UNSUPPORTED_SYNTAX`.
- **Rationale**: No mature Go libraries exist; the required subset (simple commands, quoting, `;`, `&&`, `||`, `|`, `&` for cmd, redirections, `$env:`/`%VAR%` expansion, cmdlet named parameters/aliases) is small and testable table-driven. Claude Code itself parses the PowerShell AST for rules; we do not need parity, only conservative parsing.
- **Alternatives**: embedding PowerShell to parse (rejected: executes an interpreter), tree-sitter bindings (CGO).

## R-05 Windows named pipes

- **Decision**: `github.com/Microsoft/go-winio` for pipe listener/dialer with a security descriptor limited to the current user; behind the `ipc.Transport` abstraction.
- **Rationale**: De-facto standard, used by Docker/containerd; supports SDDL-based ACLs.
- **Alternatives**: raw `golang.org/x/sys/windows` (more code), TCP localhost (rejected by brief).

## R-06 CLI framework

- **Decision**: `github.com/spf13/cobra` for sub-commands/help; `--json` flags on list/show commands.
- **Rationale**: Familiar UX (`agentguard approval show <id>`), completions for free, no impact on daemon internals.
- **Alternatives**: standard-library `flag` with a small dispatcher (acceptable if dependency count must be minimal; keep the command surface in one package so swapping is cheap).

## R-07 Configuration format and library

- **Decision**: TOML at `<ConfigDir>/config.toml`, parsed with `github.com/BurntSushi/toml`; every key optional; unknown keys warn.
- **Rationale**: Human-readable, comments allowed, simple types; brief allows YAML or TOML.
- **Alternatives**: YAML (`gopkg.in/yaml.v3`, more surprising typing rules), JSON (no comments).

## R-08 Logging

- **Decision**: `log/slog` JSON handler → `<DataDir>/logs/daemon.log` with size rotation via `gopkg.in/natefinch/lumberjack.v2` (10 MiB × 5 files); text handler to stderr for CLI commands; hook client logs only to file (stdout is the protocol channel).
- **Rationale**: Structured logs required by brief; lumberjack is tiny and standard. In-house rotation is acceptable if the dependency is unwanted.
- **Alternatives**: zap/zerolog (unnecessary).

## R-09 Per-user service management

- **Decision**: Thin per-OS implementations behind `platform.ServiceManager`: macOS LaunchAgent plist (`RunAtLoad`, `KeepAlive`, `launchctl bootstrap gui/<uid>` / `bootout`); Linux `systemd --user` unit (`Restart=on-failure`, `systemctl --user enable --now`), fallback *unmanaged* mode (detached process + pid file); Windows `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` value running `agentguard.exe daemon start`, which spawns `daemon run` detached with a hidden window (`CREATE_NO_WINDOW`), plus hook-side lazy start (§9.5). Task Scheduler is *not* used in the prototype.
- **Rationale**: Each mechanism is a few dozen lines; no elevation is ever required; `KeepAlive`/`Restart` give supervision where available; lazy start covers Windows and unmanaged Linux. `schtasks /SC ONLOGON` behaviour for standard users is inconsistent across Windows editions, so it was dropped from the prototype scope.
- **Alternatives**: `github.com/kardianos/service` (Windows path installs a real Service → needs admin; user-mode support uneven), Task Scheduler (deferred), Windows Service (rejected: elevation).

## R-10 Claude Code hook behavior **[verified against build 2.1.233 and docs, 2026-08-16]**

Resolves Appendix B-1, B-2, B-3, B-6, B-9, B-11 of `PROTOTYPE_SPEC.md`.

| Question | Finding | Evidence |
|---|---|---|
| B-1 Does hook `cwd` track the Bash tool's persistent `cd`? | **Yes.** After every Bash command the tool reads the shell's resulting cwd from a temp file and, if changed, calls the global `setCwd`; hook common fields are built from that same cwd accessor. | Bash tool post-exec cwd sync (`sC(...)→setCwd`), hook input builder `my(session, Yt(), …)`. |
| B-2 Does a PreToolUse `ask` force the prompt even when an allow rule matches? | **Yes.** For a hook `ask`, Claude checks only deny/ask rules; if none match, `hasPermissionsToUseTool` returns the hook's decision unchanged, so allow rules are not consulted and the dialog is shown. | permission decision function: `if hook==="ask" → o(..., hookResult)`; `PP=(…,i)=>{if(i)return i}`. |
| B-3 After a hook-forced `ask`, does the dialog offer "don't ask again"? | **No.** The Bash dialog adds the persistent row (`yes-prefix-edited` / `yes-apply-suggestions`) only when `permissionResult.suggestions` is non-empty; hook-produced decisions carry no suggestions, so the dialog offers **Yes / No** only. | Bash dialog options builder (`hJh`): both persistent rows are gated on `suggestions.length>0`. |
| B-6 Are Claude `permissions.deny`/`ask` rules enforced over a hook `allow`? | **Yes.** A matching deny rule overrides ("Hook returned 'allow' … but deny rule overrides"); a matching ask rule sends the call through the full permission pipeline (prompt). | permission decision function (`zDb`). |
| B-9 Subagent tool calls | Hooks receive `agent_id`/`agent_type` in common fields; treated like the main session. | common-fields builder. |
| B-11 `permissionDecision: "defer"` | Print-mode (`-p`) only: "ignoring (defer is print-mode only)" in interactive sessions. **Not used by AgentGuard.** | PreToolUse result handling. |
| Multiple hooks precedence | deny > defer > ask > allow > passthrough. | hook aggregation. |
| `systemMessage` | Any hook may return `systemMessage`; it is rendered to the user as a hook system message. | JSON output handling. |
| PermissionRequest input | Includes `permission_suggestions`; no `tool_use_id`. Output `decision.behavior` allow (`updatedInput`, `updatedPermissions`) / deny (`message`, `interrupt`). | schemas in build. |
| Exit codes | Exit 1/other → tool proceeds; only exit 2 or JSON `deny` blocks; JSON output is parsed on exit 0. | docs. |
| Built-in read-only commands | `ls cat echo pwd head tail grep find wc which diff stat du cd` and read-only `git` forms run **without prompting in every mode**; unparseable or >10k-char commands prompt. | permissions docs. |
| Compound commands & wrappers in rules | Rules must match each subcommand (`&&`, `\|\|`, `;`, `\|`, `\|&`, `&`, newline); `timeout/time/nice/nohup/stdbuf/command/builtin/noglob` and known-safe leading env assignments are stripped; bare `xargs` stripped; "don't ask again" on a compound saves one rule per subcommand (≤5). | permissions docs. |
| Where "don't ask again" writes | `.claude/settings.local.json` at the **git repository root** (worktrees resolved to the main checkout); "Yes, and don't ask again for `<prefix> *` commands" writes the space-wildcard form. | permissions docs. |
| Bash rule grammar | `Bash(cmd)` exact; `*` matches any sequence including spaces, at any position; trailing ` *` enforces a word boundary (`ls *` ≠ `lsof`); `:*` suffix ≡ trailing ` *`; `Bash(*)` ≡ `Bash`. | permissions docs. |

**Design decisions derived from these findings** (applied to `PROTOTYPE_SPEC.md` §11.3, §11.6, §19.5, Appendix B/C):

1. **Adapter mapping for ASK** — for an understood action with **no related approval** (`NO_MATCHING_APPROVAL`) the adapter **defers** (no `permissionDecision`) and attaches a `systemMessage` with AgentGuard's resolution summary, so Claude's *native* dialog — which offers "don't ask again" — is what the user sees. For `APPROVAL_MISMATCH` and `POLICY_REQUIRES_CONFIRMATION` the adapter emits `permissionDecision: "ask"` (forced prompt with the explanation; Yes/No only). BLOCK → `deny`; ALLOW → `allow`.
2. **Consent import** is triggered (a) at `PostToolUse`, when the adapter observes that a Claude allow rule covering the raw command now exists and the evaluation for that `tool_use_id` was ASK/understood, and (b) at the next `PreToolUse` as a fallback — both idempotent through `agent_rule_imports`. This makes "the user creates a persistent approval through the intended (native) permission flow" work end-to-end.
3. **Conflict avoidance is unnecessary for safety** (Claude enforces deny/ask rules over hook allow); the adapter keeps a Bash-rule matcher only for consent detection. `AGENT_RULE_CONFLICT` becomes an optional, informational class.
4. **Read-only baseline B1 aligns with Claude's built-in read-only set** and is *stricter* for sensitive paths (`cat ~/.ssh/id_rsa` → forced ask under R5, whereas Claude alone would not prompt).
5. `agentguard approve <event-id>` remains the persistent path for re-consent after invalidation (Yes/No dialog) and for non-interactive use.

## R-11 Claude settings discovery for the adapter **[verified: docs]**

- **Decision**: Read allow/deny/ask rules from managed policy settings, `~/.claude/settings.json`, `<git root>/.claude/settings.json`, `<git root>/.claude/settings.local.json` (git root = nearest `.git` ancestor of `CLAUDE_PROJECT_DIR`, worktree-resolved), re-read on mtime/size change. Implement matcher: split raw command on Claude's separators, strip Claude's wrapper list and known-safe leading assignments, then match each subcommand against rule patterns with `*` glob semantics and word-boundary handling. **Uncertain match → no consent** (import only), never used to allow.
- **Rationale**: Consent detection must mirror what Claude actually persisted; false positives could import an approval the user did not intend (still validated by resolution and policy, but avoid anyway).
- **Alternatives**: parsing the transcript (fragile), asking Claude via PermissionRequest `updatedPermissions` (creates string rules — rejected).

## R-12 IPC framing and discovery **[decision]**

- **Decision**: One JSON object + `\n` per direction per connection; 1 MiB cap; envelope `{protocol_version, request_id, method, params}` / `{protocol_version, request_id, ok, result|error}`; endpoint discovery `AGENTGUARD_ENDPOINT` → `daemon.json` → platform default; UDS 0600 in 0700 dir with peer-UID check; pipe ACL current user only.
- **Rationale**: Simplest versionable protocol; no HTTP stack; per-connection framing avoids multiplexing bugs in a hook that lives for milliseconds.
- **Alternatives**: gRPC over UDS (heavier), length-prefixed frames (no benefit for one-shot requests).

## R-13 Canonical hashing **[decision]**

- **Decision**: SHA-256 everywhere (fingerprints, `action_key`, `project_id`); `action_key` computed over a canonical JSON encoding (sorted keys, no whitespace) of the fields listed in `PROTOTYPE_SPEC.md` §20.2; fingerprint values are hex SHA-256 of normalized content (line endings normalized to `\n` for text inputs so Windows checkouts and Unix checkouts agree).
- **Rationale**: Deterministic across OSes; cheap.

## R-14 Path canonicalization details **[decision]**

- **Decision**: `filepath.EvalSymlinks` on the longest existing prefix (resolves Windows symlinks and junctions), lexical clean of the non-existing suffix, case-insensitive prefix comparison on macOS/Windows via `strings.EqualFold` on cleaned absolute paths (and `os.SameFile` when both exist), MSYS `/c/…` → `C:\…` rewriting on Windows, UNC detection via `\\?\`/`\\server\share` prefixes.
- **Rationale**: Covers §16.1 requirements with the standard library; junction resolution is what makes S9 meaningful on Windows.

## R-15 Windows console handling for the daemon **[decision]**

- **Decision**: Keep a normal console subsystem binary (CLI needs a console); `daemon start` spawns `daemon run` with `SysProcAttr{HideWindow: true, CreationFlags: CREATE_NO_WINDOW | DETACHED_PROCESS}` and redirects stdio to the log file.
- **Rationale**: One binary for CLI + daemon (P10) without a GUI-subsystem build.

## R-16 Minimum supported Claude Code version **[decision]**

- **Decision**: Reference version 2.1.233 (verified). Setup warns (does not fail) below `2.1.0`; below `2.0.45` (introduction of the PermissionRequest hook) it prints an explicit "unsupported" warning and installs PreToolUse/PostToolUse only.
- **Rationale**: Feature availability verified only for the reference build; failing setup on older versions would block users unnecessarily while PreToolUse still provides the safety gate.

## R-17 Testing & CI **[decision]**

- **Decision**: `go test ./... -race` (race on Linux/macOS; plain on Windows), `golangci-lint` (govet, staticcheck, errcheck, gosimple, ineffassign, unused, gofmt/goimports), GitHub Actions matrix `ubuntu-latest / macos-latest / windows-latest`, GoReleaser snapshot build of all six targets on every push, real release only on tags. E2E tests drive the built binary via `os/exec` with `AGENTGUARD_*` env overrides and temp dirs; Windows symlink cases skip with a message if `os.Symlink` fails with privilege errors, junction cases always run (`mklink /J` via `cmd.exe` in tests only).
- **Rationale**: Brief §33; race detector catches daemon concurrency bugs; snapshot builds catch cross-compile breakage early.

## R-18 Distribution scaffolding **[decision]**

- **Decision**: GoReleaser config with archives per target, Homebrew tap and winget manifest templates checked in but not published; Linux install script (`install.sh`) that downloads the release asset and prints the `agentguard setup claude` next step.
- **Rationale**: Architecture must support the target UX without requiring the repositories to exist (brief §5).

## R-19 Project identity **[decision]**

- **Decision**: `project_id = sha256(canonical workspace root)`; git remote URL stored as metadata only.
- **Rationale**: Deterministic and cheap; approvals are per checkout, which is the safer default (a clone elsewhere re-approves).
- **Alternatives**: remote-URL identity (approvals leak between clones/forks), root-commit hash (requires object parsing).

## R-20 Open items intentionally left to implementation (with fail-safe defaults)

| Item | Default until resolved |
|---|---|
| Whether `CLAUDE_PROJECT_DIR` differs from the git root in worktrees | Adapter searches both `CLAUDE_PROJECT_DIR` and the git root for `.claude/settings*.json`. |
| Behavior of hook `systemMessage` rendering inside the permission dialog vs transcript | Treated as transcript notice; the forced-`ask` reason (which *is* shown in the dialog) is used for mismatch/policy prompts. |
| Exact set of "known-safe env assignments" Claude strips | Matcher treats *any* leading assignment as blocking a consent match (conservative). |
| macOS notarization / Windows SmartScreen | Documented in README; not part of prototype DoD. |
