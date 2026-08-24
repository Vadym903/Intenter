# Quickstart & Validation Guide: One-Line Installation & Documentation

Purpose: prove the feature end-to-end — a published release, the four one-liners on three OSes, upgrade/pin/uninstall, and docs checks. Contracts: `contracts/installer.md`, `contracts/release-artifacts.md`, `contracts/docs-and-checks.md`.

## Prerequisites

- The repository is public on GitHub with at least one tagged release (`v0.1.0` or later) produced by the release workflow (all six archives + `checksums.txt` present).
- Fresh machines/VMs or CI runners for macOS, Linux (any distro incl. Alpine/WSL2) and Windows 10/11.
- For the docs smoke: Go ≥ 1.22 and `make` locally.

## 1. Release publishing

```bash
git tag v0.1.0 && git push origin v0.1.0
```
Expected within 30 minutes: `release.yml` runs build → verify-installers (3 OSes against the built artifacts; each install < 60 s) → publish → post-verify; the GitHub release "v0.1.0" (not draft, not pre-release) has the six archives, `checksums.txt`, notes linking `CHANGELOG.md`; Homebrew tap commit; winget PR or `winget-manifest.zip`; post-verify remote install-test green on 3 OSes. If post-verify fails, the release is automatically demoted to pre-release and "latest" still points at the previous stable version.

## 2. One-line install (fresh machine)

macOS / Linux:
```bash
curl -fsSL https://raw.githubusercontent.com/agentguard/agentguard/main/install.sh | sh
```
Windows (PowerShell):
```powershell
irm https://raw.githubusercontent.com/agentguard/agentguard/main/install.ps1 | iex
```
Expected: `verified sha256 …`, `AgentGuard 0.1.0 installed to …`, `Next step: agentguard setup claude`; in a **new** terminal `agentguard version` prints `0.1.0`; total wall time < 60 s.

## 3. Upgrade / pin / uninstall

```bash
# pin an older version, then upgrade to latest
curl -fsSL …/install.sh | sh -s -- --version 0.1.0
curl -fsSL …/install.sh | sh                       # → "upgraded from 0.1.0"; daemon restarted if registered
agentguard doctor                                  # no "daemon version ≠ CLI version" finding
curl -fsSL …/install.sh | sh -s -- --uninstall     # hooks/service removed, binary gone, PATH block gone, data kept
```
Windows equivalents use `& ([scriptblock]::Create((irm …/install.ps1))) -Version 0.1.0` / `-Uninstall`.

## 4. Package managers

```bash
brew install agentguard/tap/agentguard && agentguard version
```
`winget install AgentGuard.AgentGuard` once the upstream PR is merged (until then: `winget install --manifest <unzipped winget-manifest>`).

## 5. Homebrew upgrade coherence (regression for research R-06)

```bash
agentguard setup claude          # after brew install
brew upgrade agentguard          # simulate with a newer tap release
agentguard doctor                # hook/service path still valid (stable /opt/homebrew/bin path); daemon on new version after first hook call or restart
```

## 6. Documentation checks

```bash
go run ./tools/gendocs docs/cli && git diff --exit-code -- docs/cli   # CLI reference current
make docs-check                                                          # lychee + markdownlint + placeholder grep
go test ./test/e2e -run TestDocsSmoke -count=1                            # getting-started commands run
shellcheck -s sh install.sh; pwsh -c 'Invoke-ScriptAnalyzer install.ps1 -Severity Warning'
```
Expected: all clean; `README.md` has no `TODO(` markers.

## 7. Newcomer walkthrough (SC-006, manual)

Give `README.md` + `docs/getting-started.md` to someone new on a fresh machine; time from first command to first blocked decision must be < 10 min with only documented commands; record in `docs/validation-<date>.md`.
