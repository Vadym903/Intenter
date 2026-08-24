# Getting started

Ten minutes, from an empty machine to watching Intenter block a command that
used to be approved — because the script behind it changed.

The walkthrough uses a throwaway project, so nothing here touches your real
work.

<!--
Blocks marked `<!-- smoke -->` are executed by test/e2e/docs_smoke_test.go
against the built binary in a temporary HOME with a `claude` shim on PATH.
Inside such a block, lines beginning with a `$` prompt are the commands; every other
line is illustrative output. The `<!-- expect: "…" -->` markers list substrings
that must appear in the combined output. Blocks marked `<!-- manual -->` need a
real Claude Code session and are not executed.
-->

- [Before you start](#before-you-start)
- [1. Install](#1-install)
- [2. Set up Claude Code](#2-set-up-claude-code)
- [3. Make a demo project](#3-make-a-demo-project)
- [4. First run: approve it once](#4-first-run-approve-it-once)
- [5. Second run: no prompt](#5-second-run-no-prompt)
- [6. Change the script: blocked](#6-change-the-script-blocked)
- [7. Read the decision log](#7-read-the-decision-log)
- [8. Clean up](#8-clean-up)

## Before you start

- **Claude Code 2.1 or newer** — `claude --version`. It is the only agent
  integrated so far.
- **A terminal you can restart.** Both the installer's `PATH` change and
  Claude's hooks are read at startup, so a couple of restarts are unavoidable.
- **Node.js** is optional. The demo uses `npm run cleanup` as its example, but
  Intenter reads `package.json` to resolve the script — it never runs it in
  order to analyze it. You only need Node if you want the command to actually
  execute after being allowed.
- **Windows:** Git for Windows, because Claude Code's `Bash` tool uses Git Bash.

## 1. Install

The one-liner for your platform is in [install.md](install.md); the short
version is:

```sh
curl -fsSL https://raw.githubusercontent.com/Vadym903/Intenter/main/install.sh | sh
```

```powershell
irm https://raw.githubusercontent.com/Vadym903/Intenter/main/install.ps1 | iex
```

Then **open a new terminal** and confirm the binary is on your `PATH`:

<!-- smoke -->
<!-- expect: "intenter" -->
<!-- expect: "engine" -->

```console
$ intenter version
intenter 0.1.0
  engine   v1
  protocol v1
  schema   v1
  built    go1.22.5 (darwin/arm64)
```

If the command is not found, the `PATH` entry has not reached this shell yet —
see [troubleshooting](troubleshooting.md#intenter-command-not-found).

## 2. Set up Claude Code

One command wires everything together:

```console
$ intenter setup claude
Intenter setup

✓ Claude Code detected (2.1.x)
✓ Daemon installed (launchd, managed)
✓ Daemon running
✓ Permission hooks installed (~/.claude/settings.json, backup: ~/.claude/settings.json.intenter-backup)
✓ Database initialized (~/.local/share/intenter/intenter.db, schema v1)
✓ Integration test passed

Intenter is ready. Restart any running Claude Code sessions to activate
the hooks — Claude reads them once, when a session starts.
```

What it did, line by line:

- **Detected Claude Code** and found its settings file.
- **Installed the daemon** as a user service — launchd on macOS, a systemd user
  unit on Linux, a run key on Windows — so it starts with your session. There is
  no root involved and no system-wide service.
- **Added its hooks** to `~/.claude/settings.json`, after backing the file up.
  Hooks you already had are kept; Intenter adds itself alongside them.
- **Created the database** that stores approvals and history, locally.
- **Ran an integration test** — a synthetic command through the full hook path —
  so a broken install fails here rather than mid-session.

To see the plan without changing anything, add `--dry-run`:

<!-- smoke -->
<!-- expect: "Intenter setup (dry run)" -->
<!-- expect: "Nothing was changed" -->

```console
$ intenter setup claude --dry-run
Intenter setup (dry run)

...

Nothing was changed. Run without --dry-run to apply.
```

**Restart any running Claude Code sessions now.** Claude reads its hook
configuration once, at session start; a session that was already open knows
nothing about Intenter.

## 3. Make a demo project

A project with one script that deletes a build directory:

<!-- smoke -->
<!-- expect: "cleanup" -->

```console
$ mkdir -p /tmp/ag-demo/dist
$ cd /tmp/ag-demo
$ git init -q
$ printf '{"name":"demo","scripts":{"cleanup":"rm -rf ./dist"}}' > package.json
$ touch dist/out.js
$ cat package.json
{"name":"demo","scripts":{"cleanup":"rm -rf ./dist"}}
```

On Windows, use `%TEMP%\ag-demo` and PowerShell's equivalents; the walkthrough
is otherwise identical.

## 4. First run: approve it once

Start Claude Code in that folder and ask it to run the cleanup script:

<!-- manual -->

```console
$ claude
> run npm run cleanup
```

Before Claude's own permission dialog appears, Intenter adds a line saying
what the command *resolves to*:

```text
Intenter [event 1]: npm run cleanup -> rm -rf ./dist
  no approval yet — Claude will ask.
  "Yes, and don't ask again" lets Intenter approve what the command does, not the text you typed.
  Or approve it later: intenter approve 1
```

Read the first line as: the command is not really `npm run cleanup`, it is a
recursive, forced delete of `./dist`, which is a generated directory inside your
workspace, and nothing has approved it yet.

Then Claude asks, as it always does. Choose:

> **Yes, and don't ask again for `npm run cleanup` in /tmp/ag-demo**

That answer is the interesting one. Claude records its own "always allow" rule
for the string `npm run cleanup` — and Intenter notices, and imports it as an
approval for what that command *does*:

<!-- manual -->

```console
$ intenter approvals
ID  KIND   ACTION                    TRUSTED                                  USES  LAST USED  STATE   ORIGIN
1   EXACT  DELETE(recursive,force)   rm -rf ./dist [WORKSPACE_GENERATED]      0     -          ACTIVE  claude_rule

Project: /tmp/ag-demo
```

`ORIGIN` is `claude_rule` because it came from your answer in Claude's dialog
rather than from the CLI. If you answered plain "Yes" instead, no rule was
created — approve the event by hand:

<!-- smoke -->
<!-- expect: "Approved as" -->
<!-- expect: "trusted:" -->

```console
$ intenter approve 1
Approved as exact approval 1.
  trusted: rm -rf ./dist [WORKSPACE_GENERATED]
  valid while these stay unchanged:
    npm-script:package.json#scripts.cleanup
```

That last part is the whole idea. The approval is valid **while those inputs
stay unchanged** — here, the `cleanup` entry in `package.json`. Change it and
the approval stops applying.

Add `--semantic` if you want to trust the *kind* of action rather than these
exact paths — see [how it works](how-it-works.md#exact-and-semantic-approvals).

## 5. Second run: no prompt

Start a **new** Claude session in the same folder and ask for the same thing.

<!-- manual -->

```console
$ claude
> run npm run cleanup
```

It runs, with no dialog: the resolved effects match approval 1, so there is
nothing to ask about. Intenter says so in one line, because an allow with no
trace is indistinguishable from Intenter not being there at all:

```text
Intenter [event 2] ✓ allowed: npm run cleanup -> rm -rf ./dist
  approval 1 · intenter approval show 1
```

Reads that the baseline allows — `git status`, `ls`, `cat README.md` — pass
without a line, so this stays readable. The log records both:

<!-- smoke -->
<!-- expect: "DECISION" -->
<!-- expect: "CLASS" -->

```console
$ intenter history --limit 3
ID  TIME    DECISION  CLASS             COMMAND          RESOLVED         REASON                  APPROVAL
2   12:04   ALLOW     APPROVAL_MATCH    npm run cleanup  rm -rf ./dist    matches approval 1      1
1   12:01   ASK       NO_MATCHING_APPROVAL  npm run cleanup  rm -rf ./dist  first time seen       -
```

## 6. Change the script: blocked

Now do what a compromised dependency, a bad merge or a careless teammate might
do — point the same script name at something outside the project:

```console
$ printf '{"name":"demo","scripts":{"cleanup":"rm -rf ~/Documents"}}' > package.json
```

Ask Claude for `npm run cleanup` a third time.

<!-- manual -->

```console
> run npm run cleanup
```

It never runs. The command is denied before execution, and Claude is told why:

```text
Intenter BLOCK [event 3]: recursive delete of HOME (~/Documents) — rule R2.
  Approval 1 no longer matches:
    fingerprint npm-script:package.json#scripts.cleanup changed
    target ./dist -> ~/Documents
    scope WORKSPACE_GENERATED -> HOME
```

Two independent things happened, and both are worth noticing:

1. **The approval stopped matching.** Its fingerprint covered the script's
   contents; the contents changed, so the approval no longer speaks for this
   command. String matching on `npm run cleanup` would have sailed straight
   through.
2. **A hard rule refused it anyway.** Recursively deleting a directory in your
   home is on the short list of things no approval can authorize. Even if you
   *had* approved it explicitly, the answer would still be no.

Try the softer variant to see the difference — edit the script to
`rm -rf ./src` and ask again. That one is not catastrophic, just not what you
approved, so you get a forced prompt with an `APPROVAL_MISMATCH` explanation
instead of a block. Answering yes there covers that run only; to persist it, run
`intenter approve <event-id>`.

## 7. Read the decision log

Every decision is stored with the reasoning that produced it, and
`history show` replays it from the record — nothing is re-evaluated, so what you
read is what actually happened:

<!-- smoke -->
<!-- expect: "Event" -->
<!-- expect: "command" -->

```console
$ intenter history show 1
Event 1  12:01  ASK (NO_MATCHING_APPROVAL)
    command  npm run cleanup
    cwd      /tmp/ag-demo
    agent    claude (session 01H…)
    resolved rm -rf ./dist
             DELETE(recursive,force) ./dist [WORKSPACE_GENERATED]
    reason   no approval covers these effects yet
```

Useful filters: `--blocked` for everything refused, `--asked` for what needed
confirmation, `--since 24h`, `--project <dir>`, and `--json` on any of them for
scripting.

To see what a project trusts right now:

<!-- smoke -->
<!-- expect: "ACTIVE" -->
<!-- expect: "Project:" -->

```console
$ intenter approvals
ID  KIND   ACTION      TRUSTED                                             USES  LAST USED  STATE   ORIGIN
1   EXACT  RUN_SCRIPT  DELETE(force,recursive) WORKSPACE_GENERATED ./dist  1     12:04      ACTIVE  claude_rule

Project: /tmp/ag-demo
```

Before anything is approved, the same command says so and points at the command
that creates one:

```console
$ intenter approvals
Nothing is approved in this project yet.
Approve an evaluated command with `intenter approve <event-id>` (see `intenter history`).
```

`intenter approval show <id>` expands one approval into everything it covers
and every input it depends on. `intenter approval revoke <id>` withdraws it
permanently; `disable`/`enable` are the reversible pair.

When a Claude session ends, Intenter prints what it did across it — the same
counts `intenter summary` reports on demand:

<!-- smoke -->
<!-- expect: "commands" -->
<!-- expect: "allowed" -->

```console
$ intenter summary
Intenter — 2026-08-23 12:01 to 2026-08-23 12:04

  commands  4
  allowed   2  (1 by approval, 1 read allowed by the baseline)
  asked     1
  blocked   1

1 prompt you did not have to answer, because an approval had already
answered the same question. See what they trust: `intenter approvals`.
```

The last figure counts only the allows a stored approval produced: each one is a
dialog that did not appear. Reads the baseline let through are not counted —
they were never going to prompt.

If something looks wrong, `intenter doctor` checks the installation and prints
a fix for each problem it finds.

## 8. Clean up

Remove the demo project, and — if you were only trying Intenter out — the
integration:

```console
$ rm -rf /tmp/ag-demo
$ intenter uninstall claude
Intenter uninstall

✓ Permission hooks removed (~/.claude/settings.json)
✓ Daemon stopped and unregistered

Claude Code settings that Intenter did not create were left untouched.
Your approvals and history are still there if you reinstall.
```

Add `--purge` to delete the approvals and history too. To remove the binary and
the `PATH` entry as well, use the installer's uninstall one-liner from
[install.md](install.md#uninstall).

---

Next: [how it works](how-it-works.md) — what happens between the hook and the
decision · [security model](security-model.md) — and what is *not* covered ·
[configuration](configuration.md) · [back to the README](../README.md)
