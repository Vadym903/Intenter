# Definition of Done — review

Against `specs/001-agentguard-prototype/PROTOTYPE_SPEC.md` §30. Automated items
are checked; the rest name what is still needed and who can do it.

**Reviewed**: 2026-08-16 · **Build**: `0.1.0-dev` · **Suite**: 21 packages green
(unit, e2e, installer), `go vet` clean, cross-compiles for all six targets.

## The eight items

| # | Requirement | State |
|---|---|---|
| 1 | `setup claude` reports every step ✓ | **Automated.** `TestSetupInstallsHooksAndPreservesTheRest`, `TestSetupIsIdempotent`, `TestSetupBacksUpBeforeEditing`, and the S13 hook-binary scenario. **Manual run outstanding** — see below. |
| 2 | `npm run cleanup` resolves to `rm -rf ./dist`, `FS_DELETE`, `WORKSPACE_GENERATED`, summary shown, native dialog prompts | **Automated.** `TestS3FirstRunDefersWithASummary` and the rest of `s03_npm_cleanup_test.go`, through the real binary and real hook JSON. |
| 3 | A persistent approval via "Yes, and don't ask again" **or** `intenter approve` | **Automated.** Both paths: `s11_consent_import_test.go` covers the Claude-rule import at PreToolUse and at PostToolUse; `TestS3ApproveThenAutoAllow` covers the CLI. |
| 4 | A new session auto-allows with `APPROVAL_MATCH`/`RULE_IMPORT` | **Automated.** `TestS11ImportHappensOnlyOnce`, `TestS1ApprovalCoversEquivalentInvocations`. |
| 5 | Changed script → not reused, BLOCK with explanation; `./src` variant → ASK with the mismatch | **Automated.** `s10_invalidation_test.go` and `TestS11AnImportedApprovalStopsMatchingWhenTheScriptChanges`. |
| 6 | S1–S13 pass in CI on three OSes; every invariant has a named test | **Automated.** All thirteen scenarios have e2e tests; `go test ./... -run TestInvariant_` covers I-1…I-17, and `TestEveryInvariantInTheSpecHasATest` fails if the specification declares one that does not. **CI runs on all three OSes; the three-OS result is only observable once the repository is public.** |
| 7 | `uninstall claude` restores Claude to a working state, removing only Intenter entries | **Automated.** `TestUninstallRestoresTheSettings`, `TestUninstallKeepsHooksThatAreNotOurs`, `TestUninstallLeavesTheFileEquivalent`, plus the installer suite's byte-identical rc-file check across three install/uninstall cycles. |
| 8 | README documents install, setup, the demo, limitations, and the resolved open questions | **Done.** README plus the nine pages under `docs/`; limitations in `docs/security-model.md#limitations`; Appendix B resolutions carried into `docs/how-it-works.md` and `docs/faq.md`. The docs CI job fails on a stale CLI reference, a broken link or a leftover to-do/placeholder marker, and `TestDocsSmokeCommandsStillWork` runs the walkthrough's commands against the real binary. |

## Performance (§29)

Measured on this machine; the budgets are the specification's.

| Path | Budget | Measured (p95) |
|---|---|---|
| `evaluate`, warm | 100 ms | 0.1–0.6 ms |
| Hook round trip, end to end | 500 ms | 5–7 ms |
| Hook round trip, daemon unreachable | 500 ms | 6 ms |

`TestEvaluateLatency` and `TestHookRoundTripLatency` assert these on every run
and log the actual numbers, so a regression is visible before it is felt.

## What is not done

Each needs something this environment cannot provide. The artifact each one
requires is in place.

| Item | Needs | Ready |
|---|---|---|
| Manual validation on macOS, Linux, Windows (items 1–5, and item 6's three-OS claim) | Three machines and a real Claude Code session | `docs/validation-template.md` |
| First release (`v0.1.0-rc.1`, then `v0.1.0`) | A public repository | `.github/workflows/release.yml`, verified-before-publish |
| Homebrew channel | The tap repository and its token | `packaging/homebrew/TAP-SETUP.md` |
| winget channel | Upstream review by Microsoft | Manifest generated per release; zip attached until accepted |
| Newcomer walkthrough timing (SC-006) | A second person | `docs/validation-template.md` §4 |

## Follow-ups worth filing

Found during implementation, out of scope for the prototype:

- **A vanity install domain.** The one-liners point at `raw.githubusercontent.com`,
  which works but reads as provisional and ties the documented command to a
  hosting choice.
- **Code signing and notarization.** Releases are unsigned, so macOS Gatekeeper
  and Windows SmartScreen both warn on a manual download. The installers avoid
  the warning; a user who downloads by hand does not.
- **Scoop** for Windows users who prefer it to winget.
- **Approval portability** — sharing a project's approvals across a team, or
  moving them between machines. Today they are per user, per machine, per
  project path.
- **Gating the file-editing tools.** `Write` and `Edit` bypass the shell, so
  Intenter never sees them. This is the largest gap in coverage and is
  documented as such.

---

[Documentation](README.md) · [Release process](release-process.md)
