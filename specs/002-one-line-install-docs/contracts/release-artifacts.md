# Contract: Release Artifacts, Publishing & Upgrade Coherence

## Tag → release

- Trigger: push of tag matching `v[0-9]+.[0-9]+.[0-9]+` (optionally `-rc.N`, `-beta.N`).
- Workflow `.github/workflows/release.yml` — four jobs, so a release can only become "latest" after its exact artifacts have been installed by the real scripts on all three OSes:
  1. `build` (ubuntu): checkout (full history) → setup Go 1.22 → `make tidy-check lint-scripts docs-check test` → `goreleaser release --clean --skip=publish` → upload `dist/**` as a workflow artifact.
  2. `verify-installers` (matrix ubuntu/macos/windows, needs `build`): download `dist/`, start the local static release server (`go run ./tools/releaseserve dist/ --tag <tag>` — serves the archives + `checksums.txt` and answers `/releases/latest` with a 302 to `/releases/tag/<tag>`), run `install.sh` / `install.ps1` with `AGENTGUARD_LATEST_URL`/`AGENTGUARD_DOWNLOAD_BASE` pointed at it, assert `agentguard version` = tag in a new shell, upgrade from a previous fake version, `setup claude --dry-run` with a shim, uninstall; time the install (< 60 s); `winget validate` on the generated manifest (windows leg, `continue-on-error: true` until the manifest is upstream-accepted).
  3. `publish` (ubuntu, needs `verify-installers`): `goreleaser release --clean` (builds are reproducible with the same commit/ldflags; publishes archives, `checksums.txt`, tap formula, winget manifest/zip); pre-release tags (`-rc`, `-beta`) are published as GitHub pre-releases and never become "latest".
  4. `post-verify` (needs `publish`): invoke `install-test.yml` remote mode pinned to the tag (the **documented one-liners** against the public URLs). On failure: `gh release edit <tag> --prerelease` (demotes it so "latest" points back to the previous stable release) and fail the workflow; the release notes get a "verification failed" line via `gh release edit --notes-file`.
- GoReleaser settings (delta vs. current `.goreleaser.yaml`): `release.draft: false`, `release.prerelease: auto`, `release.footer` linking `CHANGELOG.md#<version>`; keep archives/checksums/ldflags; add `brews:` (tap `agentguard/homebrew-tap`, formula `agentguard`, `install: bin.install "agentguard"`, `test: system "#{bin}/agentguard", "version"`) and `winget:` (publisher `AgentGuard`, package `AgentGuard.AgentGuard`, `skip_upload: auto` when no token; manifests also archived to `dist/` and uploaded as `winget-manifest.zip` release extra).
- Secrets: `GITHUB_TOKEN` (release), `HOMEBREW_TAP_GITHUB_TOKEN`, optional `WINGET_GITHUB_TOKEN`.

## Assets (per release)

```
agentguard_<ver>_darwin_arm64.tar.gz
agentguard_<ver>_darwin_amd64.tar.gz
agentguard_<ver>_linux_arm64.tar.gz
agentguard_<ver>_linux_amd64.tar.gz
agentguard_<ver>_windows_arm64.zip
agentguard_<ver>_windows_amd64.zip
checksums.txt            # "<sha256>  <asset>" per line
winget-manifest.zip      # only when winget auto-submit is not configured
```

Archive contents: `agentguard` (or `agentguard.exe`), `README.md`, `LICENSE`. Binary reports `agentguard version` = `<ver>` (ldflags).

## "Latest" contract

`https://github.com/<repo>/releases/latest` redirects (HTTP 302) to `…/releases/tag/v<ver>` of the newest non-pre-release; installers rely on this and MUST NOT call `api.github.com`. Download URLs: `https://github.com/<repo>/releases/download/v<ver>/<asset>`.

## Upgrade coherence contract (binary side)

- `platform.SelfExecutablePath()` returns the **stable** path: the first PATH entry `p` such that `os.SameFile(p, resolvedExe)`, else `os.Executable()` unresolved if it is a symlink whose target lives under a `Cellar/`, `versions/`, or WinGet `Packages/` directory, else the resolved path. Hooks/service definitions embed that value (`agentguard setup claude` and `doctor` use it).
- IPC envelope gains optional `client_version` (protocol stays v1; additive). Daemon behavior: if `client_version` is a newer semver than its own **and** the daemon's own executable has been replaced on disk since it started, it serves the request normally, logs `newer client detected; restarting`, and exits with code `75` after in-flight requests. Service definitions: launchd `KeepAlive: true` (already), systemd `Restart=always` + `RestartSec=1`, Windows: hook lazy start (already) — all bring the new binary up.
  - **The executable-changed condition is required, and was added during implementation.** Restarting on the version comparison alone means a newer binary installed *elsewhere* on `PATH` — a Homebrew install alongside a `curl | sh` one — makes every request restart the daemon into the same old code. The gate would then spend its life starting up, which is a worse outcome than the staleness the mechanism exists to fix. When the executable is unchanged the daemon logs the mismatch once and keeps serving; `doctor` reports it with `agentguard daemon restart`.
  - A version this build cannot parse is never "newer": acting on a misread version is worse than not acting.
- `agentguard doctor` reports `daemon version ≠ CLI version` with the fix `agentguard daemon restart`, and `hook/service path ≠ current stable path` with the fix `agentguard setup claude`.

## Homebrew formula (generated) — user-visible contract

`brew install agentguard/tap/agentguard` installs `agentguard` on macOS and Linux; `brew upgrade agentguard` upgrades; the daemon picks up the new binary via the coherence contract; docs tell users to run `agentguard daemon restart` if `doctor` says so.

## winget manifest (generated) — user-visible contract

Package `AgentGuard.AgentGuard`, installer type `portable`, command alias `agentguard`; `winget install AgentGuard.AgentGuard` once merged upstream. Until then the manifest zip on the release page can be installed with `winget install --manifest <dir>` (documented as advanced).
