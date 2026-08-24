# Contract: Documentation Set & Automated Checks

## Files (all Markdown, rendered on GitHub)

```
README.md                    # rewritten; zero "TODO(" markers
CONTRIBUTING.md
CHANGELOG.md                 # Keep a Changelog; one section per release
docs/
  install.md
  getting-started.md
  how-it-works.md
  security-model.md
  configuration.md
  troubleshooting.md
  faq.md
  release-process.md
  cli/                       # GENERATED — do not edit by hand
    agentguard.md
    agentguard_setup.md, agentguard_setup_claude.md, agentguard_uninstall.md, agentguard_uninstall_claude.md,
    agentguard_daemon.md (+ run/start/stop/restart/status/install/uninstall),
    agentguard_approvals.md, agentguard_approval.md (+ show/disable/enable/revoke), agentguard_approve.md,
    agentguard_history.md, agentguard_history_show.md, agentguard_status.md, agentguard_doctor.md,
    agentguard_version.md, agentguard_hook.md, agentguard_hook_claude.md
  validation-<date>.md       # manual per-OS validation records
tools/gendocs/main.go        # `go run ./tools/gendocs docs/cli` (cobra/doc, front matter disabled, stable timestamps off)
```

## Required README sections (in order)

1. One-paragraph what/why + the `npm run cleanup` example.
2. **Install** — the four one-liners (macOS/Linux install, Windows install) with pin/uninstall variants linked to `docs/install.md`; Homebrew and winget one-liners with availability note.
3. **Set up Claude Code** — `agentguard setup claude`, what it does, restart sessions note.
4. **Try it** — 5-step demo linking to `docs/getting-started.md`.
5. **CLI at a glance** — table linking to `docs/cli/`.
6. **How it works / Security model** — short summary + links.
7. **Uninstall** — one-liner + `--purge` note.
8. **Documentation** index, **Contributing**, **License**.

## Automated checks (CI job `docs`, runs on every push/PR)

| Check | Tool | Pass condition |
|---|---|---|
| CLI reference up to date | `go run ./tools/gendocs docs/cli && git diff --exit-code -- docs/cli` | no diff |
| Links | `lycheeverse/lychee-action` (`--offline` for local links; online with `--max-retries 3 --accept 200,429` for external) | zero broken |
| Placeholders | `! grep -RIn "TODO(" README.md docs` | none |
| Markdown style | `markdownlint-cli2` with relaxed config (`MD013` off, `MD033` allow HTML) | clean |
| Getting-started commands | `test/e2e/docs_smoke_test.go`: extracts fenced ```console blocks marked `<!-- smoke -->` from `docs/getting-started.md`, runs them against the built binary with a Claude shim and temp HOME; asserts exit codes/expected substrings | pass on 3 OS |
| Installer lint | `shellcheck -s sh install.sh`; `Invoke-ScriptAnalyzer -Path install.ps1 -Severity Warning` | clean |

## Install-test workflow (`.github/workflows/install-test.yml`)

Triggers: `pull_request`/`push` touching `install.sh`, `install.ps1`, the workflow; `release: types: [published]`; `schedule: "0 6 * * *"`; `workflow_dispatch` (inputs: `version`); `workflow_call` (inputs: `version`, used by `release.yml` post-verify).

Matrix: `ubuntu-latest`, `macos-latest`, `windows-latest`. Steps per OS:
1. Fresh-machine assertion: `agentguard` not on PATH.
2. Install via **local script** (PR mode) or **documented remote one-liner extracted verbatim from `README.md`** (release/nightly/`workflow_call` mode); pin `${{ inputs.version || 'latest' }}`. PR-mode guard: when no published non-pre-release exists and no `version` input is given, run the hermetic `make install-test` instead and report "neutral (no release yet)".
3. Timing: measure the install command (`date +%s` / `Measure-Command`); assert < 60 s (hard fail on PR/release/`workflow_call`, warning on nightly).
4. New-shell check: `bash -lc 'agentguard version'` / `zsh -lc` (macOS) / `pwsh -NoProfile -Command 'agentguard version'` after reloading user PATH → equals expected version.
5. Upgrade path: install a pinned older release first, then the target; assert version changed and daemon (started in unmanaged mode with a temp DataDir) restarted (`agentguard daemon status --json` version = new).
6. `agentguard setup claude --dry-run` with a `claude` shim on PATH → exit 0.
7. Uninstall via one-liner; assert binary gone, PATH block/entry gone, data dir kept; `--purge` variant removes data.
8. Upload logs on failure.

Relationship to `release.yml`: pre-publish verification (`verify-installers`, local artifacts via `tools/releaseserve`) gates publishing; this workflow's remote mode is the post-publish check whose failure demotes the release to pre-release (see `contracts/release-artifacts.md`).
