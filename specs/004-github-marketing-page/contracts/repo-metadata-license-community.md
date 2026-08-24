# Contract: Repository Metadata, License, Contribution Terms, Community Files

## Repository metadata (maintainer-applied; recorded in `docs/marketing/repo-settings.md`)

| Field | Value / rule |
|---|---|
| Description | canonical sentence, ≤ 160 chars |
| Topics | `claude-code`, `claude`, `ai-coding-agent`, `ai-agents`, `permissions`, `guardrails`, `security`, `developer-tools`, `cli`, `golang`, `allowlist`, `agent-safety`, `hooks`, `devsecops` (edit freely; ≥ 8, ≤ 20) |
| Website | docs index URL until a landing site exists (never a dead link) |
| Social preview | `assets/social/preview.png` (1280×640, ≤ 1 MB) from `assets/social/preview.svg`; contains name, tagline, differentiator, install one-liner; readable at 600 px wide |
| Discussions | enabled with Q&A / Ideas / Show and tell |
| Helper | `scripts/repo-metadata.sh` runs `gh repo edit <org>/agentguard --description "…" --homepage "…" --add-topic …` (idempotent); social preview and Discussions remain manual steps documented with exact click paths |

## License files

- `LICENSE`: header lines `AgentGuard`, `Copyright (c) <YEAR> <COPYRIGHT HOLDER>`, `SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0`, blank line, then the verbatim PolyForm Noncommercial License 1.0.0 text.
- README "License" section (≤ 4 sentences): source-available under PolyForm Noncommercial 1.0.0; allowed: personal and other noncommercial use, copying, modification, sharing under the same terms; not allowed without a separate license: selling, commercial use; contact `<CONTACT>` for commercial licensing; "not open-source software in the OSI sense".
- Badge: `License-PolyForm%20Noncommercial%201.0.0-blue` linking to `LICENSE`.
- `docs/faq.md` and README FAQ: "Is it free?" / "Can I use it at work or sell it?" answered consistently.
- Placeholders `<YEAR>`, `<COPYRIGHT HOLDER>`, `<CONTACT>` are required inputs; CI fails while they remain.

## Contribution terms

- `CONTRIBUTING.md` § "Contribution terms": (1) contributions are licensed under the project license; (2) contributors certify the Developer Certificate of Origin (`git commit -s`, `Signed-off-by`); (3) contributors grant the maintainers a perpetual, worldwide, irrevocable right to license their contribution under other terms, including commercial licenses; (4) how to ask questions before contributing.
- `.github/PULL_REQUEST_TEMPLATE.md`: checklist — tests pass, docs regenerated (`make docs`), CHANGELOG entry, "I agree to the contribution terms in CONTRIBUTING.md and my commits are signed off".
- Optional later: DCO status check app.

## Community-health files

| File | Content requirements |
|---|---|
| `CODE_OF_CONDUCT.md` | Contributor Covenant 2.1 verbatim, enforcement contact `<CONTACT>` |
| `SECURITY.md` | report privately (GitHub private vulnerability reporting or `<CONTACT>`), no public issues for vulns, supported versions = latest release line, acknowledgement ≤ 3 business days, disclosure coordination, pointer to `docs/security-model.md` for what is/isn't in scope |
| `SUPPORT.md` | Discussions (Q&A) for questions, Issues for bugs, include `agentguard version` + `agentguard doctor --json` (redact paths), response expectations |
| `.github/ISSUE_TEMPLATE/bug_report.yml` | fields: what happened, expected, steps, OS/arch, `agentguard version`, Claude Code version, `agentguard doctor --json` (redacted), logs |
| `.github/ISSUE_TEMPLATE/feature_request.yml` | problem, proposal, alternatives, willingness to contribute |
| `.github/ISSUE_TEMPLATE/question.yml` | redirects to Discussions with a short form |
| `.github/ISSUE_TEMPLATE/config.yml` | `blank_issues_enabled: false`; contact links: Discussions, docs, security reporting |

Acceptance: GitHub "Community standards" checklist shows every item complete.

## `llms.txt`

As in `data-model.md` §6; served at `https://raw.githubusercontent.com/<org>/agentguard/main/llms.txt` (and by the future landing site at `/llms.txt`).
