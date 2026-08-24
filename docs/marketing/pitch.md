# Pitch

Approved copy at four lengths. Use it as written. If a channel needs something
that is not here, add it here first and then use it — that is what keeps the
project describing itself the same way everywhere, and it is the whole point of
[SC-008](../../specs/004-github-marketing-page/spec.md).

Every length that has room ends with the licensing sentence, because "is this
free?" is the first question in every thread and answering it late reads like
hiding it.

## Canonical sentence

Never edited, never paraphrased. Changing it means changing
[`canonical.md`](canonical.md) first and every surface in the same commit.

```text
Intenter is a local, deterministic permission layer for AI coding agents that approves what a command actually does, not the string it was typed as.
```

## Tagline (66 characters)

```text
Stop approving command strings. Approve what commands actually do.
```

Short form, where a tagline has to fit next to a logo:

```text
Approve what a command does, not what it is called.
```

## List entry (88 characters)

For an "awesome" list or any directory whose rows are a link and a clause. Added
after the [kit dry run](kit-dry-run.md) found the one-line pitch too long for
this shape.

```text
A permission layer for AI coding agents that approves what a command does, not its name.
```

## One line (130 characters)

For directory entries, a repository description field that is not GitHub's, or
a post that has to fit in one line.

```text
A local permission layer for AI coding agents: it approves what a command does, not what it is called. Free for noncommercial use.
```

## Short (49 words)

For a Show HN blurb, a newsletter item, or the first paragraph of a post.

```text
AI coding agents ask permission constantly, so people either stop reading the
prompts or hand over everything. Intenter resolves each command to what it
actually does, remembers that, and re-asks when it changes. Deterministic,
local, no LLM. Free for personal and noncommercial use; commercial use requires
a separate license.
```

## Long (148 words)

For a dev.to post intro, a directory submission with a description field, or a
reply explaining the project in a thread.

```text
Intenter is a permission layer that sits between an AI coding agent and your
shell. When Claude Code asks to run `npm run cleanup`, you are not approving
three words — you are approving whatever that script contains today. Intenter
resolves the command first: it follows npm, pnpm, yarn, Gradle and Maven
wrappers to what actually runs, classifies every path it touches, and remembers
those effects rather than the command text. Edit `package.json` so `cleanup`
deletes something outside the project and the approval stops matching; you are
asked again, with an explanation of exactly what changed. A short list of
catastrophic actions is blocked no matter what was approved. No language model
in the decision path, no telemetry, no account: the same command always gets the
same answer, offline. Claude Code on macOS, Linux and Windows. Free for personal
and noncommercial use; commercial use requires a separate license.
```

## Licensing sentence

The only wording used for licensing, everywhere:

```text
Free for personal and noncommercial use; commercial use requires a separate license.
```

The reply when someone says "so it is not open source" — the question arrives
within minutes of every post, and an answer written on the spot is where the
wording drifts:

```text
Correct — it is source-available, not open source in the OSI sense. The source
is public and every rule is readable; personal and noncommercial use is free;
commercial use needs a separate license.
```

Longer form, when there is room to name the license:

```text
Source-available under the PolyForm Noncommercial License 1.0.0: free for
personal and other noncommercial use; selling it or using it commercially
requires a separate license. It is not open-source software in the OSI sense.
```

## Words never to use

- **"Open source"** as a self-description. It is source-available. The only
  permitted phrasing is the negation: "not open-source software in the OSI
  sense".
- **"Sandbox"**, "isolation", "containment". It decides *whether* a command
  runs; an allowed command runs with your privileges.
- **"AI-powered"**, "intelligent", "smart". The absence of a model in the
  decision path is the feature.
- **"Secure"** as an unqualified adjective. It gates shell commands; say what it
  gates.
- Any planned integration without the word **"Planned:"** in front of it.
