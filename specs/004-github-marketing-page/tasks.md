# Tasks: GitHub Marketing Page (README, Discoverability & Community Presence)

**Input**: Design documents from `/specs/004-github-marketing-page/` — `plan.md`, `spec.md`, `research.md` (R-01…R-13), `data-model.md`, `contracts/readme-and-collateral.md`, `contracts/repo-metadata-license-community.md`, `quickstart.md`

**Prerequisites**: features 001–003 implemented (binary, installers, docs, updater); owner inputs collected before merge: **copyright-holder name**, **commercial-licensing contact**, **final GitHub org/repo name**.

**Tests**: The spec requires automated upkeep checks (links, badges, placeholders, wording, canonical consistency, version drift, demo size) and documented manual protocols (first-screen test, unfurl test, community profile, query baseline); those are included as tasks.

**Organization**: Setup → Foundational (licensing + community files) → user stories in priority order (US1/US2 P1 → US3/US4 P2 → US5 P3) → polish/validation. Tick a task by changing `- [ ]` to `- [x]`.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel with neighbours (different files, no unmet dependency)
- **[Story]**: US1 first-screen conversion · US2 skimmable trust structure · US3 discoverability (SEO/GEO) · US4 credibility & community · US5 collateral & upkeep
- Paths are repository-relative; `<org>` = final GitHub organization (owner input)

> **Reconciled with feature 005 (2026-08-19).** Done in 005 and ticked below: T040 (owner inputs filled — copyright holder `Derych`, GitHub-only contact — 005 T024; `scripts/check-readme.sh` passes with zero placeholders). Tracked in `specs/005-make-product-usable/tasks.md` rather than here: T014/T027 → 005 T058, T017/T032/T042/T044 → 005 T059, T031 → 005 T063, T041/T043 → 005 T060, T045 → 005 T066. The product was renamed to Intenter and the repository is `Vadym903/Intenter`; the demo/social assets are `assets/demo/intenter.*` and are re-recorded under 005 T058.

## Progress summary

| Phase | Tasks | Done |
|---|---|---|
| 1 Setup | T001–T003 | 3/3 |
| 2 Foundational (license & community files) | T004–T011 | 8/8 |
| 3 US1 first screen | T012–T017 | 4/6 |
| 4 US2 structure & content | T018–T024 | 7/7 |
| 5 US3 discoverability | T025–T029 | 4/5 |
| 6 US4 credibility signals | T030–T033 | 2/4 |
| 7 US5 collateral & upkeep | T034–T039 | 6/6 |
| 8 Polish & validation | T040–T045 | 0/6 |

**Everything not ticked needs something this repository cannot produce**: a tool
that is not installed (`vhs`, an SVG rasteriser), a published GitHub repository,
five human participants, or the owner inputs. Each is listed under
[Outstanding](#outstanding) with what unblocks it.

### Checkpoint 3 (2026-08-17)

Completed this session: the community files (`CODE_OF_CONDUCT.md`, three issue
forms + `config.yml`, `.github/PULL_REQUEST_TEMPLATE.md`, contribution terms in
`CONTRIBUTING.md`); the demo pipeline (`assets/demo/session.sh` +
`agentguard.tape` + `make demo`), verified end to end against the real binary;
`llms.txt`; the whole collateral kit (`pitch.md`, `describe.md`,
`launch-checklist.md`, `repo-settings.md`, `target-queries.md`,
`first-screen-test.md`, `claims-audit.md`, `kit-dry-run.md`, `README.md` index);
`assets/social/preview.svg` + `make social`; `scripts/repo-metadata.sh`;
`scripts/check-readme_test.sh` (15 cases, all rules proved to fire); the
`Makefile` and CI `docs` job wired to all three checks; the changelog entry.

Three defects found and fixed by writing the checks rather than by reading:

1. `check-readme.sh` rule 5 read the license badge's percent-encoded
   `%201.0.0` as the version literal `201.0.0`, so a correct README failed.
2. `check-badges.sh` checked badges inside `<!-- ... -->` guards — the ones
   deliberately disabled until the first release — so CI could never be green.
3. The CI and Go-version badges render "repo not found" until the repository is
   public, which FR-004 forbids shipping; they now sit behind an
   `<!-- after-repository-is-public -->` guard alongside the release badges.

One deviation from `contracts/readme-and-collateral.md`, recorded in
`docs/marketing/first-screen-test.md`: the install block sits **above** the hero
visual rather than below it. With a 1200×640 visual first, the install block
ends around 950 px on a 1440×900 laptop — below the fold, which fails SC-001
outright. Install now ends at ~535 px and the visual starts there.

### Checkpoint 2 (2026-08-16, usage limit)

`README.md` rewritten to the contract structure (hero, badges with `<!-- after-first-release -->` guard, canonical sentence, demo visual, install, TOC, all 15 sections incl. Mermaid flow, comparison table with footnotes, FAQ, status/roadmap, license section with `<CONTACT>` input); `assets/demo/agentguard.svg` (theme-safe hero visual) + `assets/demo/README.md`. Covers T005, T012, T013, T015, T018–T023 (T029 canonical pass done in the opening).

### Checkpoint 1 (2026-08-16, usage limit)

Done — `LICENSE` (PolyForm Noncommercial 1.0.0 with `<COPYRIGHT HOLDER>`/`<CONTACT>` inputs still to fill), `docs/marketing/canonical.md`, `scripts/check-readme.sh` (all contract rules incl. licensing + section order; self-test script pending), `scripts/check-badges.sh`. Decision applied from analysis I1: repository slug is `agentguard/agentguard`; only `<COPYRIGHT HOLDER>`, `<CONTACT>` (and `<YEAR>` if used) are gated placeholders.

---

## Phase 1: Setup (Shared Infrastructure)

- [x] T001 Collect and record owner inputs in `docs/marketing/repo-settings.md` (created here as a stub): copyright-holder name, commercial-licensing contact (email or form URL), final `<org>/agentguard` slug; add these three tokens (`<COPYRIGHT HOLDER>`, `<CONTACT>`, `<org>`) to the CI placeholder grep in `.github/workflows/ci.yml` and `Makefile` `docs-check` so nothing ships with them unfilled
- [x] T002 [P] Create `docs/marketing/canonical.md` with the canonical one-sentence definition (≤ 160 chars) on line 1 and a note that it must appear verbatim in README, repo description, `llms.txt`, social preview and pitch; add `scripts/check-readme.sh` skeleton (arguments: paths; exits non-zero on the first failing rule) wired into `make docs-check`
- [x] T003 [P] Create `assets/demo/` and `assets/social/` directories with `README.md` notes on regeneration commands (`make demo`, `make social`) and size limits (GIF ≤ 3 MB, PNG ≤ 1 MB)

---

## Phase 2: Foundational — licensing and community files (Blocking)

**Purpose**: nothing marketing-facing may ship on top of a placeholder license or missing community files. ⚠️ Blocks user stories.

- [x] T004 Replace `LICENSE` with the header (`AgentGuard`, `Copyright (c) <YEAR> <COPYRIGHT HOLDER>`, `SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0`) followed by the verbatim PolyForm Noncommercial License 1.0.0 text; remove the placeholder paragraph
- [x] T005 [P] Rewrite the README "License" section per `contracts/repo-metadata-license-community.md` (source-available; allowed vs not allowed; commercial contact `<CONTACT>`; "not open-source software in the OSI sense") — temporary location at the end of the current README until the Phase 4 restructure
- [x] T006 [P] Add "Contribution terms" to `CONTRIBUTING.md` (project license, DCO sign-off with `git commit -s`, perpetual relicensing grant incl. commercial licenses, where to ask first) and create `.github/PULL_REQUEST_TEMPLATE.md` (tests, `make docs`, CHANGELOG, terms checkbox, sign-off)
- [x] T007 [P] Create `CODE_OF_CONDUCT.md` (Contributor Covenant 2.1 verbatim, enforcement contact `<CONTACT>`)
- [x] T008 [P] Create `SECURITY.md` (private reporting via GitHub private vulnerability reporting or `<CONTACT>`; supported versions = latest release line; acknowledgement ≤ 3 business days; coordinated disclosure; scope pointer to `docs/security-model.md`)
- [x] T009 [P] Create `SUPPORT.md` (Discussions for questions, Issues for bugs, include `agentguard version` and redacted `agentguard doctor --json`, response expectations)
- [x] T010 [P] Create issue forms `.github/ISSUE_TEMPLATE/bug_report.yml`, `feature_request.yml`, `question.yml` and `config.yml` (`blank_issues_enabled: false`; contact links to Discussions, docs, security reporting) per the contract field lists
- [x] T011 Align `docs/faq.md` with the license decision ("Is it free?", "Can I use it at work or sell it?", "Is it open source?") using the exact wording of the README license section

**Checkpoint**: `LICENSE` real; CoC/SECURITY/SUPPORT/templates present; `make docs-check` passes with owner inputs filled (or fails only on the three tracked placeholders while inputs are pending).

---

## Phase 3: User Story 1 — A visitor understands in 10 seconds and can try it (Priority: P1) 🎯 MVP

**Goal**: first screen = title + tagline + badges + two-sentence why + demo GIF + install one-liners + TOC.

**Independent Test**: rendered README at 1440×900 shows all hero elements without scrolling; demo GIF plays ≤ 30 s; first-screen test protocol ready (`docs/marketing/first-screen-test.md`).

- [x] T012 [US1] Write hero copy in `README.md`: H1, bold tagline (≤ 90 chars, contains the canonical sentence's meaning), two-sentence "why" (≤ 60 words), followed by the install block (macOS/Linux + Windows one-liners copied verbatim from `docs/install.md`, then `agentguard setup claude`) and a linked table of contents in the contract order (sections created as stubs where not yet written)
- [x] T013 [P] [US1] Badge row in `README.md` (CI, license → `LICENSE`, platforms static, Go version; release + downloads badges added behind an `<!-- after-first-release -->` comment to enable in T031) using `<org>`; every badge has alt text
- [ ] T014 [P] [US1] Demo pipeline: `assets/demo/session.sh` (fixture story with the real binary, temp HOME/DataDir, Claude shim: setup `--dry-run` → evaluated hook payload → `agentguard approve` → second evaluation shows `APPROVAL_MATCH` → edit `package.json` → BLOCK → `agentguard history show`), `assets/demo/agentguard.tape` (VHS: 1200×640, neutral theme, ≤ 30 s), `make demo` target (requires `vhs`), committed outputs `agentguard.gif` (≤ 3 MB) and `agentguard.png`
- [x] T015 [US1] Embed the demo in the README hero: GIF with descriptive alt text, caption "recorded from a scripted session", link to the PNG fallback; verify light/dark legibility
- [x] T016 [P] [US1] `docs/marketing/first-screen-test.md`: protocol (5 participants, 10-second exposure at 1440×900, three questions, pass thresholds from SC-001) + results table
- [ ] T017 [US1] Layout check: measure the rendered first screen height (browser dev tools; ≤ ~700 px to the end of the install block); adjust copy/badge wrapping; confirm no horizontal page scroll at 400 px width (code blocks scroll internally)

**Checkpoint**: MVP hero live in the README; demo asset committed and regenerable.

---

## Phase 4: User Story 2 — Skimmable trust structure with linked claims (Priority: P1)

**Goal**: sections in contract order, each ≤ one screen, every claim linked, honest comparison and roadmap.

**Independent Test**: TOC jumps to every section; claim-link audit finds 100% linked; comparison rows verified against docs.

- [x] T018 [US2] Restructure `README.md` body into the contract order: Why (move the existing `package.json` diff + explanation sample here) → How it works → What you get → Compared to alternatives → Install → Set up Claude Code → Try it → CLI at a glance → Security & limitations → Updating → FAQ → Documentation → Status & roadmap → Contributing → License; preserve all currently accurate content (move detail to `docs/` where a section exceeds one screen)
- [x] T019 [P] [US2] Write the "How it works" section: Mermaid flowchart (hook → daemon → parse → resolve wrappers → classify → hard rules → approvals → decision) + three bullets, links to `docs/how-it-works.md`
- [x] T020 [P] [US2] Write the "What you get" section: 6–8 concrete bullets (deterministic/no LLM, local/no telemetry, macOS/Linux/Windows, one-line install, explanations for every allow/block, approvals invalidated on script/config change, self-update prompt, CLI with `--json`) each linked to its doc
- [x] T021 [US2] Write the "Compared to alternatives" table per `contracts/readme-and-collateral.md` (rows: Claude allow rules, bypass/auto mode, approve every time, AgentGuard; columns: prompts over time, survives script change, blocks catastrophic even if allowed, needs an LLM, works offline) with footnotes to `docs/how-it-works.md`, `docs/security-model.md`, `docs/faq.md`; wording reviewed against feature 001 research (never overrides Claude deny rules; Claude's built-in read-only commands don't prompt)
- [x] T022 [P] [US2] Write "Security & limitations" (not a sandbox; gates Bash/PowerShell tools only; prototype; what hard rules cover) and "Status & roadmap" (shipped: Claude Code, 3 OS, installers, updater; Planned: other agents/tools — each prefixed "Planned:")
- [x] T023 [P] [US2] Write the "FAQ" section: 6–8 quotable Q/A per contract (≤ 45 words each; first sentence answers directly), consistent with `docs/faq.md`; "Try it", "CLI at a glance", "Updating", "Documentation", "Contributing" sections kept/trimmed to ≤ one screen each
- [x] T024 [US2] Claim-link audit: list every product claim in README with its backing link in `docs/marketing/claims-audit.md`; fix unlinked or unverifiable claims; wrap version literals in `<!-- example -->…<!-- /example -->`

**Checkpoint**: README complete in contract order; audit shows 100% linked claims.

---

## Phase 5: User Story 3 — Discoverable and correctly described (SEO/GEO) (Priority: P2)

**Goal**: repository metadata set, `llms.txt`, quotable canonical facts, target-query list with baseline.

**Independent Test**: metadata visible on the repo page; `llms.txt` at the raw URL contains the canonical sentence; `docs/marketing/target-queries.md` has ≥ 10 rows with baseline entries.

- [x] T025 [US3] Create `llms.txt` at repository root per `data-model.md` §6 (H1, blockquote = canonical sentence, 3–6 quotable facts incl. license terms and what it is not, Docs links, Optional spec link); add to lychee scope
- [x] T026 [P] [US3] `docs/marketing/repo-settings.md`: description (canonical sentence ≤ 160 chars), topics list (≥ 8, ≤ 20 from research R-05), website (docs index URL), social preview upload steps, Discussions enablement (Q&A/Ideas/Show and tell), community-profile check; `scripts/repo-metadata.sh` (`gh repo edit <org>/agentguard --description … --homepage … --add-topic …`, idempotent, prints manual steps for preview/Discussions)
- [ ] T027 [P] [US3] Social preview: `assets/social/preview.svg` (1280×640; name, tagline/canonical sentence, differentiator, install one-liner; readable at 600 px), `make social` → `assets/social/preview.png` (≤ 1 MB); commit both
- [x] T028 [P] [US3] `docs/marketing/target-queries.md`: ≥ 10 queries (search phrases + AI-assistant questions such as "how to stop Claude Code asking permission without allowing everything", "AI coding agent command allowlist that survives script changes", "Claude Code hooks permission layer"), intent, intended landing section/anchor, baseline table (date, engine, result) with +30/+60 day rows to fill
- [x] T029 [US3] Canonical-facts pass over the README opening: ensure lines 1–15 state name, definition, problem, capabilities, platforms/agent, install command and license terms in plain declarative sentences; verify the canonical sentence appears verbatim in README, `llms.txt`, `preview.svg` and `docs/marketing/pitch.md` (T034) — enforced by `scripts/check-readme.sh` canonical rule

**Checkpoint**: discoverability package complete; baseline procedure ready for publication day.

---

## Phase 6: User Story 4 — Credible and safe to adopt (Priority: P2)

**Goal**: live badges, community profile 100%, licensing consistency visible.

**Independent Test**: GitHub Insights → Community standards 100%; all badges resolve; LICENSE/README/badge/FAQ agree.

- [x] T030 [US4] `scripts/check-badges.sh`: extract every `img.shields.io` URL from README and require HTTP 200 (curl, retries, 10 s timeout); wire into `make docs-check` and the CI `docs` job
- [ ] T031 [US4] Enable the release/downloads badges after the first published release (remove the `<!-- after-first-release -->` guard); confirm the latest-release badge equals the newest release tag; document in `docs/release-process.md` that badges need no manual updates
- [ ] T032 [P] [US4] Community profile pass: verify description, README, license, CoC, contributing, security policy, issue templates, PR template all detected by GitHub (Insights → Community standards); fix any file naming/location GitHub does not recognize; record in `docs/marketing/repo-settings.md`
- [x] T033 [P] [US4] Licensing consistency check in `scripts/check-readme.sh`: README license section mentions "PolyForm Noncommercial 1.0.0", `LICENSE` first lines contain the SPDX id and no `<COPYRIGHT HOLDER>`, badge text matches, README/`llms.txt` contain no `open[- ]source software|is open source`, exactly one commercial contact occurrence in README

**Checkpoint**: credibility signals verifiable by CI + GitHub community profile.

---

## Phase 7: User Story 5 — Collateral kit and upkeep (Priority: P3)

**Goal**: reusable, approved copy and assets; CI keeps README truthful.

**Independent Test**: kit files complete; `make docs-check` covers all rules in the contract; a sample post uses only kit text.

- [x] T034 [US5] `docs/marketing/pitch.md` (tagline; ≤ 140 chars; ≤ 50 words; ≤ 150 words; licensing sentence) and `docs/marketing/describe.md` (approved 2–3 sentence description + link set + licensing sentence for posts/directories)
- [x] T035 [P] [US5] `docs/marketing/launch-checklist.md`: pre-flight (release published, badges green, community profile 100%, social preview unfurl test on Slack/X/LinkedIn, `llms.txt` reachable), channels (Show HN, r/ClaudeAI, r/commandline, X/LinkedIn, dev.to, awesome-claude-code lists — OSI-only lists marked n/a), post-launch (query baseline, issue triage rota)
- [x] T036 [P] [US5] `scripts/check-readme.sh` rules: no empty alt text (`![](`), canonical sentence present in README/`llms.txt`/`pitch.md`/`preview.svg`, no `v?[0-9]+\.[0-9]+\.[0-9]+` outside `<!-- example -->` blocks, `assets/demo/agentguard.gif` ≤ 3 MB, `assets/social/preview.png` ≤ 1 MB, placeholders absent; unit-style self-test with fixture READMEs (`scripts/check-readme_test.sh`)
- [x] T037 [US5] Extend `Makefile` (`docs-check` runs `check-badges.sh` + `check-readme.sh`; `demo`; `social`; `help` updated) and `.github/workflows/ci.yml` `docs` job accordingly; ensure the job passes offline-friendly (badge check `continue-on-error` only on network failure, hard-fail on 404)
- [x] T038 [P] [US5] `CHANGELOG.md` `[Unreleased]`: license decision, README restructure, community files, collateral, `llms.txt`
- [x] T039 [US5] Dry-run the kit: draft one social post and one directory submission using only `pitch.md`/`describe.md`; record any missing copy and fix the kit (SC-008)

---

## Phase 8: Polish & validation

- [x] T040 (done in 005 T024) Fill owner inputs (holder, contact, org) everywhere and confirm `make docs-check` passes with zero placeholders
- [ ] T041 [P] Run the first-screen test (`docs/marketing/first-screen-test.md`) with 5 developers; record results; iterate hero copy if < 4/5 (SC-001)
- [ ] T042 [P] Apply repository metadata (`scripts/repo-metadata.sh` or manual), upload the social preview, enable Discussions; run unfurl tests on Slack, X and LinkedIn; record in `repo-settings.md` (SC-005)
- [ ] T043 [P] Record the GEO baseline in `docs/marketing/target-queries.md` on publication day (search engine + one AI assistant per query) and schedule +30/+60 day re-checks (SC-007)
- [ ] T044 Review the rendered README in light and dark themes on desktop and mobile; fix contrast/wrapping; verify demo GIF legibility (SC-006, FR-019)
- [ ] T045 Final review against `spec.md` SC-001…SC-008; open follow-up features for the landing site (page metadata/JSON-LD/sitemap) and translations if wanted

---

## Dependencies & Execution Order

- Phase 1 → Phase 2 sequential; Phase 2 blocks stories (a placeholder license must not go public).
- Phase 3 (US1) then Phase 4 (US2) — both edit `README.md`, so run sequentially (or one person); Phase 5 (US3) can proceed in parallel with Phase 4 except T029 (needs the final README opening); Phase 6 (US4) after Phase 3; Phase 7 (US5) after Phases 4–5 (checks depend on final structure); Phase 8 last.

### Parallel opportunities

- T002/T003 ∥ T001; T005–T010 ∥ T004; T013/T014/T016 ∥ T012; T019/T020/T022/T023 ∥ T018 (different sections — coordinate merges); T026–T028 ∥ T025; T032/T033 ∥ T030; T035/T036/T038 ∥ T034; T041–T043 ∥.

## Implementation Strategy

1. **License first** — nothing marketing-facing before `LICENSE` is real and wording is consistent.
2. **MVP hero** (Phase 3) gives the biggest conversion win; structure (Phase 4) follows.
3. **Discoverability and checks** (Phases 5–7) lock in consistency before publication; **validation** (Phase 8) evidences the success criteria.

## Notes

- Owner inputs (holder, contact, org) are required; the CI placeholder check is the gate.
- Never claim "open source"; never present planned integrations as shipped; keep every claim linked.
- Commit after each task; reference the task id (e.g. `T021`) in commit messages.

## Outstanding

Eleven tasks are not ticked. None is waiting on a decision or on more code — each
needs a tool, a service, a person or an owner input that the repository cannot
supply. What exists for each is listed so nothing has to be rediscovered.

| Task | Done | Blocked on | What unblocks it |
|---|---|---|---|
| T014 demo pipeline | `session.sh` (verified end to end against the real binary: setup dry-run → ASK → approve → `APPROVAL_MATCH` → script change → BLOCK → `history show`, with no temporary path or user name in any frame), `agentguard.tape`, `make demo` | `vhs` is not installed | `brew install vhs`, then `make demo`; commit `agentguard.gif` and `agentguard.png`. Until then `assets/demo/agentguard.svg` is the hero visual and the size rules in `check-readme.sh` simply have nothing to measure. |
| T017 first-screen height | Layout budget computed and recorded in `first-screen-test.md`; hero reordered so the install block ends at ~535 px | no browser | Open the rendered page at 1440×900, measure to the end of the install block, record the real figure. Also confirm no horizontal page scroll at 400 px. |
| T027 social preview | `assets/social/preview.svg` (1280×640, canonical sentence, install one-liner), `make social`, upload steps in `repo-settings.md` | no SVG rasteriser | `brew install librsvg`, then `make social`; commit `preview.png`. |
| T031 release badges | Both guards in `README.md`, the enabling procedure in `docs/release-process.md#badges`, the pre-flight item in `launch-checklist.md` | no published repository or release | Uncomment each group at its moment; `check-badges.sh` reports how many are still hidden. |
| T032 community profile | Every file GitHub looks for exists (`CODE_OF_CONDUCT.md`, `SECURITY.md`, `CONTRIBUTING.md`, `LICENSE`, `.github/ISSUE_TEMPLATE/`, `.github/PULL_REQUEST_TEMPLATE.md`); the checklist is in `repo-settings.md` | repository is not public | Insights → Community standards; tick the list there. |
| T040 owner inputs | Every occurrence tracked; `check-readme.sh` fails while any remains | copyright holder and contact not supplied | Fill `<COPYRIGHT HOLDER>`, `<CONTACT>`, `<YEAR>`; `scripts/check-readme.sh` then passes. |
| T041 first-screen test | Protocol, scoring, layout budget and results template | five participants | Run it; iterate the hero copy if fewer than 4 of 5 pass Q1. |
| T042 repository metadata | `scripts/repo-metadata.sh` (dry-run verified), values and click paths in `repo-settings.md` | `gh` not installed, repository not public | `scripts/repo-metadata.sh`, then the four manual steps it prints. |
| T043 GEO baseline | 14 queries with intent and landing section, baseline and +30/+60 tables | nothing published to measure | Record the baseline before the first post — after it, there is no baseline. |
| T044 theme and mobile review | Statically theme-safe: the hero SVG carries its own background and explicit colors, the Mermaid diagram inherits GitHub's theme, no CSS or embed assumes a theme | no browser | Read the rendered page in both themes on desktop and phone. |
| T045 final review | The SC status below | the tasks above | Re-check after each of them lands. |

### Success criteria status

| SC | Status |
|---|---|
| SC-001 first-screen test | **Pending** — the page is built for it and the layout budget says it fits; the test itself is T041. |
| SC-002 claims linked, no broken links/badges/placeholders | **Met in structure** — `claims-audit.md` covers all 44 claims and fixed the three unlinked ones; `check-readme.sh`, `check-readme_test.sh` and `check-badges.sh` pass; placeholders remain by design until T040. |
| SC-003 community profile 100% | **Pending** the public repository (T032); every file it checks for exists. |
| SC-004 licensing consistency | **Met**, and enforced: LICENSE/README/badge/FAQ agree, zero "open source" self-descriptions, exactly one commercial contact. Zero placeholders lands with T040. |
| SC-005 repository metadata | **Prepared** — description, 14 topics, website, preview source and unfurl procedure; applying it is T042, the PNG is T027. |
| SC-006 demo renders in both themes, ≤ 30 s, no personal data | **Met for the SVG** (own background, fixture paths only). The GIF's 30-second and 3 MB budgets are asserted by the tape and the size rule but unmeasured until T014. |
| SC-007 ≥ 10 target queries with a baseline | **Met for the list** (14 queries); the baseline is T043. |
| SC-008 no new copy for repeat actions | **Met** — `kit-dry-run.md` assembled a post and a directory entry from the kit alone; the two gaps it exposed were closed in `pitch.md`. |

### Follow-ups worth opening as their own features

- **Landing site** — page metadata, JSON-LD, sitemap, a hosted `/llms.txt`. Out of scope here by the spec, and the repository website field points at `docs/` until it exists.
- **Translations** — the canonical page is English only, by decision. Worth revisiting only after the query baseline shows where the readers are.
- **DCO status check** — the contribution terms require a sign-off; a bot enforcing it would remove the manual review step.
