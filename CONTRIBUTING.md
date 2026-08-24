# Contributing to Intenter

## What you need

- **Go 1.22 or newer.** Builds are `CGO_ENABLED=0`; there is no C toolchain
  requirement.
- **golangci-lint** for `make lint`.
- **ShellCheck** and **PowerShell 7** for `make lint-scripts`. Each only lints
  its own installer, so having one is enough to work on that one.
- **markdownlint-cli2** and **lychee** for `make docs-check`.
- Optional, only for the marketing assets: **vhs** for `make demo` and one of
  **rsvg-convert**, **Inkscape** or **ImageMagick** for `make social`. Both
  targets are run by hand when the assets change, never by CI.

CI runs all of them on Linux, macOS and Windows, so a missing local tool is
inconvenient rather than blocking.

## Getting a green build

```console
$ make build
$ make test
$ make lint
```

`make help` lists every target:

```console
$ make help
Intenter make targets:
  build: compile the binary into bin/
  clean: remove build output
  cross: build all six release targets into bin/
  demo: re-record the README demo GIF from assets/demo/intenter.tape
  docs-check: verify the docs are regenerated, linted, linked and placeholder-free
  docs: regenerate the CLI reference under docs/cli/
  fmt: format the Go sources
  help: list the available targets
  install-test: run the hermetic installer tests against a fake release server
  lint-scripts: check the installer scripts with ShellCheck and PSScriptAnalyzer
  lint: vet and golangci-lint the Go sources
  snapshot: build a local GoReleaser snapshot without publishing
  social: render assets/social/preview.png from preview.svg
  test-race: run the tests under the race detector
  test: run the unit and integration tests
  tidy-check: fail if go.mod or go.sum is not tidy
  tidy: update go.mod and go.sum
```

## The test suites

| Command | What it covers |
|---|---|
| `make test` | Everything below, plus the unit tests |
| `make test-race` | The same under the race detector |
| `make e2e` | Scenarios S1–S13 against a real binary, real daemon and real hook JSON |
| `make install-test` | `install.sh` and `install.ps1` against a fake release served locally |
| `go test ./... -run TestInvariant_` | The safety contract: invariants I-1…I-17 |

Two of those are worth knowing about specifically.

**The invariant suite** is a runnable reading of Appendix A of the
specification. Every invariant has a test named after it, and a meta-test fails
if the specification declares one that has no test. If you add a rule that
matters, add the invariant and its test together.

**The installer tests** package the real binary as a release, serve it the way
GitHub Releases does, and run the actual installer scripts against it. They exist
because installers run once, on someone else's machine, where a mistake shows up
as a stranger's failed install.

## Where things are

```text
cmd/intenter/         the binary: flags, wiring, nothing else
internal/
  action/               the domain model — what a command was understood to be
  parser/               POSIX, cmd.exe and PowerShell parsers
  resolver/             what a command actually does, incl. npm/Gradle/Maven
  scope/                where a path really is, after symlinks
  policy/               the hard rules, the baseline, the decision order
  approval/             matching, creation, invalidation, consent import
  audit/                the decision log
  storage/              SQLite schema and repositories
  daemon/               the per-user service and its IPC handlers
  ipc/                  protocol, framing, transports
  adapter/claude/       everything that knows Claude Code exists
  cli/                  commands and output
  platform/             per-OS paths, services, path rules
tools/                  build-time helpers (docs generator, release server)
test/e2e/               scenarios against the real binary
test/install/           installer tests against a fake release
specs/                  the specification and task plans
docs/                   user documentation; docs/cli is generated
```

**The dependency direction is enforced.** Nothing under `internal/` outside
`adapter/` and `cli/` may import them — that is invariant I-7, and it is what
makes a second agent an additive adapter rather than a rewrite. Both `depguard`
and a test enforce it.

## Where behavior is decided

`specs/001-agentguard-prototype/PROTOTYPE_SPEC.md` is the source of truth for
every rule, decision class and invariant. If the code and the specification
disagree, that is a bug in one of them — and which one is a decision worth
making explicitly, not working around.

When implementation reveals a genuine contradiction in the specification, record
it in Appendix C with the resolution *before* coding around it. There are
several entries there already; each one is a case where the specification could
not have been implemented as written.

## Sending a change

1. **Tests.** Behavior changes come with tests. Follow the style of the tests
   next to the code you are changing.
2. **Regenerate the docs** if you touched a command, a flag or a help string:
   `make docs`, and commit `docs/cli/`.
3. **Add a changelog entry** under `## [Unreleased]` in `CHANGELOG.md`.
4. **Run the gates** you can: `make test lint docs-check`.
5. Explain *why* in the description. The what is in the diff.

### Comments

Comments explain why something is necessary, not what the code says. There are
no decorative separators. When a piece of code exists because of a specific
failure, the comment names that failure — several would otherwise look like
overcomplication and be simplified away by the next reader.

## Contribution terms

Intenter is source-available under the [PolyForm Noncommercial License
1.0.0](LICENSE), and commercial licenses are sold separately. That combination
only works if the project can license the whole codebase, contributions
included, so sending a change means agreeing to the following.

1. **Your contribution is licensed under the project license.** Everything you
   submit — code, documentation, assets — is contributed under the PolyForm
   Noncommercial License 1.0.0 and released under it alongside the rest of the
   project.
2. **You certify the [Developer Certificate of Origin](https://developercertificate.org/)**
   for each commit: you wrote it, or you have the right to submit it under the
   project license. Certify it by signing off:

   ```console
   $ git commit -s -m "resolver: handle pnpm workspace scripts"
   ```

   which appends a `Signed-off-by: Your Name <you@example.com>` trailer. Amend
   an unsigned commit with `git commit -s --amend`, or a whole branch with
   `git rebase --signoff main`. Pull requests without a sign-off on every commit
   are asked for one before review.
3. **You grant the maintainers a relicensing right.** You give a perpetual,
   worldwide, irrevocable, royalty-free right to use, reproduce, modify,
   sublicense and distribute your contribution under other terms as well,
   including commercial licenses of Intenter. You keep the copyright in what
   you wrote; this is a licence grant, not an assignment.
4. **Ask before you build something large.** Open a
   [Discussion](https://github.com/Vadym903/Intenter/discussions) or an
   issue first for new behavior, a new adapter, a dependency, or anything that
   changes the specification — the decision is usually about the rules in
   `specs/001-agentguard-prototype/PROTOTYPE_SPEC.md`, and it is cheaper to have
   that conversation before the code exists. Bug fixes and documentation need no
   preamble.

If you cannot agree to these terms, please open an issue describing the change
instead of a pull request; it can still be implemented, just not from your
patch.

## Releasing

See [docs/release-process.md](docs/release-process.md).
