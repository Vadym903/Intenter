# Bootstrapping the Homebrew tap

One-time setup, needed before the first release can publish a formula. Nothing
here is automated because it creates a second repository and a token, and both
are decisions rather than steps.

## 1. Create the tap repository

Homebrew requires the name to be `homebrew-<tap>`, and users then type
`<owner>/<tap>`:

| Repository | Users type |
|---|---|
| `Vadym903/homebrew-tap` | `brew install Vadym903/tap/intenter` |

```sh
gh repo create Vadym903/homebrew-tap --public \
  --description "Homebrew tap for Intenter"
```

It must be **public** — `brew install` from a private tap needs credentials.

## 2. Give it a README and an empty Formula directory

The formula is generated, so the README's job is to stop someone editing it by
hand:

```sh
git clone https://github.com/Vadym903/homebrew-tap
cd homebrew-tap
mkdir -p Formula
cat > README.md <<'EOF'
# Intenter Homebrew tap

    brew install Vadym903/tap/intenter

`Formula/intenter.rb` is **generated** by GoReleaser on every Intenter
release. Do not edit it by hand — the next release will overwrite it. Changes
belong in the `brews:` block of `.goreleaser.yaml` in the main repository.
EOF
git add README.md Formula
git commit -m "Bootstrap the tap"
git push
```

Leave branch protection off on `main`: the release bot commits directly.

## 3. Create the token

A fine-grained personal access token with **Contents: read and write** on
`Vadym903/homebrew-tap` only. Nothing else — this token is used by an
automated release and should be able to do exactly one thing.

Add it to the **main** repository as the secret `HOMEBREW_TAP_GITHUB_TOKEN`:

```sh
gh secret set HOMEBREW_TAP_GITHUB_TOKEN --repo Vadym903/Intenter
```

Without it, GoReleaser skips the formula (`skip_upload: auto`) and the release
still succeeds — the brew channel simply does not update.

## 4. Check the generated formula before relying on it

```sh
goreleaser release --snapshot --skip=publish --clean
cat dist/homebrew/Formula/intenter.rb
```

Compare it against `packaging/homebrew/intenter.rb.tmpl`, which documents the
expected shape. Look for: both architectures on both operating systems, the
right URLs, a `test do` block, and the `caveats` telling the user to run
`intenter setup claude`.

## 5. Verify on a machine that has never had it

```sh
brew install Vadym903/tap/intenter
intenter version
intenter setup claude
```

Then the upgrade path, which is the one worth being careful about:

```sh
brew upgrade intenter
intenter doctor
```

`doctor` should be clean, or should say only that the daemon needs restarting.
If it reports that the Claude hook points at a `Cellar/…/<old version>/…` path,
the stable-path logic has regressed — see
`TestStablePathAcrossTwoCellarVersions` in `internal/platform/self_test.go`.

## Notes

- The tap is not a submodule and is not vendored. It is a separate repository
  the release writes to.
- Removing the tap later: `brew untap Vadym903/tap`.
- The formula name (`intenter`) is what users type after the tap name, so
  changing it is a breaking change for anyone who installed via brew.
