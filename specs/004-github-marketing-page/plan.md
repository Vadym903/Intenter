# Implementation Plan: GitHub Marketing Page (README, Discoverability & Community Presence)

**Branch**: `004-github-marketing-page` | **Date**: 2026-08-16 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/004-github-marketing-page/spec.md` (decisions: PolyForm Noncommercial 1.0.0 license; README + repository metadata scope; GEO = Generative Engine Optimization)

**Progress tracking**: `tasks.md` (tick `- [x]` as work completes)

## Summary

Turn the repository page into a marketing-grade landing page without a single untrue claim: restructure `README.md` around a first screen (tagline, badges, two-sentence why, scripted demo GIF, install one-liners, TOC) and a skimmable trust structure (why → how → what you get → honest comparison → install/setup/try → CLI → security & limitations → updating → FAQ → docs → status/roadmap → contributing → license); make the project discoverable and correctly described by search and AI answer engines (repository description/topics/website/social preview, quotable canonical facts, `llms.txt`, FAQ, target-query baseline); make it credible and safe to adopt (real license text — PolyForm Noncommercial 1.0.0 — with plain-language terms and a commercial contact, contribution terms, code of conduct, security policy, support page, issue/PR templates); and keep it truthful over time with a collateral kit and CI checks (links, badges, placeholders, wording, canonical-sentence consistency, version drift, demo size).

## Technical Context

**Language/Version**: Markdown (README, docs, community files), YAML (issue forms), SVG/PNG (social preview), VHS tape + POSIX sh (demo recording), small shell scripts for checks; no application code changes

**Primary Dependencies**: existing `docs` CI job (lychee, markdownlint, placeholder grep) **[repo]**; shields.io badges; VHS (`charmbracelet/vhs`, optional local tool for regenerating the demo); `rsvg-convert`/Inkscape (optional, social PNG export); `gh` CLI (optional metadata helper); GitHub repository settings (description, topics, website, social preview, Discussions)

**Storage**: none (repository files + GitHub settings; measurements recorded in `docs/marketing/*.md`)

**Testing**: `make docs-check` extended (badges, wording, alt text, canonical sentence, version drift, demo size); manual protocols for first-screen test, unfurl test, community-profile check, query baseline (documented in `docs/marketing/`)

**Target Platform**: GitHub repository page (light/dark themes, desktop and mobile), social unfurls (Slack, X, LinkedIn, Discord), AI answer engines/crawlers reading `README.md` and `llms.txt`

**Project Type**: documentation/marketing assets + repository configuration

**Performance Goals**: first screen ≤ ~700 px tall at 1440×900; demo GIF ≤ 3 MB and ≤ 30 s; social preview ≤ 1 MB; `make docs-check` < 3 min

**Constraints**: every claim linked and true; never "open source" self-description; planned items marked "Planned:"; no external embeds/scripts/trackers; owner-supplied copyright holder and commercial contact required before merge; English canonical page only

**Scale/Scope**: 1 README rewrite, ~7 community files, `llms.txt`, ~7 collateral docs, 2 asset pipelines (demo, social), 2 helper scripts, CI job extension

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

`.specify/memory/constitution.md` is still the unfilled template → no ratified gates. Effective gates from feature 001 principles and this spec's own constraints:

| Gate | Pre-research | Post-design |
|---|---|---|
| Truthfulness: every claim linked, roadmap ≠ shipped, comparison factual (P13 explainability spirit) | PASS | PASS (copy rules + CI wording checks) |
| P1 local-first / no telemetry: no trackers, no scripts, no external embeds in README | PASS | PASS (GIF/Mermaid only) |
| Licensing consistency: LICENSE ↔ README ↔ badge; no "open source" claim | PASS (decision) | PASS (placeholder + wording checks) |
| I-9-style safety of repository files: existing accurate content preserved/moved, not deleted | PASS | PASS (README structure keeps all current sections) |
| Scope: no landing site/translations/docs site | PASS | PASS |

No violations → Complexity Tracking empty.

## Project Structure

### Documentation (this feature)

```text
specs/004-github-marketing-page/
├── spec.md
├── plan.md              # This file
├── research.md          # R-01…R-13
├── data-model.md        # README structure, canonical sentence, badges, metadata, demo, llms.txt, community files, collateral, checks
├── quickstart.md
├── contracts/
│   ├── readme-and-collateral.md          # copy rules, hero/demo/comparison/FAQ contracts, collateral kit, CI checks
│   └── repo-metadata-license-community.md# metadata values, LICENSE/README wording, contribution terms, community files, llms.txt
├── checklists/requirements.md
└── tasks.md
```

### Source Code (repository root — additions/changes only)

```text
README.md                          # rewritten to the contract structure (existing accurate content preserved/moved)
LICENSE                            # header + verbatim PolyForm Noncommercial 1.0.0 (replaces placeholder)
llms.txt                           # NEW: machine-readable summary for AI crawlers
CODE_OF_CONDUCT.md, SECURITY.md, SUPPORT.md            # NEW
CONTRIBUTING.md                    # + "Contribution terms" (license, DCO sign-off, relicensing grant)
.github/ISSUE_TEMPLATE/{bug_report,feature_request,question}.yml, config.yml   # NEW
.github/PULL_REQUEST_TEMPLATE.md   # NEW
assets/demo/{agentguard.tape,session.sh,agentguard.gif,agentguard.png}         # NEW (demo pipeline + committed outputs)
assets/social/{preview.svg,preview.png}                # NEW (social preview)
docs/marketing/{canonical.md,pitch.md,describe.md,launch-checklist.md,repo-settings.md,target-queries.md,first-screen-test.md}   # NEW collateral kit
docs/faq.md                        # + licensing Q/A aligned with README FAQ
scripts/check-badges.sh, scripts/check-readme.sh, scripts/repo-metadata.sh    # NEW checks + metadata helper
Makefile                           # + demo, social, docs-check extensions
.github/workflows/ci.yml           # docs job: badges/wording/alt/canonical/version-drift/demo-size checks
CHANGELOG.md                       # entry
```

**Structure Decision**: No Go changes. All marketing assets live in `assets/` and `docs/marketing/`; checks are shell scripts invoked by `make docs-check` and the existing `docs` CI job, so the README cannot drift silently.

## Implementation Phases (roadmap)

| # | Step | Delivers | Exit criterion | `tasks.md` phase(s) |
|---|---|---|---|---|
| 1 | Licensing & community foundation | LICENSE (real text), README license wording, contribution terms, CoC, SECURITY, SUPPORT, issue/PR templates | community profile 100% (once public); placeholder checks pass with owner inputs | 1 Setup, 2 Foundational |
| 2 | README rewrite & assets | canonical sentence, hero, TOC, sections in contract order, Mermaid flow, comparison table, FAQ, status/roadmap; demo GIF pipeline; social preview | first-screen fits; all links/badges resolve; demo ≤ 30 s | 3 US1, 4 US2 |
| 3 | Discoverability (SEO/GEO) | repository metadata + helper script, `llms.txt`, target-query list with baseline procedure | metadata complete; `llms.txt` reachable; ≥ 10 queries recorded | 5 US3 |
| 4 | Credibility signals & upkeep | badges (live sources only), CI checks (badges, wording, alt, canonical, version drift, demo size), collateral kit, first-screen protocol | `make docs-check` green; kit complete | 6 US4, 7 US5 |
| 5 | Validation | quickstart §2–§7 executed and recorded (first-screen results, unfurl tests, baseline) | SC-001…SC-008 evidenced | 8 Polish |

## Complexity Tracking

> No constitution violations to justify.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| — | — | — |
