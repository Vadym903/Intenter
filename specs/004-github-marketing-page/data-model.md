# Data Model: GitHub Marketing Page

**Feature**: `004-github-marketing-page` · **Date**: 2026-08-16

No runtime data. The "entities" are repository artifacts, GitHub settings values and recorded measurements; each has fields, sources and validation rules.

## 1. README structure (`README.md`)

| Order | Section | Required content | Validation |
|---|---|---|---|
| 0 | Hero | H1 + bold tagline; badge row; two-sentence "why"; demo GIF (alt text, fallback link); install one-liners (macOS/Linux, Windows) + `agentguard setup claude`; TOC | first screen ≤ ~700 px at 1440×900; badges resolve; alt text present |
| 1 | Why | problem (permission fatigue vs blanket rules), the `package.json` diff story, the "explanation" sample | claims linked |
| 2 | How it works | Mermaid flow + 3 bullets (hard rules, read-only baseline, fingerprinted approvals) | links to `docs/how-it-works.md` |
| 3 | What you get | 6–8 concrete bullets (deterministic/no LLM, local/no telemetry, cross-platform, one-line install, explanations, invalidation, self-update prompt, CLI/JSON) | each bullet links |
| 4 | Compared to alternatives | table per research R-10 | factual, linked |
| 5 | Install | one-liners, brew, winget (pending), link to `docs/install.md` | no version literals outside `<!-- example -->` |
| 6 | Set up Claude Code | `agentguard setup claude` + restart note | |
| 7 | Try it | 5-step outline + link to getting-started | |
| 8 | CLI at a glance | table (existing) | links to `docs/cli/` |
| 9 | Security & limitations | not a sandbox, gates shell tools only, prototype; link security model | plain statements |
| 10 | Updating | prompt sample (`<!-- example -->`), commands | |
| 11 | FAQ | 6–8 quotable Q/A (GEO) | canonical sentence reused |
| 12 | Documentation | index (existing) | |
| 13 | Status & roadmap | prototype status; shipped vs planned clearly separated | no "planned" item worded as shipped |
| 14 | Contributing | link + contribution terms one-liner | |
| 15 | License | PolyForm Noncommercial terms sentence + commercial contact | badge/LICENSE agree; no "open source" self-description |

## 2. Canonical sentence (`docs/marketing/canonical.md`, line 1)

Single sentence definition (≤ 160 chars) reused verbatim in: README tagline paragraph, repository description, `llms.txt` blockquote, `assets/social/preview.svg`, `docs/marketing/pitch.md`. Validation: CI grep.

## 3. Badges

| Badge | Source | Condition |
|---|---|---|
| CI | shields `github/actions/workflow/status/<org>/agentguard/ci.yml?branch=main` | always |
| Release | `github/v/release/<org>/agentguard` | after first release |
| License | static `License-PolyForm%20Noncommercial%201.0.0-blue` → `LICENSE` | always |
| Platforms | static `macOS \| Linux \| Windows` | always |
| Go version | `github/go-mod/go-version/<org>/agentguard` | always |
| Downloads | `github/downloads/<org>/agentguard/total` | after first release |

Validation: each URL returns 200 (`scripts/check-badges.sh`).

## 4. Repository metadata (`docs/marketing/repo-settings.md`)

| Field | Value | Applied by |
|---|---|---|
| Description | canonical sentence (≤ 160 chars) | maintainer / `scripts/repo-metadata.sh` (`gh repo edit`) |
| Topics | ≥ 8, ≤ 20 from research R-05 | same |
| Website | docs index URL (until landing site) | same |
| Social preview | `assets/social/preview.png` 1280×640 (from `preview.svg`) | maintainer upload |
| Discussions | enabled; categories Q&A, Ideas, Show and tell | maintainer |
| Community profile | 100% | derived |

## 5. Demo asset

`assets/demo/agentguard.tape` (VHS script) + `assets/demo/session.sh` (fixture story using the real binary, Claude shim, temp HOME) → `assets/demo/agentguard.gif` (≤ 30 s, ≤ 3 MB, neutral fixture names) + `assets/demo/agentguard.png` (first frame). Regeneration: `make demo` (requires `vhs`). Validation: file sizes, no personal paths (grep for `/Users/`, `C:\Users\` outside fixture names), alt text in README.

## 6. `llms.txt` (repository root)

```
# AgentGuard
> <canonical sentence>
<3–6 quotable facts: what it does, how it decides, platforms/agents, install command, license terms, what it is not>
## Docs
- [README](…) - overview and install
- [Getting started](docs/getting-started.md) - …
- [How it works](docs/how-it-works.md) - …
- [Security model](docs/security-model.md) - …
- [CLI reference](docs/cli/README.md) - …
- [Install](docs/install.md) - …
- [License](LICENSE) - PolyForm Noncommercial 1.0.0
## Optional
- [Specification](specs/001-agentguard-prototype/PROTOTYPE_SPEC.md)
```
Validation: canonical sentence present; links resolve (lychee).

## 7. Community-health files

| File | Required content |
|---|---|
| `LICENSE` | header (copyright holder, year, SPDX id) + verbatim PolyForm Noncommercial 1.0.0 |
| `CODE_OF_CONDUCT.md` | Contributor Covenant 2.1 + contact |
| `SECURITY.md` | how to report (private), supported versions, response time, scope pointer to security model |
| `SUPPORT.md` | where to ask, what to include (`agentguard doctor --json`) |
| `CONTRIBUTING.md` (existing) | + "Contribution terms" (license acceptance, DCO sign-off, relicensing grant) |
| `.github/ISSUE_TEMPLATE/{bug_report,feature_request,question}.yml`, `config.yml` | forms with environment fields (OS, version, `agentguard doctor --json`) |
| `.github/PULL_REQUEST_TEMPLATE.md` | checklist incl. contribution terms |

## 8. Collateral kit (`docs/marketing/`)

`pitch.md` (tagline; ≤ 140 chars; ≤ 50 words; ≤ 150 words), `describe.md` (approved description + licensing sentence for posts/directories), `launch-checklist.md` (channels, OSI-only ones marked n/a, pre-flight checks), `repo-settings.md`, `target-queries.md`, `first-screen-test.md`; assets under `assets/social/`, `assets/demo/`.

## 9. Target query record (`docs/marketing/target-queries.md`)

| Field | Notes |
|---|---|
| query | search phrase or AI-assistant question |
| intent | what the visitor wants |
| landing section | README anchor / doc |
| baseline (date, engine, result) | recorded at publication |
| follow-ups | +30 d, +60 d rows |

## 10. Automated checks (extension of `docs` CI job / `make docs-check`)

badges resolve · no "open source" self-description in README/`llms.txt` · no empty alt text · canonical sentence consistency · no version literals outside `<!-- example -->` blocks · placeholders (`TODO(`, `<COPYRIGHT HOLDER>`, `<CONTACT>`, `<org>`) absent · lychee links · markdownlint.
