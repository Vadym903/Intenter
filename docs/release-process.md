# Release process

A release is what the one-line installers fetch. It is published only after the
exact artifacts about to be published have been installed by the real scripts on
macOS, Linux and Windows.

## Before the first release

The repository is `github.com/Vadym903/Intenter` and the Homebrew tap is
`Vadym903/homebrew-tap`. Both strings are deliberately identical everywhere they
appear (`install.sh`, `install.ps1`, `.goreleaser.yaml`, `.github/workflows/`,
`README.md`, these docs), so a future move is one search and replace — but the
one-liners are copied by users and cannot be quietly changed after a release:

```sh
grep -rIl "Vadym903/Intenter" . --exclude-dir=.git
grep -rIl "Vadym903/homebrew-tap" . --exclude-dir=.git
```

Required before the first tag:

- The repository is **public**. The installers resolve "latest" by following a
  redirect on the releases page; a private repository has no public redirect.
- The secrets below exist.
- A `## [X.Y.Z]` section exists in `CHANGELOG.md`. The release workflow fails
  without one, because a release whose notes nobody wrote is a release nobody
  can read.

## Versioning

Semantic versioning, tags prefixed with `v`.

| Tag | Effect |
|---|---|
| `v0.2.0` | A normal release. Becomes "latest"; the one-liners install it. |
| `v0.2.0-rc.1` | Published as a pre-release. Never becomes "latest"; installable only with an explicit `--version`. |

Until 1.0.0 the CLI surface, the approvals schema and the IPC protocol may change
between minor versions. Say so in the changelog when they do.

## Cutting a release

```sh
# 1. Everything green locally.
make tidy-check lint lint-scripts docs-check test e2e install-test

# 2. Move the Unreleased entries into a dated section.
$EDITOR CHANGELOG.md
scripts/changelog-section.sh v0.2.0    # prints it, or fails if it is missing

# 3. Regenerate the CLI reference if any command changed.
make docs && git diff --stat -- docs/cli

# 4. Commit, tag, push.
git commit -am "release: v0.2.0"
git tag v0.2.0
git push origin main --tags
```

Pushing the tag is the only trigger. Everything after it is automatic.

## Cutting the first release

The first release goes out in two steps so that nothing becomes "latest" before
a person has seen it work once.

**The candidate** (`v0.1.0-rc.1`) is cut as soon as CI is green on the public
repository and the real signing key is in place:

```sh
scripts/changelog-section.sh v0.1.0-rc.1   # the section exists (date it in the heading)
git commit -am "release: v0.1.0-rc.1"
git tag v0.1.0-rc.1
git push origin main --tags
```

The pipeline publishes it as a pre-release; it installs only with
`--version 0.1.0-rc.1` / `-Version 0.1.0-rc.1` and the README says so.

**The final release** (`v0.1.0`) is the maintainer's step, after the hands-on
walkthrough. The checklist, in order:

1. Run the walkthrough from [validation-template.md](validation-template.md)
   against `v0.1.0-rc.1` (every Definition-of-Done step and the §4a hook
   checks) and record it in the pre-created
   [validation-2026-08-19.md](validation-2026-08-19.md). Every ✗ is fixed or
   documented first; a fix means a new `rc.N` and a repeat.
2. `CHANGELOG.md`: date the `## [0.1.0]` heading (`## [0.1.0] - YYYY-MM-DD`).
3. `README.md`: delete the "Release candidate" block in *Install* (the
   `<!-- example -->` block marked "release-candidate note") and rewrite the
   "Release status" paragraph in *Status & roadmap* from the record (which
   platforms were validated by hand); `docs/install.md`: change the "Pin a
   version" example back to a generic version and drop the candidate sentence.
4. `README.md`: remove the `<!-- after-first-release -->` guard lines so the
   release and download badges render.
5. `scripts/check-readme.sh && scripts/check-rename.sh && scripts/check-badges.sh`
   (and `make docs` if any command changed).
6. `git commit -am "release: v0.1.0" && git tag v0.1.0 && git push origin main --tags`.

After step 6 the pipeline publishes `v0.1.0` as "latest", the README one-liners
work with no version flag, an installed candidate updates to it with the
signature verified, and the Homebrew formula lands in the tap automatically.

## What the release workflow does

`.github/workflows/release.yml`, four jobs in sequence:

**1. `build`** — runs `make tidy-check lint-scripts docs-check test`, checks the
changelog section exists, then `goreleaser release --clean --skip=publish`. The
artifacts go to a workflow artifact, not to GitHub Releases.

**2. `verify-installers`** — on ubuntu, macOS and Windows in parallel. Serves the
freshly built `dist/` with `tools/releaseserve`, which answers exactly like
GitHub Releases including the `/releases/latest` redirect, then runs the real
`install.sh` and `install.ps1` against it: install, assert the version in a new
shell, upgrade from an older build, uninstall, all inside 60 seconds. The
Windows leg also validates the generated winget manifest.

This is the job that makes the release trustworthy. It installs the bytes that
are about to be published, with the scripts users will run.

**3. `publish`** — `goreleaser release --clean`. Uploads the six archives and
`checksums.txt`, opens the Homebrew tap pull request, and generates the winget
manifest.

**4. `post-verify`** — runs `install-test.yml` in remote mode against the
published release, using the one-liners **extracted from `README.md`**, so the
documented commands and the tested commands cannot drift.

If post-verify fails, the release is automatically marked a pre-release. That
rolls "latest" back to the previous stable release, so no one-liner picks up a
release that cannot be installed, and a "verification failed — do not use" line
is appended to the notes.

To re-promote after fixing whatever it was:

```sh
gh release edit v0.2.0 --prerelease=false --latest
```

## Required secrets

| Secret | Used for | Needed |
|---|---|---|
| `GITHUB_TOKEN` | Publishing the release | Automatic |
| `COSIGN_PRIVATE_KEY` | Signing `checksums.txt` (the encrypted private key, PEM text) | **Always** — the build job refuses to run without a real key |
| `COSIGN_PASSWORD` | The password of that key | **Always** |
| `HOMEBREW_TAP_GITHUB_TOKEN` | Committing the formula to the tap repository | For the brew channel |
| `WINGET_GITHUB_TOKEN` | Opening a pull request against `microsoft/winget-pkgs` | For the winget channel |

Without the optional two, GoReleaser skips those publishers and the release still
completes; the winget manifest is attached to the release as a zip instead.

## Signing

Every release ships `checksums.txt.sig`: a cosign signature (keyed, ECDSA P-256
over SHA-256) of `checksums.txt`. The public half is `cosign.pub` at the
repository root, and the identical bytes are embedded in the binary
(`internal/updater/cosign.pub`) and in both installers. `intenter update`
verifies the signature before it trusts the checksums, always; `install.sh` and
`install.ps1` verify it with `cosign` or `openssl`/.NET when one is available
and say so plainly when they could only check the checksum
([install.md](install.md#verifying-a-download-by-hand)).

**Before the first release the maintainer creates the key pair once, offline:**

```sh
cosign generate-key-pair              # asks for a password; writes cosign.key and cosign.pub
gh secret set COSIGN_PRIVATE_KEY --repo Vadym903/Intenter < cosign.key
gh secret set COSIGN_PASSWORD  --repo Vadym903/Intenter    # paste the password
cp cosign.pub internal/updater/cosign.pub                   # keep the two copies identical
git add cosign.pub internal/updater/cosign.pub && git commit -m "release: signing key"
shred -u cosign.key 2>/dev/null || rm -P cosign.key         # the private key lives only in the secret
```

Until then `cosign.pub` holds a **verification-only placeholder** (its private
half was destroyed on creation, so nothing can ever verify against it), and the
release workflow's first step refuses to build a release while that placeholder
is in place. The updater's tests assert that the embedded copy equals the
repository-root file.

**Rotation:** publish the new public key in a release still signed by the old
key (updaters accept it because they verify with the key they already carry),
then sign later releases with the new key; keep the old public key in
`docs/install.md` for a release cycle so people can verify older downloads.

## The Homebrew tap

A separate repository, `<owner>/homebrew-tap`, containing a `Formula/`
directory. GoReleaser commits the formula there on each release, so nothing is
maintained by hand.

Bootstrapping it once:

1. Create the repository with a README explaining it is generated.
2. Create an empty `Formula/` directory.
3. Create a token with `contents: write` on it and add it as
   `HOMEBREW_TAP_GITHUB_TOKEN` to the main repository's secrets.
4. Check the generated formula before the first real release:

   ```sh
   goreleaser release --snapshot --skip=publish --clean
   cat dist/homebrew/Formula/intenter.rb
   ```

Homebrew installs the binary as a symlink into its versioned Cellar. Intenter
records the stable symlink path rather than the versioned one, so `brew upgrade`
does not break Claude's hooks — see
[how upgrades stay coherent](#how-upgrades-stay-coherent).

## The winget manifest

Generated per release and submitted as a pull request to
`microsoft/winget-pkgs`, which is reviewed by Microsoft and takes days. Until
`Intenter.Intenter` is accepted, the release page carries a
`winget-manifest.zip` that can be installed with `winget install --manifest`.

## How upgrades stay coherent

Two mechanisms, both worth knowing when a release changes either:

- **The recorded path is the stable one.** Claude's hooks and the service
  definition embed the path from `platform.SelfExecutablePath()`, which prefers
  the `PATH` entry over a version-pinned target. Without this, a package-manager
  upgrade leaves Claude calling a deleted file.
- **The daemon steps aside.** Requests carry the client's version; when it is
  newer *and* the daemon's own binary has been replaced on disk, the daemon
  finishes the request and exits 75, and the service manager starts the new one.

`intenter doctor` reports both if anything is left inconsistent.

## Badges

The README's badges read from live sources — the CI workflow's status, the
newest release tag, the download total, the `go` directive in `go.mod`. **None
of them is ever edited by hand**, and a release that required a README change to
show the right number would be a badge worth deleting instead.

Four of them cannot work before there is something to report, and a shields
badge with nothing to report is not blank — it renders an error image saying
"repo not found" or "no releases", which looks worse than no badge at all. Those
four are commented out in `README.md` behind two markers, each naming the event
that enables it:

| Marker | Badges | Enable when |
|---|---|---|
| `<!-- after-repository-is-public -->` | CI status, Go version | the repository is public and the workflow has run once |
| `<!-- after-first-release -->` | latest release, downloads | the first tag is published |

Uncomment each group once, at its moment, and never touch them again:

```sh
grep -n "after-repository-is-public\|after-first-release" README.md
scripts/check-badges.sh
```

`check-badges.sh` reports how many badges it skipped for being commented out, so
a group that was never enabled cannot pass unnoticed.

`check-badges.sh` fetches every shields.io URL in the README and reads the
response body, because shields answers `200` with an image that says "repo not
found" — an HTTP check alone would call a broken badge healthy. It runs in the
`docs` CI job on every push, so a badge that starts failing is noticed without
anyone looking at the page.

## After a release

- Watch the `post-verify` run.
- Check `brew install <owner>/tap/intenter` on a machine that does not have it.
- Move the changelog's `## [Unreleased]` heading back to the top.
- **After the first release only:** enable the release and download badges as
  described above, and confirm the latest-release badge shows the tag you just
  published.

## Yanking

There is no unpublish. To stop a release being installed:

```sh
gh release edit v0.2.0 --prerelease          # "latest" falls back to the previous release
gh release edit v0.2.0 --notes-file notes.md # say why, at the top
```

Deleting a release breaks anyone who pinned it. Demote rather than delete.

---

[Contributing](../CONTRIBUTING.md) · [Documentation](README.md)
