# Claim audit

Every product claim the README makes, and the document that backs it. A claim
here is a statement a reader could be wrong about after believing it — not a
description of the page ("the CLI reference is in docs/cli") and not an
instruction ("restart your Claude sessions").

The rule the audit enforces: **a claim a reader might doubt carries a link to
where it is demonstrated**. Marketing copy earns trust by being checkable, and
the check has to be one click away or it is decorative.

Re-run this when the README changes. `scripts/check-readme.sh` catches empty alt
text, drifted wording and stray version literals, but no script can tell whether
a sentence is true — that is what this file is for.

## Audit

| # | Section | Claim | Backing |
|---|---|---|---|
| 1 | Hero | "a local, deterministic permission layer … approves what a command actually does" | [How it works](../how-it-works.md) — resolution and the decision order |
| 2 | Hero | "re-asks the moment a remembered command starts doing something else" | [Fingerprints and invalidation](../how-it-works.md#fingerprints-and-invalidation) |
| 3 | Hero | "Works with Claude Code today on macOS, Linux and Windows" | [Install](../install.md) — the three platforms and their installers |
| 4 | Why | Approving `npm run cleanup` approves whatever the script contains today | [Resolution](../how-it-works.md#resolution-what-does-this-command-actually-do) |
| 5 | Why | The approval stops applying when the script changes, with an explanation of what changed | [Fingerprints and invalidation](../how-it-works.md#fingerprints-and-invalidation) |
| 6 | Why | "fewer prompts *and* a stricter gate than any allowlist" | [How it works](../how-it-works.md) · [Security model](../security-model.md) |
| 7 | How it works | Hard rules stop a set of actions no approval can override — home deletes, system writes, credential reads, protected-branch force-pushes, download-to-shell pipes | [What it is for](../security-model.md#what-it-is-for) |
| 8 | How it works | Reads inside the project are allowed without asking | [The decision](../how-it-works.md#the-decision) |
| 9 | How it works | Approvals record resolved effects plus a fingerprint of every mutable input | [Exact and semantic approvals](../how-it-works.md#exact-and-semantic-approvals) |
| 10 | How it works | "there is no language model in the decision path" | [Why is there no AI in it](../faq.md#why-is-there-no-ai-in-it) |
| 11 | What you get | Approve once, equivalent actions stop asking across sessions | [Exact and semantic approvals](../how-it-works.md#exact-and-semantic-approvals) |
| 12 | What you get | Approvals expire when script, target, scope or build config changes | [Fingerprints and invalidation](../how-it-works.md#fingerprints-and-invalidation) |
| 13 | What you get | A safety floor no approval can lower | [What it is for](../security-model.md#what-it-is-for) |
| 14 | What you get | An explanation for every allow and block | [`intenter history show`](../cli/intenter_history_show.md) |
| 15 | What you get | "no LLM, no telemetry, no account; decisions never touch the network" | [What stays on your machine](../security-model.md#what-stays-on-your-machine) |
| 16 | What you get | One-line install on three platforms, checksum-verified, upgradable and removable with the same command | [Install](../install.md) |
| 17 | What you get | Updates ask first | [Updating](../updates.md) |
| 18 | What you get | Every list and show command takes `--json` | [CLI reference](../cli/README.md) |
| 19 | Compared to alternatives | Claude's own rules match command text and are enforced by Claude; Intenter never overrides a deny rule | footnote → [Importing "don't ask again"](../how-it-works.md#importing-dont-ask-again) |
| 20 | Compared to alternatives | Auto mode reviews tool calls with a classifier, needs the network, and does not remember what a script resolved to | footnote → [What about bypass mode](../faq.md#what-about-bypass-mode) |
| 21 | Compared to alternatives | Intenter's approvals stop matching when inputs change | footnote → [Fingerprints and invalidation](../how-it-works.md#fingerprints-and-invalidation) |
| 22 | Compared to alternatives | Hard rules are evaluated before approvals and cannot be switched off by one | footnote → [What it is for](../security-model.md#what-it-is-for) |
| 23 | Install | Both installers verify a checksum before installing and put the binary on PATH | [Install](../install.md) |
| 24 | Install | Homebrew tap available | [Package managers](../install.md#package-managers) |
| 25 | Install | winget "available once the manifest is accepted upstream" | [Package managers](../install.md#package-managers) |
| 26 | Set up Claude Code | `setup claude` backs up settings, adds hooks alongside existing ones, starts the daemon, installs the update check, self-tests | [`intenter setup claude`](../cli/intenter_setup_claude.md) |
| 27 | Try it | The five-step walkthrough ends in a blocked command | [Getting started](../getting-started.md) |
| 28 | Security & limitations | Not a sandbox; an allowed command runs with your privileges | [Limitations](../security-model.md#limitations) |
| 29 | Security & limitations | Gates the `Bash` and `PowerShell` tools, not the file-edit tools | [Limitations](../security-model.md#limitations) |
| 30 | Security & limitations | Unresolvable commands are handed back to Claude's prompt rather than guessed | [The decision](../how-it-works.md#the-decision) |
| 31 | Security & limitations | A daemon that is down means "ask", never "allow" | [Fail-safe behavior](../security-model.md#fail-safe-behavior) |
| 32 | Updating | "Not now" is quiet for a day, "skip" for that version; `INTENTER_NO_UPDATE_CHECK=1` disables it; nothing runs in scripts, CI or Claude sessions | [Updates](../updates.md) |
| 33 | FAQ | No LLM in the decision path, same answer offline | [Why is there no AI in it](../faq.md#why-is-there-no-ai-in-it) |
| 34 | FAQ | Not a sandbox | [Security model](../security-model.md) |
| 35 | FAQ | Claude Code only; other agents planned, not shipped | README "Status & roadmap" |
| 36 | FAQ | Free for noncommercial use; commercial use needs a separate license | README "License" · [LICENSE](../../LICENSE) |
| 37 | FAQ | A string rule keeps matching after the script changes; an approval does not | [Fingerprints and invalidation](../how-it-works.md#fingerprints-and-invalidation) |
| 38 | FAQ | No telemetry, accounts or model calls; only an anonymous release check, switchable | [Does it send anything anywhere](../faq.md#does-it-send-anything-anywhere) |
| 39 | FAQ | If the daemon is down, Claude's own permission flow takes over | [Fail-safe behavior](../security-model.md#fail-safe-behavior) |
| 40 | Status & roadmap | Shipped: Claude Code on three platforms, npm/pnpm/yarn/Gradle/Maven resolution, hard rules, approvals with invalidation, installers, Homebrew tap, winget manifest, update check | [Changelog](../../CHANGELOG.md) · [Install](../install.md) |
| 41 | Status & roadmap | Prototype status: one agent, shell tools only, schema may change before 1.0 | [Changelog](../../CHANGELOG.md) |
| 42 | Status & roadmap | Planned items are not available today | stated in the section; nothing to link, and nothing may be presented as shipped |
| 43 | Contributing | Contributions accepted under the project license with sign-off and a relicensing grant | [Contribution terms](../../CONTRIBUTING.md#contribution-terms) |
| 44 | License | Source-available under PolyForm Noncommercial 1.0.0; noncommercial use allowed, commercial use needs a separate license; not open-source in the OSI sense | [LICENSE](../../LICENSE) |

## True of the code? — verdicts (2026-08-18)

A link proves a claim is *documented*; this pass checked each one against the
code and the security review, after the rename and the audit fixes. `yes` = the
code provides it as stated; `bounded` = true within a limit that the page now
states; `pending` = true once the named step has happened, and the page says so
until then. No row is `no`.

| # | Verdict | Reworded to / evidence |
|---|---|---|
| 1, 2, 4, 5, 6, 9, 10, 11, 12, 14, 15, 17, 18, 19, 20, 21, 22, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 41, 42, 43, 44 | yes | unchanged; each links to the page that describes the mechanism, and the invariant/e2e suites cover it (`go test ./... -run 'TestInvariant_\|S0\|S1'`) |
| 3 | pending | "on macOS, Linux and Windows": built and tested for all three; the three-OS CI run and the per-OS validation record are the evidence, referenced from *Status & roadmap* once they exist. Until then the section says the CI result "is only observable once the repository is public". |
| 7, 13, 22 | bounded | The hard rules judge the effects the resolver produced. README *How it works* now says they apply "to every effect Intenter can see in the resolved command" and that an unexamined line "is forced to a prompt rather than trusted" (rule R13); `docs/security-model.md#what-it-is-for` explains the resolver-completeness bound; the new *Limitations* paragraph covers opaque command strings. |
| 8 | yes | read-only baseline is `RESOLVED`-only, workspace-scoped, no sensitive/traversal/symlink/network flags (`internal/policy/baseline.go`, S1). |
| 16, 23 | bounded | "checksum-verified" is unconditional; the release signature is verified by the installers only when `cosign`/`openssl`/.NET is available, always by the updater — README *What you get* and *Install* say exactly that; `docs/install.md#verifying-a-download-by-hand` shows the by-hand check. |
| 24, 25, 40 | pending | Homebrew tap and winget manifest are generated by the pipeline but nothing is published before the first release; README *Install* and *Status & roadmap* say "not yet public / nothing is published until the first release". Becomes `yes` at `v0.1.0` (tap) and on upstream acceptance (winget). |
| 26 | yes | setup steps as listed; hook entries left by a pre-rename development install are additionally replaced (see the changelog's rename entry). |
| FAQ "What about bypass mode?" | bounded | reworded: the floor holds in bypass mode "for any command Intenter can resolve"; an opaque `bash -c '…'` string is unresolved and, in that mode, run by Claude as bypass mode intends. |
| security-model "Nothing is signed yet" | superseded | replaced by "Releases are signed, binaries are not code-signed" — notarization/Authenticode remain undone and are stated as such. |

## Fixes made by this audit

| Claim | Was | Now |
|---|---|---|
| 3 | "Works with Claude Code today on macOS, Linux and Windows" — no link, and platform support is exactly the kind of claim a reader checks before installing | links to [install.md](../install.md) |
| 24, 25 | Homebrew and winget stated as bare facts; the winget one is a *pending* fact, which is the most important kind to be able to verify | both link to [package managers](../install.md#package-managers) |
| 26 | The setup section listed six things the command does with nothing to check them against | links to the [generated command reference](../cli/intenter_setup_claude.md) |
| hero caption | "Recorded from a scripted session" — but the embedded visual is `intenter.svg`, a hand-drawn panel, not a recording. A caption is a claim, and this one was false | says the image illustrates the scripted session; [`assets/demo/README.md`](../../assets/demo/README.md) records that the caption reverts to "recorded from" when the GIF replaces the SVG |

## Claims deliberately left unlinked

- **The `npm run cleanup` example output** (the invalidation explanation block).
  It is a sample of real output, and the section around it links to how it works.
  Linking every code block would make the page harder to read for no gain.
- **"Planned:" items.** There is nothing to link to — that is the point of the
  prefix, and a link would imply the opposite.
- **Definitional FAQ answers** ("What is Intenter?"). They restate the
  canonical sentence, which the rest of the page backs.

## Version literals

The README contains release version numbers in exactly one place: the update
prompt sample, wrapped in `<!-- example -->…<!-- /example -->` so
`scripts/check-readme.sh` accepts it and so no reader mistakes it for the
current release. The license version (`1.0.0`) is part of the license's name,
not a release, and is stripped before the rule runs.
