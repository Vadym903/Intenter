# Changelog

All notable changes to Intenter are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Until 1.0.0 the command-line surface, the approval database schema and the IPC
protocol may change between minor versions; each such change is listed here.

## [Unreleased]

Nothing yet.

## [0.1.0-rc.1]

Release candidate for the hands-on validation; the notes are under [0.1.0]
below. Installable with `--version 0.1.0-rc.1` (macOS/Linux) or
`-Version 0.1.0-rc.1` (Windows); `latest` resolves only to final releases.

## [0.1.0]

First published release. The prototype was developed under the name AgentGuard; the rename to Intenter is the first entry under *Changed*.
It is a semantic runtime permission layer for AI coding agents: it remembers
what a command *does*, not what it is called, so an approval stops applying the
moment the underlying behavior changes.

### Added

- **Semantic approvals.** Approving `npm run cleanup` records the resolved
  effects — `DELETE(recursive,force)` on `./dist` inside the workspace — together
  with a fingerprint of every mutable input the resolution depended on. A
  different command that resolves to the same behavior is allowed; the same
  command is not, once its script changes.
- **Command resolution.** POSIX, `cmd.exe` and PowerShell parsers; recognizers
  for filesystem utilities, `git`, `npm`/`pnpm`/`yarn`, Gradle, Maven, JS test
  runners and `curl`; `npm run` scripts, Gradle and Maven tasks are resolved to
  the work they actually perform. On Windows a package script is evaluated under
  both `cmd` and POSIX readings, and the effects are combined.
- **Path scope classification** on canonical, symlink-resolved paths: SYSTEM,
  WORKSPACE, WORKSPACE_GENERATED, HOME and OUTSIDE_WORKSPACE, with flags for
  credentials, traversal, symlink escapes and network paths.
- **Hard safety rules R1–R13** that no approval can override, covering system
  locations, home-directory deletes, credential files, workspace escapes, force
  pushes, history rewrites, elevated privileges, piping downloads into a shell,
  and command lines that could not be examined to the end.
- **Read-only baseline.** Reads confined to the project are allowed without a
  prompt, so `git status`, `grep -r`, `ls` and `cat README.md` never interrupt.
- **Claude Code integration** via PreToolUse, PermissionRequest, PostToolUse and
  ConfigChange hooks, including importing "Yes, and don't ask again" from
  Claude's own dialog as a validated Intenter approval.
- **Explainable decisions.** Every allow and block is answerable from the stored
  audit row alone: `intenter history show <id>` reports the resolution chain,
  targets and scopes, effects, fingerprints, the rule or approval that decided,
  and what the agent was told.
- **CLI**: `setup claude`, `uninstall claude`, `approvals`, `approval
  show|disable|enable|revoke`, `approve`, `history`, `history show`, `status`,
  `doctor`, `daemon`, `update`, `version`, all with `--json`; every command
  carries examples in `--help` and in the generated reference under `docs/cli/`.
- **Per-user daemon** over a Unix socket or Windows named pipe, registered with
  launchd, systemd (user) or the Windows Run key, with lazy start from the hook.
- **One-line installers** for macOS, Linux and Windows, with checksum and
  signature verification, in-place upgrade, version pinning and uninstall.
- **End-user documentation set** and a CLI reference generated from the binary.
- **Update checks and guided self-update.** Intenter notices new releases and
  asks once, when you open a terminal: update now, not now, or skip this
  version. "Not now" is quiet for a day, "skip" for that version forever, and an
  unanswered prompt counts as "not now" after 30 seconds. Opening ten terminals
  produces one prompt.
- `intenter update` — `--check` (with `--json`), `--plan`, `--yes`,
  `--version`, `--skip`/`--unskip`, `--channel`, `--allow-downgrade` — and
  `intenter update startup status|enable|disable` for the terminal check.
- Updates download over HTTPS, are verified against the signed, published
  checksums, and replace the executable atomically; a mismatch changes nothing.
  Homebrew and winget installations are handed to `brew upgrade` /
  `winget upgrade` rather than overwritten.
- Checks are anonymous — the public release page only, no identifiers — run in
  the background, never on the path of a Claude hook or a policy decision, and
  can be switched off entirely with `updates.check = false` or
  `INTENTER_NO_UPDATE_CHECK=1`.
- `[updates]` configuration section and `doctor` checks for whether an update is
  available, whether the terminal check can actually appear, and whether a
  pre-rename development install left anything behind.
- **A license.** Intenter is source-available under the PolyForm Noncommercial
  License 1.0.0: free for personal and other noncommercial use, while selling it
  or using it commercially requires a separate license. It is not open-source
  software in the OSI sense. `LICENSE` carries the full text, and the README,
  the FAQ and the license badge say the same thing in the same words.
- **Community files** — code of conduct (Contributor Covenant 2.1), security
  policy with private reporting, support page, issue forms for bugs, features
  and questions, and a pull-request template. `CONTRIBUTING.md` gains
  contribution terms: the project license, a Developer Certificate of Origin
  sign-off, and a relicensing grant that makes commercial licensing possible.
- **A README built as a landing page**: a first screen with the tagline, badges,
  demo and install one-liners, then a skimmable structure from why through how
  it works, what you get, an honest comparison with Claude's own allow rules,
  install, setup, the CLI, security and limitations, updating, an FAQ, and the
  roadmap with planned work marked as planned.
- **`llms.txt`** — a machine-readable summary for AI answer engines, carrying
  the canonical definition, the quotable facts and the documentation index.
- **Marketing collateral** under `docs/marketing/`: the canonical sentence,
  approved copy at four lengths, an approved description for directories, the
  repository-settings record, a launch checklist, a target-query list with a
  baseline procedure, the first-screen test protocol and a claim audit with a
  "true of the code" verdict per claim.
- **Demo and social assets** — `assets/demo/` (a scripted session driving the
  real binary, recorded with VHS through `make demo`) and `assets/social/` (the
  1280×640 preview card, rendered with `make social`).
- **Upkeep checks** — `scripts/check-readme.sh` (placeholders, wording, alt
  text, canonical-sentence consistency, version drift, asset sizes, licensing
  consistency, section order) with its own self-test, `scripts/check-badges.sh`
  (reads each badge's body because shields.io answers `200` for repositories
  that do not exist), and `scripts/check-rename.sh` (the old product name may
  survive only where the rename entry allows). All run in `make docs-check` and
  the `docs` CI job.
- `scripts/repo-metadata.sh` applies the repository description, website and
  topics, and prints the steps that only exist in the web UI.
- **`intenter summary`.** Counts what Intenter decided over a period, a project
  or a session: commands checked, how many ran without a prompt, how many of
  those a stored approval was responsible for, and how many were asked about or
  refused. `--since`, `--project`, `--session`, `--all` and `--json`. The counts
  come from the same audit rows `intenter history` lists, so the two cannot
  disagree.
- **A summary when a Claude session ends.** Intenter hooks `SessionEnd` and
  reports what it did across the session. Claude shows that message to the user
  and not to itself, so it costs the model no context; a session in which
  nothing was decided prints nothing.
- **How many prompts an approval answered for you.** Both views report the
  allows that a stored approval produced — each one a dialog that did not appear
  because the same question had already been answered. Reads let through by the
  read-only baseline are counted separately and excluded from that figure: they
  were never going to prompt. Nothing is extrapolated into minutes saved.
- **An allow an approval produced says so.** A command allowed by
  `APPROVAL_MATCH` or by an imported Claude rule prints one line naming the
  approval and how to inspect it, rather than passing in silence — silence is
  indistinguishable from Intenter not running. Allows from the read-only
  baseline stay quiet: they answer `git status`, `ls` and `cat` many times a
  session, and a line for each would bury everything else.
- **The deferral notice names the dialog answer it depends on.** Before Claude's
  own permission dialog appears, Intenter's summary says what "Yes, and don't
  ask again" will mean — an approval for what the command does, not for the text
  it was typed as. The dialog's own option labels are rendered by Claude from
  its permission suggestions and no hook output reaches them, so the line before
  it is the only place the two can be connected.
- **`intenter doctor` reports a required hook that is missing**, not only a
  settings file with no hooks at all. An installation that predates a new hook
  event keeps gating commands correctly, so nothing else would notice that
  whatever depends on that event never happens.

### Changed

- **Renamed to Intenter.** The prototype was developed under the name AgentGuard; this rename changes everything user-visible.
  The command is `intenter`, environment variables are `INTENTER_*`, the
  launchd/systemd/Run-key service, the data and configuration directories, the
  installer's PATH markers and the Claude Code hook entries carry the new name,
  and the repository is `github.com/Vadym903/Intenter`. A development install
  made under the old name is recognised by `intenter setup claude` — which
  replaces the stale hook entries, unregisters the old service and moves the
  data directory once, keeping approvals and history — and by `intenter doctor`,
  which lists any leftover with the command that removes it. No release was
  ever published under the old name, so no upgrade path is affected.

### Fixed

- `gofmt` violations across seventeen files that the repository's own linter
  configuration rejects.

### Security

- The gate fails safe: anything Intenter cannot parse, resolve or decide asks
  rather than allows, and no failure path — unreachable daemon, database error,
  panic — can produce an allow.
- Everything stays on the machine. No network calls, no telemetry, no accounts,
  and no language model in the decision path.
- Eleven findings from the 2026-08-17 security review, all fixed with
  regression tests: environment assignments are now an allowlist of
  behavior-neutral variables (anything else, such as `PAGER`, `EDITOR`,
  `BASH_ENV` or `_JAVA_OPTIONS`, makes the command unresolved and therefore
  asked); `git --paginate` no longer counts as a plain read; `git diff
  --no-index` models both operands as reads so a credential file is asked about;
  `curl` models `--cookie-jar`/`--cookie`/form/`-w` file I/O and refuses
  host-redirecting flags such as `--resolve`; every workspace-supplied file is
  opened non-blocking with a regular-file and size check, so a FIFO or a giant
  `package.json` cannot hang the daemon; the parsers examine every command past
  the 32-command cap and a new hard rule R13 forces a prompt whenever the whole
  line could not be examined; shell-init and OS-persistence files
  (`~/.zshenv`, LaunchAgents, autostart) are treated as sensitive; the updater
  honors URL overrides only in test mode and never downgrades to plain HTTP; the
  workspace `.claude/settings*.json` is protected against shell writes; loopback
  detection covers all of `127.0.0.0/8` and IPv6; and uninstall matches its own
  hook entries exactly.
- **Signed releases.** Every release's `checksums.txt` is signed with the
  release key (`cosign.pub`); `intenter update` verifies the signature before
  trusting the checksums and refuses with exit code 8 otherwise; both installers
  verify it when `cosign`, `openssl` or PowerShell 7 is available and print one
  line saying so when they could only check the checksum.
- **Second review pass** over the areas the first review had not covered, all
  fixed with regression tests. Windows command surface: `<`/`>` redirections
  are now recognised without surrounding whitespace in both the cmd.exe and
  PowerShell dialects (`echo hi>>…\authorized_keys` was invisible), `2>&1` no
  longer makes cmd.exe drop the command in front of it (`rd /s /q %USERPROFILE%
  2>&1` never reached the hard rules), a `^` inside a double-quoted cmd.exe
  string no longer hides a second command, `New-Item -ItemType
  SymbolicLink|Junction|HardLink` is unresolved rather than a plain create, and
  the PowerShell profile directories are sensitive. Package and build tools: a
  `.yarnrc.yml` `yarnPath` or a pnpm `.pnpmfile.cjs` (which runs regardless of
  `--ignore-scripts`) makes every `yarn`/`pnpm` command unresolved; `npm
  install` refuses the same workspace-, directory- and config-redirecting flags
  as `npm run`; `npx`/`dlx` no longer drop arguments; Gradle and Maven refuse
  the JVM proxy and TLS-trust properties (`-Dhttps.proxyHost`,
  `-Djavax.net.ssl.trustStore`, `-Dmaven.wagon.http.ssl.insecure`, …); Gradle
  composite builds (`includeBuild`) are unresolved; user-level Gradle/Maven
  configuration (`~/.gradle/init.d`, `~/.m2/settings.xml`, …) is part of the
  approval fingerprint. Approval matching: a semantic approval's envelope now
  carries each target's classification flags, so an approval for an ordinary
  home-directory write no longer covers a write into a tool cache
  (`~/.npm/_cacache`), and a disabled or revoked approval's mismatch is
  explained. A standing effect-superset corpus test guards the reviewed
  recognizers against regressions. Windows installer: downloads are pinned to
  HTTPS end to end (a redirect to plaintext is refused, as `install.sh` already
  did), the checksum lookup is anchored so a filename-suffix collision cannot
  match the wrong line, zip entries are checked for path traversal before
  extraction, and the no-verifier notice no longer fails in a console-less
  host.

[Unreleased]: https://github.com/Vadym903/Intenter/compare/v0.1.0-rc.1...HEAD
[0.1.0-rc.1]: https://github.com/Vadym903/Intenter/releases/tag/v0.1.0-rc.1
[0.1.0]: https://github.com/Vadym903/Intenter/releases/tag/v0.1.0
