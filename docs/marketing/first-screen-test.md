# First-screen test

Ten seconds is roughly how long a developer gives an unfamiliar repository
before deciding whether to keep reading. This test measures what they take away
in that time, which is not the same as whether the README is well written.

Target — [SC-001](../../specs/004-github-marketing-page/spec.md): **4 of 5**
participants correctly state what Intenter does and who it is for, and **5 of
5** locate the install command without scrolling.

## Setup

- **Participants**: five developers who have never seen the project. They do not
  need to use Claude Code; two who do not is useful, because "who it is for" is
  half of what is being measured.
- **Viewport**: 1440×900, browser at 100% zoom, the rendered repository page —
  not the raw Markdown, not a local preview. Both themes are worth covering:
  run at least two participants in dark mode.
- **Exposure**: exactly 10 seconds. Open the page, count, then hide it —
  a second browser tab to switch to is easier than covering the screen.
- **No preamble.** Do not say what the project is, do not read the tagline out
  loud, do not answer questions during the exposure. The page is on its own.

## Layout budget

Question 3 is decided before any participant sees the page: either the install
block is above the fold or it is not. At 1440×900 the browser chrome and
GitHub's repository header leave roughly **580–620 px** of README, in a content
column about **868 px** wide.

Measured as rendered heights at that width:

| Element | Height |
|---|---|
| `# Intenter` (h1 with its rule) | ~67 px |
| Bold tagline | ~40 px |
| Badge row (one line) | ~36 px |
| Canonical sentence + "why" paragraph (5 lines) | ~136 px |
| Install: label, two code blocks, the setup line | ~256 px |
| **Total to the end of the install block** | **~535 px** |
| Hero visual (1200×640, scaled to the column) | ~462 px |
| Caption | ~36 px |

This is why the install block sits **above** the demo visual rather than below
it, which is the one place the README departs from the hero order in
`contracts/readme-and-collateral.md`. With the visual first, the install block
starts at ~700 px and ends at ~950 px — below the fold on every laptop, which
fails SC-001's "5 of 5 locate the install command without scrolling" no matter
how good the copy is. In the current order the visual begins at ~535 px, so its
first third is still on the first screen: enough to see that there is a demo,
which is what earns the scroll.

The numbers above are computed from GitHub's rendered styles, not measured in a
browser. **Confirm them with dev tools before running the test**, and record the
real figure here — if the install block ends beyond ~580 px, cut a badge or a
line of the "why" paragraph before recruiting anyone.

Also confirm at 400 px width (a phone): no horizontal page scroll. Code blocks
scroll inside themselves, which is expected; the page must not.

## Questions

Asked after the page is hidden, in this order, without prompting:

1. **What does it do?** — a sentence in their own words.
2. **Who is it for?** — who would install this.
3. **How would you install it?** — they may point rather than recite; what is
   being measured is whether they saw the command, not whether they memorised
   it.

Then, before moving on, ask the one open question, number four:
**What made you unsure?** — the only source of copy fixes worth making.

## Scoring

| Question | Pass |
|---|---|
| 1. What does it do | Names permissions/approvals **and** the idea that the decision is about what the command does rather than its name. "It stops the agent running dangerous commands" alone is a **partial** — it misses the differentiator. |
| 2. Who is it for | Developers using an AI coding agent (Claude Code by name is a pass, not a requirement). |
| 3. Install | Points at or recalls the one-line install without scrolling. Scrolling to find it is a fail even if they find it. |

A **partial** on question 1 counts as a fail against the 4-of-5 threshold.
Rounding up here would make the test decorative.

## Results

Copy this block per round; keep the old rounds, since the point of re-running it
after a copy change is the comparison.

### Round 1 — date, README commit

| # | Theme | Q1 what | Q2 who | Q3 install | Q4 what made them unsure |
|---|---|---|---|---|---|
| 1 | | | | | |
| 2 | | | | | |
| 3 | | | | | |
| 4 | | | | | |
| 5 | | | | | |

**Score**: _n_/5 on Q1 · _n_/5 on Q2 · _n_/5 on Q3 → pass / fail

**Changes made as a result**:

## If it fails

The fix is almost always in the first 40 words, and almost never further down
the page — the participants never got there.

- **Q1 partial across the board** → the tagline names the category but not the
  difference. The differentiator has to be in the bold line, not in the
  paragraph under it.
- **Q1 fails with "something about security"** → too abstract too early. The
  `npm run cleanup` story needs to be visible, in the demo or the first
  paragraph.
- **Q2 fails** → "AI coding agents" is not concrete enough for someone who has
  not used one; name Claude Code in the first screen.
- **Q3 fails** → the install block is below the fold. Cut badges or shorten the
  "why" paragraph rather than moving the demo, which is what earns the scroll.
