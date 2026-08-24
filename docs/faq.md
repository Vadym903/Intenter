# FAQ

## Does this replace Claude Code's permission prompts?

No. It decides what those prompts are *about*. Claude still asks; Intenter
makes sure the question is about what the command actually does, and blocks the
handful of things no answer should permit.

## What happens the very first time I run a command?

Intenter resolves it, finds no approval, and steps back — Claude's own dialog
appears, including the "Yes, and don't ask again" option. Answer that and
Intenter turns it into an approval for the resolved effects.

It deliberately does not force its own prompt there, because a hook-forced
dialog in Claude does not offer the persistent option, and taking that away
would make the first prompt worse rather than better.

## Why did some commands not prompt at all?

Reads inside your project are allowed without asking: `git status`, `git diff`,
`grep -r`, `ls`, `cat README.md`, `find`. Anything sensitive, anything outside
the project and anything that writes still goes through the normal path. Turn
the baseline off with `allow_readonly_workspace = false` if you would rather be
asked about everything.

## Why was I asked again for a command I approved?

Something the approval depended on changed — most often the script itself. The
prompt names it, and `intenter approval show <id>` lists everything being
watched. This is the entire point of the tool.

## Does an upgrade lose my approvals?

No. Approvals and history live in a database the installer never touches. An
upgrade replaces only the binary. `--purge` on uninstall is the only thing that
deletes them, and you have to ask for it.

## Does it work when I run `claude -p` non-interactively?

Blocks are enforced. Asks become denials, because a non-interactive session has
nobody to ask — which is the fail-safe direction. Approve the commands your
scripted runs need beforehand with `intenter approve`.

## What about bypass mode?

In `bypassPermissions` mode Intenter enforces only blocks — the hard safety
rules — and stays out of everything else. You asked for no prompts; you still do
not get a deleted home directory *from any command Intenter can resolve*:
`rm -rf ~/Documents`, `npm run cleanup` resolving to it, `Remove-Item -Recurse
-Force ~`. The limit is honesty about what a static gate can see: a delete
hidden inside an opaque string — `bash -c '<script>'`, a command built at
runtime — is unresolved, and unresolved commands are never approved by Intenter
but, in this mode, are run by Claude exactly as bypass mode intends. Outside
bypass mode they go to Claude's own prompt instead. If that matters to you,
do not run in bypass mode; the read-only baseline and remembered approvals
already remove most prompts without it.

## How much does it slow things down?

An evaluation is a few milliseconds: a local socket round trip, a parse, some
path resolution, a SQLite lookup. There is no network call and no model. The
budget for the whole hook is well under Claude's ten-second timeout, and the
work that dominates is reading `package.json`.

## Does it send anything anywhere?

No telemetry, no accounts, no model calls. The only outbound request the binary
makes is an anonymous check of the public release page for a newer version —
no identifiers, nothing about you or your commands — and `updates.check = false`
or `INTENTER_NO_UPDATE_CHECK=1` switches even that off. Decisions never touch
the network. See [security model](security-model.md#what-stays-on-your-machine)
and [updates](updates.md#turning-it-off).

## Is it free? Can I use it at work?

Intenter is source-available under the PolyForm Noncommercial License 1.0.0.
You may use, copy, modify and share it for personal and other noncommercial
purposes — hobby projects, study, research, and use by charities, schools,
public research bodies and government institutions. Using it commercially, or
selling it, requires a separate commercial license from the copyright holder;
the contact is in the [License](../README.md#license) section of the README.

## Is it open source?

Not in the OSI sense: the license permits noncommercial use only, so it is
"source-available". The source is public, you can read every rule it applies,
and contributions are welcome under the terms in
[CONTRIBUTING.md](../CONTRIBUTING.md#contribution-terms).

## Can I see everything it knows about me?

`intenter history` and `intenter approvals`, both with `--json`. That is all
of it, and it is a file on your disk.

## How do I undo an approval?

```console
$ intenter approval revoke 1     # permanent
$ intenter approval disable 1    # reversible
$ intenter approval enable 1
```

Nothing is deleted from the record — a revoked approval stays, so the history
that mentions it still explains itself.

## What is the difference between EXACT and SEMANTIC?

EXACT covers these effects on these targets: approving `rm -rf ./dist` does not
cover `rm -rf ./src`. SEMANTIC covers the same effects anywhere in the same
scope — any generated directory in this project. SEMANTIC is opt-in
(`intenter approve <id> --semantic`) because it is a wider promise than most
people mean to make.

## Can I share approvals across a team, or a machine?

Not yet. They are per user, per machine, per project, and a checkout at a
different path is a different project.

## Does it gate the files Claude edits directly?

No. Claude's `Write` and `Edit` tools do not go through a shell, so Intenter
never sees them. It gates `Bash` and `PowerShell` commands. This is the most
important limitation to know about — see
[limitations](security-model.md#limitations).

## Which agents work with it?

Claude Code only, for now. The core has no knowledge of any particular agent —
that lives entirely in an adapter — so another integration is additive work
rather than a rewrite.

## Why is there no AI in it?

Because a permission check has to give the same answer every time, work offline,
and be explainable from a record months later. Matching is a pure function of
the resolved command and the stored approvals.

## Something I do all day keeps getting asked about

Look at `intenter history show <id>`. If it says `UNRESOLVED`, Intenter
cannot tell what the command does and will not guess — often a program it does
not model. If it resolves fine, approve it once and it will stop asking until
the behavior changes.

## Can I run it in CI?

It is designed for an interactive developer machine. In CI there is nobody to
prompt, so asks become denials; you would need to approve everything in advance,
on that machine. A container or a locked-down runner is a better fit for CI than
a permission prompt.

## How do I remove it completely?

```sh
curl -fsSL https://raw.githubusercontent.com/Vadym903/Intenter/main/install.sh | sh -s -- --uninstall --purge
```

Hooks, service, binary, PATH entry, approvals and history. Claude Code settings
Intenter did not create are left exactly as they were.

---

Still stuck? [Troubleshooting](troubleshooting.md) ·
[How it works](how-it-works.md) · [README](../README.md)
