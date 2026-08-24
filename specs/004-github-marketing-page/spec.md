# Feature Specification: GitHub Marketing Page (README, Discoverability & Community Presence)

**Feature Branch**: `004-github-marketing-page`

**Created**: 2026-08-16

**Status**: Draft

**Input**: User description: "we need to create an attractive and successful from the marketing perspective main page in github, modify readme MD so that we have: clear docs, short and clear explanation why do our user need our app, configured SEO and GEO, all other required for the marketing stuff"

**Clarifications recorded (2026-08-16)**: (Q3, answered) **License** — source-available and non-commercial: anyone may use AgentGuard for their own personal/non-commercial purposes; selling it or using it commercially is not permitted without a separate license — implemented with the **PolyForm Noncommercial 1.0.0** license text (owner supplies the copyright-holder name). (Q1, default applied) **Scope** — the GitHub README plus repository metadata is the main page; a standalone landing site is a follow-up feature. (Q2, default applied) **"GEO"** — Generative Engine Optimization: being found and described correctly by AI answer engines; no localization/translations.

**Context**: AgentGuard is implemented (features 001–003): one binary, one-line installers, docs, self-update. The GitHub repository page — whose face is `README.md` — is where nearly every prospective user first meets the product. Today the README is accurate and complete but not built to convert: no headline value proposition above the fold, no visual proof, no badges/social proof, no repository metadata (description, topics, social preview), a `LICENSE` placeholder that contradicts the README's "MIT", no community-health files, and nothing that helps search engines or AI answer engines describe the project correctly. This feature turns the repository into a marketing-grade landing page while keeping every claim truthful and every command copy-pasteable.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A visitor understands in 10 seconds why they need it and how to try it (Priority: P1)

A developer lands on the repository (from search, an AI answer, a social post or a link). Above the fold they see one line saying what AgentGuard is, one line saying why it matters to them (permission fatigue vs. blanket permissions; approvals that survive script changes), a visual proof (a short demo recording or an annotated before/after), and the one-line install command for their OS. Within a minute they know whether it is for them and how to try it.

**Why this priority**: The whole point of a marketing page; everything else amplifies it.

**Independent Test**: Show the rendered README to five developers unfamiliar with the project for 10 seconds; at least four can say what it does and who it is for; all can find the install command without scrolling past the first screen on a laptop.

**Acceptance Scenarios**:

1. **Given** the repository page, **When** a visitor reads only the first screen (title, tagline, badges, hero visual, install), **Then** they can state the product's purpose, its key differentiator (approves *effects*, not command strings), and the platforms it supports.
2. **Given** the first screen, **When** the visitor wants to try it, **Then** the install command for macOS/Linux and Windows and the single next step (`agentguard setup claude`) are visible without scrolling on a typical laptop viewport.
3. **Given** the demo visual, **When** it plays/renders, **Then** it shows the core story (approve once → auto-allowed → script changes → blocked with explanation) in under 30 seconds and renders on GitHub without external services.

---

### User Story 2 - The page answers "why", "how", "is it safe", "what does it not do" in a skimmable structure (Priority: P1)

Below the fold the README follows a conventional, scannable structure: problem/why → how it works (one diagram or three bullets) → what you get (feature bullets with concrete claims) → comparison with string allowlists / permission modes → install & setup → try it → CLI at a glance → security & limitations → updating → docs index → status/roadmap → contributing → license. Every claim links to the doc or spec that backs it.

**Why this priority**: Conversion depends on trust; a security tool must be honest and specific.

**Independent Test**: A reviewer can navigate from any question in the list above to the answering section via the table of contents within one click; every claim has a link; no section exceeds one screen.

**Acceptance Scenarios**:

1. **Given** the README, **When** a visitor opens the table of contents, **Then** each of the listed sections exists in that order and each heading links to it.
2. **Given** the "what you get" and comparison sections, **When** a reviewer checks the claims, **Then** every claim is verifiable against the docs/spec and the comparison never misrepresents alternatives (Claude's own permission rules, string allowlists, bypass mode).
3. **Given** the security & limitations section, **When** a skeptical reader looks for what the tool does *not* do, **Then** it is stated plainly (not a sandbox; gates shell tools only; prototype status).

---

### User Story 3 - The project is discoverable and correctly described by search and AI answer engines (Priority: P2)

Someone searching for terms like "Claude Code permission fatigue", "AI coding agent command allowlist", "approve rm -rf safely agent", or asking an AI assistant "how do I stop Claude Code asking permission every time without allowing everything" finds AgentGuard, and the search snippet / AI answer describes it accurately with the correct install command and links.

**Why this priority**: Organic discovery is the main acquisition channel for a developer tool; being described *wrongly* by an AI engine is worse than not being found.

**Independent Test**: Repository metadata (description, topics, website, social preview) is set; the README's first paragraph and a machine-readable summary file state the canonical facts; a checklist of target queries is documented with the intended landing section; the page validates against a link/format checker.

**Acceptance Scenarios**:

1. **Given** the repository settings, **When** viewed, **Then** the description is a single sentence with the primary keywords, the topics list contains the target keywords, the website field points at the canonical page, and a social preview image is set (renders on Twitter/X, LinkedIn, Slack, Discord unfurls).
2. **Given** the README and the machine-readable summary, **When** an AI engine or crawler reads them, **Then** it finds: name, one-sentence definition, problem solved, key features, supported platforms/agents, install command, license, links to docs — stated in plain, quotable sentences near the top.
3. **Given** the target-query checklist, **When** each query is entered in a search engine or AI assistant after publication, **Then** the result mentions AgentGuard with a correct description (tracked over time; baseline recorded).

---

### User Story 4 - The repository looks alive, credible and safe to adopt (Priority: P2)

Visitors see the signals they expect from a maintained source-available project: badges (build status, latest release, license, platforms; more only when their sources exist), a decided license whose terms are stated plainly (free for personal and non-commercial use; commercial use by separate license), clear contribution terms, a security policy, a code of conduct, contribution guide, issue/PR templates, discussions or a clear support channel, a changelog and release notes, and a roadmap/status that manages expectations (prototype, Claude Code only today).

**Why this priority**: Credibility gaps are the most common reason developers bounce; each item is cheap.

**Independent Test**: The GitHub "community standards" checklist for the repository shows all items complete; badges render and are green; `LICENSE` contains a real license matching the README and badge.

**Acceptance Scenarios**:

1. **Given** the repository, **When** the community profile is checked, **Then** description, README, license, code of conduct, contributing, security policy, issue templates and pull request template are all present.
2. **Given** the badges, **When** the page renders, **Then** every badge resolves (no broken images) and reflects live status; none is decorative-only.
3. **Given** the license, **When** a visitor checks `LICENSE`, the README license section and the badge, **Then** all three agree (PolyForm Noncommercial 1.0.0), the file contains the full license text with the copyright holder, the README states in one sentence what is and is not allowed, and a commercial-use contact is given.
4. **Given** a prospective contributor, **When** they open the contributing guide or PR template, **Then** the contribution terms (license acceptance and rights grant) are stated before they submit.

---

### User Story 5 - Marketing collateral is reusable and stays truthful over time (Priority: P3)

The maintainers have a small collateral kit: the tagline and elevator pitch in three lengths, the demo recording source, the social preview image source, a "how to describe AgentGuard" snippet for posts/awesome-lists/directories, and a launch checklist (where to submit, what to post). Automated checks keep the README free of broken links, stale version numbers and placeholder text.

**Why this priority**: Prevents drift and makes launches/repeat posts fast.

**Independent Test**: The collateral files exist in the repo; the docs check job fails on broken links/placeholders; the pitch text is used consistently across README, repo description and social preview.

**Acceptance Scenarios**:

1. **Given** the collateral kit, **When** a maintainer prepares a post or directory submission, **Then** they copy approved text (short/medium/long) and assets without rewriting.
2. **Given** a change to the README, **When** CI runs, **Then** broken links, leftover placeholders and version numbers that disagree with the latest release are caught before merge.

---

### Edge Cases

- Visitors on mobile or narrow viewports: the hero, badges and install commands must remain readable; wide tables and images must not force horizontal scrolling of the whole page.
- Dark and light GitHub themes: hero image/diagram must be legible in both (theme-aware assets or neutral palette).
- Media that GitHub cannot render (external players, scripts): the demo must be a plain image/animated image or a static frame with a link — no external embed dependency.
- Claims about integrations that do not exist yet (Codex, Cursor, VS Code, JetBrains): must be worded as roadmap, not features.
- Badges depending on services that are not configured yet (downloads, report card, coverage): only add badges whose source exists; add the rest when their source is live.
- The winget channel is not yet available upstream: state "available once accepted", never present it as installable today.
- Localization: the English page is canonical; translated READMEs are out of scope (GEO here means Generative Engine Optimization).
- Non-commercial license consequences: GitHub's license detector may show "Other"; some directories/awesome-lists require an OSI-approved license (the launch checklist marks them not applicable); wording must never say "open source"; contributors must see the contribution terms before submitting.
- README length: marketing sections must not push essential docs (install, setup) far down; keep the page under one long scroll and move detail to `docs/`.
- Screenshots/recordings contain paths or usernames: use neutral fixture names, no personal data.
- Accessibility: images have alt text; contrast in the hero visual; no information conveyed by color alone.

## Requirements *(mandatory)*

### Functional Requirements

**Above the fold**

- **FR-001**: The README MUST open with the project name, a one-sentence tagline stating what AgentGuard is, and a one-sentence "why" stating the problem it solves for developers using AI coding agents (permission fatigue vs. blanket permissions; approvals that follow the command's *effects*).
- **FR-002**: The first screen MUST include a visual proof of the core story (a short demo recording or an annotated before/after) that renders natively on GitHub, in light and dark themes, in under 30 seconds, using neutral fixture data.
- **FR-003**: The first screen MUST show the one-line install command for macOS/Linux and for Windows and the single next step, and MUST state supported platforms and the currently supported agent.
- **FR-004**: The README MUST show badges for build status, latest release, license and supported platforms, and MAY add further badges only when their source is live; every badge MUST resolve.

**Structure and content**

- **FR-005**: The README MUST follow this section order with a linked table of contents: Why → How it works → What you get → Compared to alternatives → Install → Set up Claude Code → Try it → CLI at a glance → Security & limitations → Updating → Documentation → Status & roadmap → Contributing → License. Existing accurate content MUST be preserved or moved to `docs/`, not deleted.
- **FR-006**: Every product claim in the README MUST link to the document or specification that backs it, and comparisons with alternatives MUST be factual and specific.
- **FR-007**: The "Status & roadmap" section MUST state honestly what is prototype-level, which agents/tools are gated today, and what is planned, worded so that planned items cannot be mistaken for shipped features.
- **FR-008**: The README MUST stay skimmable: no section longer than one laptop screen; details live in `docs/` with links.

**Discoverability (SEO) and answer-engine readiness (GEO)**

- **FR-009**: Repository metadata MUST be set: a one-sentence description with primary keywords, a topics list covering the target keywords, the website field, and a social preview image that unfurls correctly on major platforms.
- **FR-010**: (GEO = Generative Engine Optimization) The README's opening MUST contain the canonical facts in plain, quotable sentences (name, definition, problem solved, key capabilities, supported platforms/agents, install command, license terms) so search snippets and AI answers describe the project correctly; a machine-readable summary file for AI/LLM crawlers MUST be provided at a conventional location and kept in sync with the README; a short FAQ-style block MUST answer the most common questions in the same quotable style; naming and the one-sentence definition MUST be identical across README, repository description, machine-readable summary and collateral.
- **FR-011**: A target-query list (search phrases and AI-assistant questions the page should answer) MUST be documented with the README section intended to satisfy each, and a baseline of current results MUST be recorded for later comparison.
- **FR-012**: The "main page" of this feature is the GitHub repository page — `README.md` plus repository metadata (description, topics, website field, social preview). A standalone landing site with page-level metadata (title, meta description, Open Graph/Twitter cards, structured data, sitemap) is explicitly a follow-up feature; the README MUST NOT depend on it (no dead "website" links until it exists — the website field points at the repository or docs until then).

**Credibility, licensing and community**

- **FR-013**: The repository MUST carry a decided, non-commercial source-available license: full **PolyForm Noncommercial 1.0.0** text in `LICENSE` with the owner-supplied copyright holder, matching the README license section and the license badge. The README MUST describe the terms plainly ("free for personal and non-commercial use; selling or commercial use requires a separate license — contact …") and MUST NOT describe the project as "open source" (it is source-available); a "Commercial use" pointer with a contact channel MUST be present.
- **FR-014**: Contribution terms MUST be stated (in `CONTRIBUTING.md` and the PR template): contributors agree to the project license and grant the owner the rights needed to offer commercial licenses (a short contributor license statement or a Developer Certificate of Origin plus license grant — wording chosen at implementation with the owner).
- **FR-015**: Community-health files MUST exist and be linked: contributing guide (exists), code of conduct, security policy (how to report vulnerabilities, supported versions), issue templates (bug, feature request, question), pull request template, and a support/discussion channel statement.
- **FR-016**: Release notes and the changelog MUST be linked from the README, and the latest-release badge MUST match the newest published release.

**Collateral and upkeep**

- **FR-017**: A collateral kit MUST be maintained in the repository: tagline, elevator pitch in three lengths (≤ 140 chars, ≤ 50 words, ≤ 150 words), the demo recording source and rebuild instructions, the social preview image source, an approved "how to describe AgentGuard" snippet (including the licensing sentence), and a launch/submission checklist that flags directories requiring an OSI-approved license as not applicable.
- **FR-018**: Automated checks MUST fail on broken README links, leftover placeholders, badges that do not resolve, README/version drift against the latest release, and any occurrence of "open source" as a self-description; the demo and social image MUST be regenerable from sources kept in the repository.
- **FR-019**: All README media MUST have alt text; the page MUST remain readable on narrow viewports and in both GitHub themes.

### Key Entities

- **README (landing page)**: the marketing-structured `README.md` with hero, TOC, sections, badges, media, links.
- **Repository metadata**: description, topics, website, social preview image — set in repository settings and documented so they can be re-applied.
- **Demo asset**: the recording/animation source and its rendered artifact; neutral fixture content; theme-safe.
- **Community-health files**: LICENSE (real text), CODE_OF_CONDUCT, SECURITY policy, issue/PR templates, CONTRIBUTING (existing).
- **Machine-readable summary**: the AI/crawler-oriented summary of canonical facts, kept in sync with the README.
- **Collateral kit**: pitch texts, description snippet, launch checklist, asset sources.
- **Target-query list**: search/AI queries with intended landing sections and recorded baseline results.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In a 10-second first-screen test with five developers new to the project, at least 4 of 5 correctly state what AgentGuard does and who it is for, and 5 of 5 locate the install command without scrolling on a laptop viewport.
- **SC-002**: 100% of README product claims link to a backing document; 0 broken links, 0 unresolved badges, 0 placeholder markers at merge time (enforced automatically).
- **SC-003**: The GitHub community-standards checklist shows 100% of items complete (description, README, license, code of conduct, contributing, security policy, issue templates, PR template).
- **SC-004**: `LICENSE`, the README license section and the license badge agree on PolyForm Noncommercial 1.0.0, `LICENSE` contains the full text with the copyright holder (0 placeholders), and the README contains 0 occurrences of "open source" as a self-description and exactly one commercial-use contact.
- **SC-005**: Repository metadata is complete (description ≤ 160 characters with primary keywords, ≥ 8 relevant topics, website set, social preview renders in unfurl tests on at least 3 platforms).
- **SC-006**: The demo asset renders on GitHub in both themes, tells the core story in ≤ 30 seconds, and contains no personal data.
- **SC-007**: A documented list of ≥ 10 target queries exists with a recorded baseline; within 60 days of publication at least 5 of them return AgentGuard with a correct description (search or AI assistant), measured and recorded.
- **SC-008**: Repeat marketing actions (a post, a directory submission) require no new copywriting: 100% of text comes from the collateral kit.

## Assumptions

- The repository will be public on GitHub under the final organization/name; metadata (description, topics, website, social preview) is applied by a maintainer via repository settings and documented so it can be re-applied (GitHub has no repository-metadata-as-code for these fields).
- "Attractive" means the clean, conventional structure developers expect from well-run source-available/open-source project pages, with one strong visual and a consistent tone — no heavy graphics; the README remains readable as plain Markdown.
- The demo visual is generated from a scripted session against a fixture project (neutral names) so it can be regenerated when output formats change; it is stored in the repository (small, theme-safe) rather than served externally.
- The winget channel is described as pending until accepted upstream; Homebrew tap and script installers are presented as available.
- The English README is canonical; translations are not part of this feature (GEO = Generative Engine Optimization, decided).
- The existing docs (`docs/*`, CLI reference, CONTRIBUTING, CHANGELOG) remain the detail layer; the README links rather than duplicates.
- Discussions (or an equivalent support channel) can be enabled by a maintainer; the README states where to ask questions.
- License: PolyForm Noncommercial 1.0.0 is used verbatim; the owner supplies the copyright-holder name (and, if desired, a commercial-licensing contact address) at implementation time — both are required inputs, not placeholders to ship. Contribution terms are a short contributor license statement chosen with the owner (DCO + license grant is the default suggestion).
- Homebrew tap and winget accept non-OSI licenses; Homebrew *core* and OSI-only directories are not targets.

### Out of scope

- Paid promotion, analytics/telemetry embedded in the README (GitHub does not run scripts; no tracking pixels), newsletter or website forms.
- Translations/localized READMEs.
- A standalone landing site with page-level SEO metadata and a documentation website — both are follow-up features (decided: README + repository metadata now).
- Directory listings and posts themselves (Hacker News, Product Hunt, awesome-lists) — the kit prepares them; executing a launch campaign is operational work outside this spec.
- Legal review of the license choice and commercial-license terms/pricing.
