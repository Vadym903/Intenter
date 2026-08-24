# Target queries

The questions someone asks when they have the problem Intenter solves but have
never heard of it. Two kinds, because they behave differently: search phrases
typed into a search engine, and questions asked of an assistant that will answer
from whatever it can read — which for this project is `README.md`, `llms.txt`
and `docs/`.

Each query names the section it should land on. If a query has no good landing
section, that is a gap in the README, not a gap in this list.

## Queries

| # | Query | Kind | Intent | Landing section |
|---|---|---|---|---|
| 1 | how to stop Claude Code asking permission without allowing everything | assistant | Has permission fatigue, knows bypass mode is not the answer | [Why](../../README.md#why) |
| 2 | Claude Code permission prompts every command annoying | search | Same problem, angrier phrasing | [Why](../../README.md#why) |
| 3 | AI coding agent command allowlist that survives script changes | search | Understands the allowlist hole already | [Compared to alternatives](../../README.md#compared-to-alternatives) |
| 4 | is it safe to let Claude Code run npm run scripts | assistant | Evaluating the risk before granting anything | [How it works](../../README.md#how-it-works) |
| 5 | Claude Code hooks permission layer | search | Knows hooks exist, looking for something built on them | [How it works](../../README.md#how-it-works) |
| 6 | Claude Code dangerously-skip-permissions safer alternative | search | Currently using bypass mode and uneasy about it | [Compared to alternatives](../../README.md#compared-to-alternatives) |
| 7 | how do I stop an AI agent from running rm -rf on my home directory | assistant | Wants a floor that no approval can lower | [Security & limitations](../../README.md#security--limitations) |
| 8 | Bash(npm run *) allow rule risk | search | Has the exact rule and suspects the hole | [Compared to alternatives](../../README.md#compared-to-alternatives) |
| 9 | guardrails for AI coding agents without sending code to an LLM | assistant | Cannot or will not add a model to the decision path | [What you get](../../README.md#what-you-get) |
| 10 | Claude Code permission manager macOS Linux Windows | search | Checking platform coverage before trying | [Install](../../README.md#install) |
| 11 | what is Intenter | assistant | Heard the name, wants the one-sentence definition | [README opening](../../README.md) · [`llms.txt`](../../llms.txt) |
| 12 | is Intenter open source / can I use it at work | assistant | Licensing gate before adoption | [License](../../README.md#license) · [FAQ](../../README.md#faq) |
| 13 | AI agent sandbox vs permission layer difference | assistant | Category confusion that costs trust if unanswered | [Security & limitations](../../README.md#security--limitations) |
| 14 | approve a command once and never be asked again Claude Code | search | Wants fewer prompts specifically | [Try it](../../README.md#try-it) |

Queries 11–13 exist because an assistant that describes the project wrongly is
worse than one that does not mention it: "open source" and "sandbox" are the two
things it must never be called, and both are answered in the FAQ in the exact
words that should be quoted back.

## Baseline

Record on publication day, before any promotion. One search engine and one
assistant per query is enough to see movement; the point is a fixed reference,
not a survey.

**Result**: `first page` / `page N` / `not found` for a search engine;
`named + correct`, `named + wrong detail`, `not named` for an assistant, with
the wrong detail quoted.

| Query # | Date | Engine | Result | Notes |
|---|---|---|---|---|
| | | | | |

### +30 days

| Query # | Date | Engine | Result | Notes |
|---|---|---|---|---|
| | | | | |

### +60 days

| Query # | Date | Engine | Result | Notes |
|---|---|---|---|---|
| | | | | |

## What to do with a bad result

- **Not found for a search query** — the phrase probably does not appear in the
  README in the reader's own words. Add it to the FAQ as a question, not as
  keyword padding.
- **Assistant names it with a wrong detail** — the correct fact is missing from
  `llms.txt` or is stated somewhere an assistant is unlikely to read. Move it
  into a quotable sentence near the top of the README.
- **Assistant does not name it at all** — nothing to fix on the page; that is a
  reach problem for [the launch checklist](launch-checklist.md).
