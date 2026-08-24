# How to describe Intenter

The block to paste when a directory, an "awesome" list, a newsletter or a
comment asks what this is. It is deliberately separate from
[`pitch.md`](pitch.md): that file holds the copy at four lengths, this one is
the whole submission — description, links and licensing — in the order those
forms usually ask for them.

Everything below is approved. Do not rewrite it for a specific venue; if a venue
needs something different, add it here first.

## Description (three sentences)

```text
Intenter is a permission layer for AI coding agents that approves what a
command actually does rather than the string it was typed as. It resolves each
command — following npm, pnpm, yarn, Gradle and Maven wrappers to what really
runs — remembers the resolved effects you approve, and asks again the moment a
remembered command starts doing something else, while a short list of
catastrophic actions stays blocked no matter what was approved. Decisions are
deterministic and local: no language model, no telemetry, no account.
```

Two-sentence version, where a form counts characters:

```text
Intenter is a permission layer for AI coding agents that approves what a
command actually does rather than the string it was typed as, so an approval
stops applying the moment the script behind it changes. It runs locally and
decides deterministically — no language model, no telemetry, no account.
```

## Category and platform

- **Category**: developer tool · CLI · AI agent tooling · security
- **Supported agent**: Claude Code (`Bash` and `PowerShell` tools)
- **Platforms**: macOS, Linux, Windows
- **Language**: Go
- **Install**: `curl -fsSL https://raw.githubusercontent.com/Vadym903/Intenter/main/install.sh | sh`

Never list an agent, editor or tool that is not shipped. The roadmap items go in
a "planned" field if the form has one, prefixed "Planned:", or nowhere.

## Links

| What | URL |
|---|---|
| Repository | `https://github.com/Vadym903/Intenter` |
| Documentation | `https://github.com/Vadym903/Intenter/tree/main/docs` |
| Getting started | `https://github.com/Vadym903/Intenter/blob/main/docs/getting-started.md` |
| How it works | `https://github.com/Vadym903/Intenter/blob/main/docs/how-it-works.md` |
| Security model | `https://github.com/Vadym903/Intenter/blob/main/docs/security-model.md` |
| License | `https://github.com/Vadym903/Intenter/blob/main/LICENSE` |
| Releases | `https://github.com/Vadym903/Intenter/releases` |
| Discussions | `https://github.com/Vadym903/Intenter/discussions` |
| Machine-readable summary | `https://raw.githubusercontent.com/Vadym903/Intenter/main/llms.txt` |

## Licensing

Include this whenever the venue has a license field, and in the body when it
does not:

```text
Free for personal and noncommercial use; commercial use requires a separate
license.
```

With room to name it:

```text
Source-available under the PolyForm Noncommercial License 1.0.0. It is not
open-source software in the OSI sense: the source is public and every rule is
readable, but commercial use is reserved.
```

**Lists that require an OSI-approved license do not qualify.** Do not submit and
do not describe the project as open source to get in — see
[`launch-checklist.md`](launch-checklist.md), where those venues are marked.

## Screenshot and preview image

- Social card: `assets/social/preview.png` (1280×640)
- Demo, animated: `assets/demo/intenter.gif`
- Demo, still: `assets/demo/intenter.png`
- Hero illustration: `assets/demo/intenter.svg`

Alt text for any of them:

```text
Intenter approves npm run cleanup once, runs it without a prompt in the next
session, and blocks it with an explanation after package.json changes what it
deletes.
```
