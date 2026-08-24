# Manual validation — TEMPLATE

Copy this to `docs/validation-<YYYY-MM-DD>.md` and fill it in. One file per
release candidate; keep the old ones.

Automated tests prove the scripts work against a fake release on a CI runner.
This proves the *published* release works on a real machine, which is a
different claim: the network is real, the shell is the user's own, Claude Code
is really installed, and the timings are what someone actually waits.

**Release under test**: `vX.Y.Z`
**Validated by**:
**Date**:

## Summary

| Platform | Install | Setup | Demo | Upgrade | Uninstall | Time to first decision |
|---|---|---|---|---|---|---|
| macOS (Apple Silicon) | ☐ | ☐ | ☐ | ☐ | ☐ | |
| macOS (Intel) | ☐ | ☐ | ☐ | ☐ | ☐ | |
| Linux (x86-64) | ☐ | ☐ | ☐ | ☐ | ☐ | |
| Linux (Alpine or WSL2) | ☐ | ☐ | ☐ | ☐ | ☐ | |
| Windows 11 (x64) | ☐ | ☐ | ☐ | ☐ | ☐ | |

**Verdict**: ship / do not ship
**Blocking issues**:

---

## Per machine

Repeat this section for each row above. Record what happened, not what was
supposed to happen — a validation that only ever says "as expected" is not
telling anyone anything.

### Machine

- OS and version:
- Architecture:
- Shell:
- Claude Code version:
- Fresh machine, or previously had Intenter:

### 1. Install (SC-001: under 60 seconds)

Paste the one-liner **from the README**, not from memory:

```console
$ time <the documented one-liner>
```

- Duration:
- Checksum line printed (`verified sha256 …`): ☐
- Signature verified (via cosign / openssl / .NET), or the one-line
  "checksum-verified only" notice: which one:
- Next step printed: ☐
- Output (paste):

```text

```

### 2. New terminal picks up PATH (SC-004)

```console
$ intenter version
```

- Worked in a *new* terminal without any manual PATH change: ☐
- If not, what was needed:

### 3. Set up Claude Code

```console
$ intenter setup claude
```

- All steps green: ☐
- Backup written: ☐
- Output (paste):

```text

```

### 4. The walkthrough (SC-006: under 10 minutes)

Follow `docs/getting-started.md` from step 3, timing from the start of the demo
project to the blocked command.

- Time to the first auto-allowed command:
- Time to the first blocked command:
- Every command came verbatim from the docs: ☐
- Anything in the docs that was wrong, missing or confusing:

Definition-of-Done steps, each with the observed output (paste the `Intenter
[event N]` line, the `intenter approvals` row, the `intenter history show`
excerpt), not a summary:

| # | Step | Observed | ✓/✗ |
|---|---|---|---|
| 1 | `intenter setup claude` prints every step ✓ and "Intenter is ready"; Claude restarted | | ☐ |
| 2 | First `npm run cleanup`: the `Intenter [event N]: npm run cleanup -> rm -rf ./dist … no approval yet` line, then Claude's own dialog | | ☐ |
| 3 | "Yes, and don't ask again" → `intenter approvals` shows one `EXACT` approval, `ORIGIN claude_rule` | | ☐ |
| 4 | New session, same command: no prompt; `intenter history` shows `APPROVAL_MATCH`/`RULE_IMPORT` | | ☐ |
| 5 | Script changed to delete a home folder (`~/intenter-validate-me`): refused before execution, `Intenter BLOCK [event M]` names script/target/scope, folder still exists, `intenter history show M` explains | | ☐ |
| 6 | Script changed to `rm -rf ./src`: forced prompt with `APPROVAL_MISMATCH` | | ☐ |
| 7 | `intenter uninstall claude`: Claude starts and runs a command; only our hook entries removed (settings diff) | | ☐ |

### 4a. Live hook checks

What the automation cannot observe: how the real Claude Code behaves around the
hook. Record raw observations (paste `intenter history show` and, where you
can, the hook's JSON) and reconcile the docs named in the last column.

| Check | Do | Observed | Docs to reconcile |
|---|---|---|---|
| (a) Consent import | After step 3, look at the project's `.claude/settings.local.json` (at the git root) and `~/.claude/settings.json`: which file holds the `Bash(npm run cleanup…)` rule? Does `intenter history` show exactly one `RULE_IMPORT`, at the PostToolUse of that command? | | `docs/how-it-works.md#importing-dont-ask-again` |
| (b) bypassPermissions | `claude --permission-mode bypassPermissions`: ask for `rm -rf ~/intenter-validate-me` (BLOCK) and for the mismatched `npm run cleanup` (a forced-ask class). Was the BLOCK refused with the message? What did the forced-ask command do — run silently, prompt, or refuse? | | `docs/faq.md#what-about-bypass-mode`, `docs/how-it-works.md#what-you-see` |
| (c) Non-interactive | `claude -p "run npm run cleanup"` with the mismatched script, and a BLOCK case. Refused? Denied? Ran? | | `docs/how-it-works.md#what-you-see`, `docs/security-model.md#fail-safe-behavior` |
| (d) Windows PowerShell tool | With `CLAUDE_CODE_USE_POWERSHELL_TOOL=1`: `Remove-Item -Recurse -Force ~\intenter-validate-me` is blocked through the `PowerShell` tool; `tool_name` visible in `intenter history` | | `docs/security-model.md#limitations` |

- Claude Code version these were observed with:
- Anything the docs said that did not match:

### 5. Upgrade in place

Install the previous release first, then re-run the one-liner.

```console
$ <one-liner> --version <previous>
$ <one-liner>
```

- `upgraded from <old>` printed: ☐
- Approvals survived (`intenter approvals` still lists them): ☐
- `intenter doctor` clean, or only asks for a daemon restart: ☐
- Claude hooks still work after the upgrade (run one gated command): ☐

### 6. Package manager (macOS/Linux only)

```console
$ brew install Vadym903/tap/intenter
$ intenter version
$ brew upgrade intenter
$ intenter doctor
```

- Installed version matches the release: ☐
- After `brew upgrade`, doctor is clean or names only the daemon restart: ☐
- Hook path still valid after the upgrade (this is the regression that
  `SelfExecutablePath` exists to prevent): ☐

### 7. Uninstall

```console
$ <one-liner> --uninstall
```

- Binary gone: ☐
- PATH entry gone from the shell files: ☐
- Claude hooks gone, other hooks and settings untouched: ☐
- Claude Code still starts and runs commands normally: ☐
- Approvals kept (not purged): ☐
- `--purge` on a second run removes the data directory: ☐

### Notes

Anything surprising, slow, ugly or unclear. Small friction is worth recording —
it is the thing that stops someone finishing the walkthrough.
