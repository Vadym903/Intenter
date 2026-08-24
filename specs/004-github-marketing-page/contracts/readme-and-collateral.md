# Contract: README Content, Copy Rules & Collateral

## Copy rules (apply to README, `llms.txt`, repo description, social preview, collateral)

1. Canonical sentence (from `docs/marketing/canonical.md`, line 1) appears verbatim in every surface listed in data-model §2.
2. Never describe the project as "open source"; use "source-available" and state: "free for personal and noncommercial use; selling or commercial use requires a separate license".
3. Planned integrations (Codex, Cursor, VS Code, JetBrains, other tools) appear only under "Status & roadmap" and are prefixed with "Planned:".
4. winget: "available once the manifest is accepted upstream" until it is.
5. Claude Code comparisons state facts verified in feature 001 research (e.g., Claude enforces its own deny rules; AgentGuard never overrides them; Claude's built-in read-only commands run without prompting).
6. No version literals except inside `<!-- example -->…<!-- /example -->` blocks (update prompt sample, pin example).
7. Every image has descriptive alt text; the demo has a caption "recorded from a scripted session".
8. Every claim in "What you get" and "Compared to alternatives" links to a backing doc.

## Hero copy (final wording produced in implementation; constraints)

- Tagline ≤ 90 characters, states category + differentiator (e.g., "A permission layer for AI coding agents that approves what a command *does*, not what it is called.").
- "Why" paragraph ≤ 60 words: problem sentence + difference sentence.
- Install block: exactly the documented one-liners from feature 002 (copied verbatim by CI script from `docs/install.md` canonical block or vice versa) + `agentguard setup claude`.

## Demo asset contract

- Source: `assets/demo/agentguard.tape` + `assets/demo/session.sh`; output `assets/demo/agentguard.gif` (≤ 30 s, ≤ 3 MB, 1200×640) and `assets/demo/agentguard.png`.
- Story: approve once (`agentguard approve`) → auto-allowed (`history` shows `APPROVAL_MATCH`) → script changed → BLOCK with explanation (`history show`).
- Neutral fixture data only (`/home/dev/proj` style paths or workspace-relative), no real usernames.
- `make demo` regenerates; README embeds the GIF with alt text and links the PNG fallback.

## Comparison table contract

Rows: "Claude Code allow rules (`Bash(npm run *)`)", "Bypass / auto mode", "Approve every time", "AgentGuard". Columns: "Prompts over time", "Survives script change", "Blocks catastrophic commands even if allowed", "Needs an LLM", "Works offline". Cells: ✓/✗/short phrase; footnotes link to `docs/how-it-works.md`, `docs/security-model.md`, `docs/faq.md`.

## FAQ contract (README §11)

6–8 Q/A, each answer ≤ 45 words, quotable, first sentence answers directly: What is AgentGuard? Does it use an LLM to decide? Is it a sandbox? Which agents/tools does it support? Is it free? Can I use it at work / sell it? How is it different from allowing `npm run *`? Does it send data anywhere?

## Collateral kit contract (`docs/marketing/`)

| File | Content |
|---|---|
| `canonical.md` | line 1: canonical sentence; below: allowed variants (none by default) |
| `pitch.md` | tagline; pitch ≤ 140 chars; ≤ 50 words; ≤ 150 words; each ends with the licensing sentence where length allows |
| `describe.md` | approved 2–3 sentence description for posts/directories + link set + licensing sentence |
| `launch-checklist.md` | pre-flight (release published, badges green, community profile 100%, social preview unfurl test), channels (Show HN, r/ClaudeAI, r/commandline, X/LinkedIn, dev.to, awesome-claude-code lists — OSI-only lists marked n/a), post-launch (query baseline, issues triage) |
| `repo-settings.md` | description, topics, website, social preview upload, Discussions, community profile — with the `gh repo edit` helper |
| `target-queries.md` | ≥ 10 queries with intent, landing section, baseline table |
| `first-screen-test.md` | protocol + results table (SC-001) |

## Automated checks contract (`make docs-check` / CI `docs` job)

| Check | Fails when |
|---|---|
| lychee | any broken link (existing) |
| markdownlint | style violations (existing config) |
| placeholders | `TODO(`, `<COPYRIGHT HOLDER>`, `<CONTACT>`, `<org>` present |
| badges | any `img.shields.io` URL in README ≠ HTTP 200 |
| wording | `open[- ]source software` or `is open source` in README/`llms.txt` |
| alt text | `![](` present in README |
| canonical | canonical sentence missing from README, `llms.txt`, `pitch.md`, `preview.svg` |
| version drift | `v?[0-9]+\.[0-9]+\.[0-9]+` in README outside `<!-- example -->` blocks |
| demo size | `assets/demo/agentguard.gif` > 3 MB |
