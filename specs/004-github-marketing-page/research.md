# Research: GitHub Marketing Page (Phase 0)

**Feature**: `004-github-marketing-page` · **Date**: 2026-08-16 · **Inputs**: `spec.md` (decisions: PolyForm Noncommercial license; README + repository metadata; GEO = Generative Engine Optimization), current `README.md` (feature 002 rewrite), `CONTRIBUTING.md`, `docs/*`, `.github/workflows/ci.yml` docs job (lychee, markdownlint, placeholder grep), `Makefile` (`docs`, `docs-check`).

Format: **Decision / Rationale / Alternatives**. **[repo]** = verified against the repository.

---

## R-01 First screen ("hero") composition

- **Decision**: Order at the top of `README.md`: (1) H1 `AgentGuard` with a one-line tagline in bold; (2) badge row (CI, latest release, license, platforms, Go version, downloads-total once releases exist); (3) one-paragraph "why" (two sentences: the problem, the difference); (4) demo GIF (with alt text and a "static frame" fallback link); (5) two install commands (macOS/Linux, Windows) + `agentguard setup claude`; (6) a "Table of contents" list. Everything above the demo must fit in the first laptop screen (~700 px); the current README's diff-based explanation **[repo]** moves under "Why" right after the TOC.
- **Rationale**: FR-001…FR-004, SC-001; conventional developer-tool landing pattern (badges → what/why → visual → install) is what visitors scan for.
- **Alternatives**: centered HTML hero with logo (fragile in Markdown renderers, no logo yet), tabs for OS install (GitHub Markdown has no tabs).

## R-02 Demo asset

- **Decision**: A scripted terminal recording made with **VHS** (`charmbracelet/vhs`): `assets/demo/agentguard.tape` → `assets/demo/agentguard.gif` (≤ 30 s, ≤ 3 MB, 1200×640, neutral theme readable on light/dark) plus a static first-frame `assets/demo/agentguard.png` used as fallback link. The tape drives a **fixture script** (`assets/demo/session.sh`) that replays the canonical story using the real binary against a fixture project and a Claude *shim* (setup `--dry-run`, an evaluated hook payload, `agentguard approve`, `history show` after the script changes) — labelled in the README caption as "recorded from a scripted session". `make demo` regenerates it when `vhs` is installed; CI does not require it (asset is committed).
- **Rationale**: FR-002/SC-006; VHS is deterministic and reproducible; GIF renders natively on GitHub without external embeds; the fixture keeps paths/usernames neutral.
- **Alternatives**: asciinema player (external embed — not rendered by GitHub), hand-made screenshots (drift), SVG animation (poor tooling).

## R-03 How-it-works visual

- **Decision**: A Mermaid flowchart in the README (`hook → daemon → parse → resolve wrappers → classify → hard rules → approvals → decision`), which GitHub renders natively and theme-aware, plus three bullets. No raster diagram to maintain.
- **Rationale**: Theme-safe, editable, no assets; FR-005/FR-008.
- **Alternatives**: PNG/SVG diagram (theme and upkeep cost).

## R-04 Badges (only with live sources)

- **Decision**: shields.io badges: CI (`github/actions/workflow/status/<org>/agentguard/ci.yml?branch=main`), latest release (`github/v/release/<org>/agentguard`), license (static: `License-PolyForm%20Noncommercial%201.0.0-blue`, linking to `LICENSE`), platforms (static: `macOS%20%7C%20Linux%20%7C%20Windows`), Go version (`github/go-mod/go-version/<org>/agentguard`), downloads (`github/downloads/<org>/agentguard/total`, added once the first release exists). Deferred until sources exist: Go Report Card, coverage, Homebrew/winget version badges. A CI check fetches every badge URL and requires HTTP 200.
- **Rationale**: FR-004/SC-002; decorative badges harm credibility.

## R-05 Repository metadata & social preview

- **Decision**: Description (≤ 160 chars): "Semantic permission layer for AI coding agents: approve what a command does, not its string. Local, deterministic Claude Code hooks." Topics (≥ 8, ≤ 20): `claude-code`, `claude`, `ai-coding-agent`, `ai-agents`, `permissions`, `guardrails`, `security`, `developer-tools`, `cli`, `golang`, `allowlist`, `agent-safety`, `hooks`, `devsecops`. Website field: the docs index (`docs/README.md` rendered URL) until a landing site exists. Social preview: 1280×640 PNG exported from `assets/social/preview.svg` (name, tagline, three-word differentiator, install one-liner), uploaded in Settings → Social preview. All values recorded in `docs/marketing/repo-settings.md` (checklist with screenshots-free instructions) because GitHub has no metadata-as-code for these fields.
- **Rationale**: FR-009/SC-005; the description and topics are what GitHub search, Google and AI engines index first.
- **Alternatives**: `gh api` script to set description/topics (fine as a helper; social preview still manual) — included as an optional `scripts/repo-metadata.sh` using `gh repo edit --description --add-topic`.

## R-06 GEO (Generative Engine Optimization) package

- **Decision**: (a) canonical facts paragraph in README lines 1–15 (name, definition, problem, capabilities, platforms/agents, install, license terms) written as plain declarative sentences; (b) `llms.txt` at repository root following the llms.txt convention (H1 name, blockquote summary, sections with links to README/docs/CLI reference/security model/license/install), plus `docs/llms-full.md`? — no: keep only `llms.txt` (small) to avoid drift; (c) a "FAQ" section in README with 6–8 quotable Q/A (What is it? Does it use an LLM? Is it a sandbox? Which agents? Is it free? Can I use it at work? How is it different from allowing `npm run:*`? Does it send data anywhere?); (d) identical one-sentence definition string reused in README, repo description, `llms.txt`, social preview and collateral (checked by CI via a shared snippet file `docs/marketing/canonical.md` and a grep); (e) `docs/marketing/target-queries.md` with ≥ 10 queries, intended landing section, and a baseline table (date, engine, result) recorded manually at publication and at +30/+60 days.
- **Rationale**: FR-010/FR-011/SC-007; AI engines quote short factual sentences and FAQ blocks; consistency across surfaces is what makes descriptions converge.
- **Alternatives**: JSON-LD/structured data (impossible in README; landing-site follow-up), sitemap (n/a).

## R-07 License implementation (PolyForm Noncommercial 1.0.0)

- **Decision**: `LICENSE` = verbatim PolyForm Noncommercial 1.0.0 text preceded by a header block: `AgentGuard — Copyright (c) <YEAR> <COPYRIGHT HOLDER>. Licensed under the PolyForm Noncommercial License 1.0.0 (SPDX: PolyForm-Noncommercial-1.0.0).` README "License" section: "AgentGuard is source-available under the PolyForm Noncommercial License 1.0.0: you may use, copy, modify and share it for personal and other noncommercial purposes; selling it or using it for commercial purposes requires a separate commercial license — contact <CONTACT>. It is not open-source software in the OSI sense." Badge as R-04. `docs/faq.md`/README FAQ gain "Is it free? / Can I use it at work?" answers. Owner inputs (holder, contact) are collected before merge; the CI placeholder check fails on `<COPYRIGHT HOLDER>`/`<CONTACT>` left in place.
- **Rationale**: user decision (Q3); PolyForm NC is a purpose-written, plain-language noncommercial license with an SPDX identifier and explicit room for separate commercial licensing.
- **Alternatives**: Commons Clause + MIT (permits internal commercial use — contradicts the decision), Prosperity (30-day commercial trial), BUSL (time-delayed open source), CC BY-NC (not for software).

## R-08 Contribution terms

- **Decision**: `CONTRIBUTING.md` gains a "Contribution terms" section: contributions are accepted under the project license, and by contributing you certify the DCO (sign-off) and grant the maintainers a perpetual, worldwide right to license your contribution under other terms, including commercial licenses. PR template includes the checkbox; a `Signed-off-by` line is required (documented; enforcement via a DCO check MAY be enabled later). Owner may swap this for a formal CLA.
- **Rationale**: FR-014; without a grant the owner could not offer commercial licenses covering contributions.

## R-09 Community-health files

- **Decision**: `CODE_OF_CONDUCT.md` (Contributor Covenant 2.1, contact = <CONTACT>), `SECURITY.md` (private reporting via GitHub Security Advisories or email; supported versions = latest release; acknowledgement within 3 business days), `SUPPORT.md` (Discussions/Issues, what to include, `agentguard doctor --json`), `.github/ISSUE_TEMPLATE/bug_report.yml`, `feature_request.yml`, `question.yml`, `config.yml` (blank issues off, links to docs/discussions/security), `.github/PULL_REQUEST_TEMPLATE.md` (checklist: tests, docs regenerated, changelog, contribution terms). Enable Discussions (maintainer action, recorded in `repo-settings.md`).
- **Rationale**: FR-015/SC-003 (GitHub community profile 100%).

## R-10 Comparison content (must be factual)

- **Decision**: A compact table "How AgentGuard compares" with rows: Claude Code allow rules (`Bash(npm run *)`), bypass/auto modes, manual approval every time, AgentGuard — columns: prompts over time, survives script change, blocks catastrophic commands even when allowed, needs an LLM, works offline. Every cell backed by `docs/how-it-works.md`/`docs/security-model.md`/feature 001 research (e.g., "Claude enforces its own deny rules; AgentGuard never overrides them").
- **Rationale**: FR-006; the differentiator must be shown against real alternatives without misrepresenting Claude Code.

## R-11 Automated upkeep checks

- **Decision**: Extend the existing `docs` CI job **[repo: lychee + markdownlint + placeholder grep]** and `make docs-check` with: badge URL check (`scripts/check-badges.sh` — every `img.shields.io` URL in README returns 200); wording check (`! grep -Ei 'open[- ]source software|is open source' README.md llms.txt`); alt-text check (no `![](`); canonical-sentence consistency (`docs/marketing/canonical.md` line 1 must appear verbatim in README, `llms.txt`, and `docs/marketing/pitch.md`); version-drift check (README contains no `vX.Y.Z` literals except inside `<!-- example -->…<!-- /example -->` blocks); placeholder grep extended to `<COPYRIGHT HOLDER>`, `<CONTACT>`, `<org>`.
- **Rationale**: FR-017/FR-018, SC-002/SC-004.

## R-12 First-screen and query measurements

- **Decision**: `docs/marketing/first-screen-test.md` — protocol (5 participants, 10-second exposure of the rendered README at 1440×900, three questions) and results table; `docs/marketing/target-queries.md` baseline captured on publication day and re-checked at +30/+60 days by a maintainer (manual, dated rows).
- **Rationale**: SC-001/SC-007 are outcome metrics that need a recorded procedure.

## R-13 What stays out

- Landing site with page metadata (follow-up), translations, docs website, launch execution, legal review of license terms.
