# Quickstart & Validation Guide: GitHub Marketing Page

Purpose: verify the repository page converts, is credible, is discoverable, and stays consistent. Contracts: `contracts/readme-and-collateral.md`, `contracts/repo-metadata-license-community.md`.

## Prerequisites

- Owner inputs: copyright-holder name, commercial-licensing contact, final GitHub org/repo name.
- Tools for regeneration (optional locally, not required in CI): `vhs` (demo GIF), `rsvg-convert` or Inkscape (social preview PNG), `gh` CLI (metadata helper).

## 1. Automated checks

```bash
make docs-check          # lychee, markdownlint, placeholders, badges, wording, alt text, canonical sentence, version drift, demo size
make demo                # regenerates assets/demo/agentguard.gif + .png (needs vhs); commit if changed
make social              # regenerates assets/social/preview.png from preview.svg (needs rsvg-convert)
```
Expected: all green; README has zero `TODO(`/`<COPYRIGHT HOLDER>`/`<CONTACT>`; every badge 200.

## 2. Rendered README review

Open the repository page at 1440×900 (light and dark theme): title, tagline, badges, "why", demo GIF and both install one-liners visible without scrolling; TOC links jump to every section in the contract order; demo GIF plays ≤ 30 s and shows approve → auto-allow → change → block; images have alt text (inspect); narrow viewport (≤ 400 px): no horizontal page scroll (tables/code scroll inside their box).

## 3. First-screen test (SC-001)

Follow `docs/marketing/first-screen-test.md`: 5 developers, 10-second exposure, questions "What does it do? Who is it for? Where would you click to install?" — record results; pass = ≥ 4/5 correct on purpose, 5/5 find install.

## 4. Repository metadata & unfurls (SC-005)

`bash scripts/repo-metadata.sh` (or set manually per `docs/marketing/repo-settings.md`); upload `assets/social/preview.png`; enable Discussions. Paste the repo URL into Slack, X/Twitter card validator (or a post preview), and LinkedIn post preview → image, title and description render. GitHub → Insights → Community standards → 100%.

## 5. Licensing consistency (SC-004)

`LICENSE` starts with the header (holder, year, SPDX id) followed by verbatim PolyForm Noncommercial 1.0.0; README license section, badge, FAQ answers agree; `grep -i "open source" README.md llms.txt` returns nothing self-descriptive; commercial contact present once.

## 6. GEO baseline (SC-007)

Fill `docs/marketing/target-queries.md`: ≥ 10 queries, intended landing sections; on publication day record for each query the result in a search engine and one AI assistant (name, correct/incorrect/missing); schedule +30/+60 day re-checks; `llms.txt` reachable at the raw URL and quotes the canonical sentence.

## 7. Collateral (SC-008)

Draft a sample post and a directory submission using only `docs/marketing/pitch.md` + `describe.md`; no new copy needed; `launch-checklist.md` marks OSI-only directories as not applicable.
