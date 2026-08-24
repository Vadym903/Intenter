# Repository settings

Everything on the GitHub repository page that is not a file in the repository:
description, topics, website, social preview, Discussions. It is written down
because none of it is version-controlled — a setting changed in the web UI
leaves no diff, and the next person has no way to tell what it was supposed to
be.

Apply with [`scripts/repo-metadata.sh`](../../scripts/repo-metadata.sh) where
the API allows it, by hand where it does not. Record the date each block was
last applied.

## Owner inputs (required before publishing)

Three values are not decided by the code. They were filled on 2026-08-18;
`scripts/check-readme.sh` fails if a placeholder token ever comes back, so
nothing ships on top of a placeholder license.

| Value | Decided as | Where it appears |
|---|---|---|
| Copyright holder | `Derych` | `LICENSE` (header line and the Required Notice) |
| Contact channel | GitHub only, no e-mail: commercial licensing and support → `https://github.com/Vadym903/Intenter/discussions`; security and code-of-conduct reports → private vulnerability reporting, `https://github.com/Vadym903/Intenter/security/advisories/new` | `LICENSE`, `README.md` License section, `SECURITY.md`, `SUPPORT.md`, `CODE_OF_CONDUCT.md` |
| Copyright year | 2026 | `LICENSE` |

Confirm nothing regressed with:

```sh
scripts/check-readme.sh
```

The repository slug is **not** an open input: it is `Vadym903/Intenter`,
already used in every badge, install one-liner and link. Changing it is a
find-and-replace across `README.md`, `llms.txt`, `docs/`, `install.sh`,
`install.ps1` and `.goreleaser.yaml`, not a placeholder fill.

## Description

The canonical sentence, verbatim, from
[`canonical.md`](canonical.md) — 150 characters, inside GitHub's 160-character
limit:

```text
Intenter is a local, deterministic permission layer for AI coding agents that approves what a command actually does, not the string it was typed as.
```

It is the same sentence as the README opening, the `llms.txt` blockquote and the
social preview. That is the point: a search result, an AI answer and the page
itself should agree word for word.

## Topics

Fourteen, within GitHub's limit of twenty, ordered by how someone would search:

```text
claude-code  claude  ai-coding-agent  ai-agents  permissions  guardrails
security  developer-tools  cli  golang  allowlist  agent-safety  hooks  devsecops
```

`claude-code` and `ai-coding-agent` are the ones that matter — they are how
people browsing the ecosystem arrive. The rest widen the net without misleading:
every one of them is something the project actually is.

## Website

Until a landing site exists, the documentation index:

```text
https://github.com/Vadym903/Intenter/tree/main/docs
```

Never leave this empty and never point it at a page that does not exist yet — a
dead "website" link on the repository page costs more trust than a missing one.

## Social preview

`assets/social/preview.png`, 1280×640, under 1 MB, rendered from
`preview.svg` with `make social`. It is what Slack, X, LinkedIn and Discord show
when someone pastes a link, and it is the only part of the page most people on
those platforms will ever see.

Upload it by hand — the API does not expose it:

1. **Settings → General → Social preview → Edit → Upload an image.**
2. Confirm the preview shown there is not cropped: GitHub renders the whole
   1280×640, but clients crop to about 1.91:1, which this ratio already is.
3. Re-upload after every change to `preview.svg`; the old image is cached by
   the platforms for hours.

### Unfurl test

After uploading, paste `https://github.com/Vadym903/Intenter` into:

- **Slack** — a private message to yourself; the card appears within seconds.
- **X** — the composer, without posting; use the
  [card validator](https://cards-dev.twitter.com/validator) if it does not.
- **LinkedIn** — the
  [post inspector](https://www.linkedin.com/post-inspector/) forces a re-fetch.

What to check on each: the image is not stretched or letterboxed, the title
reads as the project name, and the description is the canonical sentence rather
than a truncated README line.

| Date | Platform | Result |
|---|---|---|
| | Slack | |
| | X | |
| | LinkedIn | |

## Discussions

**Settings → General → Features → Discussions → Set up discussions.**

Keep the categories to three, so the first visitor knows where to go:

| Category | Format | For |
|---|---|---|
| Q&A | question / answer | "how do I…", "why did it block this" |
| Ideas | open-ended | proposals before they become issues |
| Show and tell | open-ended | configurations and workflows people built |

Delete the default "General" and "Announcements" categories unless there is
something to announce; empty categories read as an abandoned project.

`SUPPORT.md`, the issue template `config.yml` and the README all link to Q&A,
so enabling Discussions is what makes those links resolve.

## Community profile

**Insights → Community standards** must show every item checked:

- [ ] Description
- [ ] README
- [ ] Code of conduct — `CODE_OF_CONDUCT.md`
- [ ] Contributing guidelines — `CONTRIBUTING.md`
- [ ] License — `LICENSE`
- [ ] Security policy — `SECURITY.md`
- [ ] Issue templates — `.github/ISSUE_TEMPLATE/`
- [ ] Pull request template — `.github/PULL_REQUEST_TEMPLATE.md`

GitHub detects these by name and location only. If an item shows as missing, the
file is in the wrong place or spelled differently — the content is never the
reason.

One thing it will not detect: the license. GitHub's classifier recognises OSI
licenses, and PolyForm Noncommercial is not one, so the sidebar may show
"Unknown license" or nothing at all while the checklist item still passes on the
presence of the file. That is expected; the README License section and the badge
carry the meaning.

## Other settings

- **Releases** — shown in the sidebar automatically; nothing to configure.
- **Packages, Environments, Deployments** — disable in the sidebar's gear icon
  if they are empty, so the page has no dead sections.
- **Issues** — enabled, with blank issues turned off by
  `.github/ISSUE_TEMPLATE/config.yml`.
- **Wiki** — off. The documentation lives in `docs/` and is reviewed with the
  code.
- **Private vulnerability reporting** — **Settings → Code security → Private
  vulnerability reporting → Enable.** `SECURITY.md` and the issue-template
  contact link both point at it, so it must be on before publishing.

## Applied

| Date | What was applied | By |
|---|---|---|
| | | |
