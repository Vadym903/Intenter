# Launch checklist

Publication day, in order. The pre-flight exists because the expensive mistake
is not a weak post — it is a good post pointing at a page with a broken badge, a
404 in the install command, or a license nobody can find.

Every piece of text used below comes from [`pitch.md`](pitch.md) and
[`describe.md`](describe.md). Writing something new for a channel means the kit
is missing a length; add it there first.

## Pre-flight

Nothing is posted until every line here is ticked.

- [ ] **Owner inputs filled** — `scripts/check-readme.sh` passes with zero
      placeholders (copyright holder, contact). See
      [`repo-settings.md`](repo-settings.md).
- [ ] **A release is published** with binaries for all six targets and
      `checksums.txt`. The install one-liner resolves a real asset, not a
      redirect to an empty releases page.
- [ ] **Install tested from the published release** on macOS and Windows, in a
      shell that has never had Intenter on its PATH.
- [ ] **`make docs-check` green** — links, markdown, badges, README rules.
- [ ] **All badges enabled** — both the `<!-- after-repository-is-public -->`
      and `<!-- after-first-release -->` guards removed from `README.md`, and
      `scripts/check-badges.sh` reporting zero skipped badges and zero errors.
      See [release process](../release-process.md#badges).
- [ ] **Repository metadata applied** — description, topics, website
      (`scripts/repo-metadata.sh`).
- [ ] **Social preview uploaded** and the unfurl test run on Slack, X and
      LinkedIn, recorded in [`repo-settings.md`](repo-settings.md).
- [ ] **Discussions enabled** with Q&A, Ideas, Show and tell. Every link in
      `SUPPORT.md` and the issue templates resolves.
- [ ] **Private vulnerability reporting enabled**, so `SECURITY.md`'s preferred
      route works.
- [ ] **Community standards 100%** — Insights → Community standards.
- [ ] **`llms.txt` reachable** at
      `https://raw.githubusercontent.com/Vadym903/Intenter/main/llms.txt`.
- [ ] **The demo renders** on the rendered README in both themes, on a phone and
      on a laptop.
- [ ] **Baseline recorded** in [`target-queries.md`](target-queries.md) —
      *before* posting anything, or there is nothing to compare against.

## Channels

Post over two or three days rather than all at once: each venue produces
questions, and answering them well is what the next venue's readers see.

| # | Channel | What to post | Notes |
|---|---|---|---|
| 1 | **Show HN** | Title: `Show HN: Intenter – approve what a command does, not what it is called`. Body: the 49-word pitch, then what is not built yet. | Post it yourself, in the morning US time. State the license in the body — HN asks within minutes otherwise, and answering it first reads as candour. |
| 2 | **r/ClaudeAI** | The short pitch plus the demo GIF; lead with the `npm run cleanup` story rather than the feature list. | The most on-target audience. They know exactly what permission fatigue is; skip the explanation of the problem. |
| 3 | **r/commandline** | The demo GIF and the CLI table. | Cares about the tool, not the AI framing. Lead with the terminal. |
| 4 | **X** | The 130-character line + the social card + the repository link. | The card does the work; the text should not repeat it. |
| 5 | **LinkedIn** | The 49-word pitch + the card. | Slower burn, worth the two minutes. |
| 6 | **dev.to** | A post built from the long pitch plus the "Why" and "How it works" sections, ending with install. | Canonical URL back to the repository. Do not fork the copy — link the docs for detail. |
| 7 | **awesome-claude-code** and similar lists | The two-sentence description from `describe.md` + repository link. | **Check the list's licensing rule first.** |
| 8 | **Hacker News comment threads** on adjacent posts | Nothing pre-written. | Only where it genuinely answers the question being asked. Anything else is spam and reads as it. |

### Lists that do not qualify

Directories that require an OSI-approved license — most "awesome open source"
lists, several package directories, and anything whose submission form asks you
to confirm the project is open source — are **n/a**. The project is
source-available under PolyForm Noncommercial. Do not submit, and do not soften
the description to get in: one inaccurate listing is quoted back for years, and
it contradicts the README, the LICENSE and the FAQ.

Lists that accept source-available projects are fine; check the contribution
guidelines rather than the title.

## The questions that always come up

Answers live in the README FAQ; keep them consistent word for word.

| Question | Where the answer is |
|---|---|
| "Is this open source?" | [FAQ](../faq.md#is-it-open-source) — source-available, PolyForm Noncommercial, not OSI. |
| "Why not just use `Bash(npm run *)`?" | README "Compared to alternatives". |
| "Does it use an LLM?" | [FAQ](../faq.md#why-is-there-no-ai-in-it) — no. |
| "Is it a sandbox?" | README "Security & limitations" — no. |
| "Does it phone home?" | [FAQ](../faq.md#does-it-send-anything-anywhere) — update check only, switchable. |
| "Can I use it at work?" | [FAQ](../faq.md#is-it-free-can-i-use-it-at-work) — commercial use needs a separate license. |

## Post-launch

- [ ] **Day 0** — baseline already recorded; note the launch date next to it.
- [ ] **Day 0–3** — answer every issue and comment within a day. This is the
      only window where a first impression is still forming.
- [ ] **Day 3** — triage: label everything, close what is answered, convert
      questions to Discussions so they stay findable.
- [ ] **Day 7** — re-read the FAQ against what people actually asked. Anything
      asked twice belongs in the README.
- [ ] **+30 days** — re-run [`target-queries.md`](target-queries.md).
- [ ] **+60 days** — re-run it again; SC-007 wants ≥ 5 of the queries returning
      a correct description by then.
- [ ] **Whenever the copy changes** — re-run the
      [first-screen test](first-screen-test.md).
