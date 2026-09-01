# How it works

Intenter answers one question, over and over: *may this command run?* This
page is how it gets to an answer, and why the answer is about behavior rather
than text.

- [The path a command takes](#the-path-a-command-takes)
- [Resolution: what does this command actually do?](#resolution-what-does-this-command-actually-do)
- [Scopes: where does it touch?](#scopes-where-does-it-touch)
- [The decision](#the-decision)
- [Exact and semantic approvals](#exact-and-semantic-approvals)
- [Fingerprints and invalidation](#fingerprints-and-invalidation)
- [What you see](#what-you-see)
- [What a session added up to](#what-a-session-added-up-to)
- [Importing "don't ask again"](#importing-dont-ask-again)
- [Why there is no model in the loop](#why-there-is-no-model-in-the-loop)

## The path a command takes

```text
Claude Code                Intenter hook            Intenter daemon
    │                            │                            │
    │ about to run `npm run …`   │                            │
    ├───────────────────────────>│  one JSON object on stdin  │
    │                            ├───────────────────────────>│  parse
    │                            │                            │  resolve
    │                            │                            │  classify
    │                            │                            │  decide
    │                            │<───────────────────────────┤  record
    │<───────────────────────────┤  allow / deny / say nothing │
```

The hook is the same binary, invoked as `intenter hook claude`. It reads one
event, asks the daemon, prints at most one JSON object, and always exits 0 — a
gate that breaks the session is worse than no gate.

The daemon is per-user and local. It holds the approvals database and is the
only thing that decides.

## Resolution: what does this command actually do?

`npm run cleanup` does not tell you anything. Resolution is the work of finding
out what it means:

1. **Parse.** The command line is parsed into commands with a real shell
   grammar — POSIX for `sh`/`bash`/`zsh`, and separate parsers for `cmd.exe` and
   PowerShell. Anything the parser will not commit to interpreting is refused
   rather than guessed at.
2. **Recognize.** Each command is matched against a model of what that program
   does: `rm`, `cp`, `mv`, `mkdir`, `cat`, `grep`, `find`, `git`, `npm`, `pnpm`,
   `yarn`, Gradle, Maven, JS test runners, `curl`. Flags are read properly — `rm
   -rf` is a recursive forced delete, `--` ends the options.
3. **Follow wrappers.** `npm run cleanup` is looked up in `package.json` and the
   script's text is resolved in turn. Gradle and Maven tasks map to the work
   they declare. The chain is recorded: `npm run cleanup → rm -rf ./dist`.
4. **Normalize targets.** Every path is made absolute against the command's
   effective directory — after any `cd` — cleaned, and resolved through symlinks.
5. **Aggregate.** The result is a set of *effects*: a type (`READ`, `WRITE`,
   `CREATE`, `DELETE`, `NETWORK`, `EXECUTE`), a target, and flags such as
   `recursive`, `force` or `broad`.

When any step cannot be completed with certainty — an unknown program, a
variable that cannot be expanded, a script that is not there — the action is
marked unresolved, and an unresolved action is never allowed automatically.

On Windows a package script is genuinely ambiguous: npm hands it to `cmd.exe`,
but Git Bash may supply the utilities it calls. Both readings are evaluated and
their effects combined, because picking one would mean picking, half the time,
the reading that misses the dangerous effect.

## Scopes: where does it touch?

Every target is classified by where it *really* is, using the canonical
symlink-resolved path:

| Scope | What it means |
|---|---|
| `SYSTEM` | `/usr`, `/etc`, `C:\Windows`, … |
| `WORKSPACE` | Inside the project you are working in |
| `WORKSPACE_GENERATED` | Build output inside the project: `dist/`, `build/`, `target/`, `node_modules/` |
| `HOME` | Your home directory, outside the project |
| `OUTSIDE_WORKSPACE` | Anywhere else |

Plus flags that matter on their own: `sensitive` (credential files, SSH keys,
`.env`, Intenter's own configuration), `traversal` (a path that climbs out
with `..`), `symlink_escape` (a project-local name that resolves elsewhere),
`network_path`, `broad` (a whole home directory or drive root).

Classification follows the link rather than the spelling. `./build/output` looks
project-local; if it is a symlink to `~/Documents`, it is treated as your home
directory, because that is what a delete would remove.

## The decision

In order, stopping at the first step that decides:

1. **Hard safety rules (R1–R12).** A small, fixed set no approval can override.
   Five of them can block outright: deleting system locations, recursive deletes
   in your home directory, deletes outside the workspace, single files outside
   it, and changes to credential files. The rest force a prompt: reading
   credentials, escaping the workspace, force-pushing or deleting a remote
   branch, rewriting history, elevated privileges, credentials on the command
   line, disabled TLS verification, and piping a download into a shell.
2. **Uncertainty.** Anything unresolved, unparseable, or with a path that could
   not be pinned down: ask.
3. **The read-only baseline.** A fully resolved action that only reads, only
   inside your project, with nothing sensitive and no escape: allow. This is
   what keeps `git status`, `grep -r`, `ls` and `cat README.md` from
   interrupting. It can be turned off in
   [configuration](configuration.md#policy).
4. **Approvals.** Does a stored approval cover exactly these effects?
5. **Imported consent.** Did the agent already hold persistent permission for
   this command, and does the resolved action pass everything above?
6. **Otherwise: ask.**

## Exact and semantic approvals

Approving records an *envelope*: the effect types, scopes and flags the action
produced.

- **EXACT** — the default. Covers these effects on these display targets.
  Approving `rm -rf ./dist` does not cover `rm -rf ./src`.
- **SEMANTIC** — opt in with `intenter approve <id> --semantic`. Covers the
  same effects anywhere in the same scope, so a delete of any generated
  directory in this project is allowed. Useful when the exact path varies;
  deliberately not the default, because it is a wider promise than most people
  mean to make.

Both are scoped to one project, and both stop matching if the effects grow. An
approval for a delete does not cover a delete plus a network call.

## Fingerprints and invalidation

An approval records a hash of every mutable input the resolution depended on:

```text
valid while unchanged:
  npm-config:.npmrc#script-shell           5a42f60a7908
  npm-script:package.json#scripts.cleanup  66165daf8787
```

Change any of them and the approval stops matching, and the prompt explains
which one changed. This is the mechanism behind the demo: the approval was never
for the string `npm run cleanup`, it was for `rm -rf ./dist` *given that
`package.json` says what it said*.

Fingerprints cover npm/pnpm/yarn scripts and their shell configuration, Gradle
and Maven build files, and the lockfiles that decide what an install would
fetch.

## What you see

The decision maps onto Claude's own permission flow, and which of the three
happens is worth understanding:

| Decision | What Claude does |
|---|---|
| ALLOW, an approval matched | Runs without a prompt, and one line names the approval that allowed it |
| ALLOW, the read-only baseline | Runs without a prompt, and without a line |
| BLOCK | Refused, with Intenter's reason |
| ASK, never approved before | Intenter **steps back** and lets Claude's own dialog appear — the one with "Yes, and don't ask again" |
| ASK, an approval stopped matching | Intenter **forces** the dialog, so a Claude rule that would have allowed it silently cannot |

The difference between the last two is why "ask" sometimes shows an Intenter
message and sometimes does not. It is recorded, so `intenter history show`
tells you which happened.

The split in the first two is about noise rather than secrecy. An allow that one
of your approvals produced is worth seeing:

```text
Intenter [event 2] ✓ allowed: npm run cleanup -> rm -rf ./dist
  approval 1 · intenter approval show 1
```

The baseline is not. It answers `git status`, `ls` and `cat README.md` dozens of
times in a session, and a line for each would bury everything else. Both are in
`intenter history` either way.

## What a session added up to

When a Claude session ends, Intenter reports what it did across it:

```text
Intenter this session: 46 commands checked — 42 allowed, 3 asked, 1 blocked.
  2 commands allowed by approvals you gave once — 2 prompts you did not have to answer.
  intenter summary
```

Claude shows a `SessionEnd` message to you and not to itself, which is what
makes it the right place for a report meant for a person: it costs the model no
context. A session in which nothing was decided prints nothing.

The second line is the only "what did this save me" figure the audit log
supports, and it is deliberately narrow. It counts allows that a stored approval
produced — each one a dialog that did not appear because the same question had
already been answered. Reads let through by the baseline are not counted: they
were never going to prompt, so counting them would inflate the number. Nothing
is converted into minutes, because the log knows how many prompts did not happen
and cannot know what any of them would have cost.

`intenter summary` asks the same question on demand, over the last 24 hours by
default:

```console
$ intenter summary
Intenter — 2026-08-23 09:12 to 2026-08-23 17:40

  commands  46
  allowed   42  (2 by approval, 40 reads allowed by the baseline)
  asked     3
  blocked   1

2 prompts you did not have to answer, because an approval had already
answered the same question. See what they trust: `intenter approvals`.
```

`--since`, `--project` and `--session` narrow it; `--all` counts everything on
record; `--json` gives the same numbers for scripting. They are counted from the
same audit rows `intenter history` lists, so the two can never disagree.

## Importing "don't ask again"

When you answer "Yes, and don't ask again" in Claude's dialog, Claude writes a
rule for the *string*. Intenter notices, resolves the command fully, checks it
against every rule above, and only then records an approval for the resolved
effects.

The string rule keeps existing on Claude's side; it is simply no longer the only
thing standing between the agent and your files. When the script behind it
changes, Claude's rule still matches and Intenter's approval does not.

Because that approval is created on your behalf rather than typed by you, the
first run it allows says where it came from:

```text
Intenter [event 2] ✓ allowed: npm run cleanup -> rm -rf ./dist
  approval 1, imported from Claude's "don't ask again" · intenter approval show 1
```

The dialog itself cannot be labelled: Claude renders its own options, and
nothing a hook returns reaches them. What Intenter can do is say, in the line
before the dialog, what that answer will mean here.

### Two things trust a command, so both are listed

That leftover string rule is why "what is trusted here" has two answers.
`intenter approvals` shows both: the approvals Intenter holds, and the rules
Claude holds of its own. A rule allows a command whether or not Intenter ever
imported it, so a list that showed only approvals would tell you this project
trusts less than it does.

It is also why taking a permission back reaches both. `intenter approval revoke`
removes the approval *and* the rules that grant the same command — otherwise the
command would keep running silently and the revoke would have changed only a
record. What it will change is printed first, and the settings file is backed up
before it is written; see [security model](security-model.md).

## Why there is no model in the loop

Matching is a pure function of the resolved action and the stored approvals. The
same command in the same project always gets the same answer, and the answer can
be recomputed from the audit record months later. A language model in the
decision path would make the gate non-deterministic, slow, and dependent on a
network — three things a permission check must not be.

---

Next: [security model](security-model.md) — what this does *not* protect ·
[configuration](configuration.md) · [README](../README.md)
