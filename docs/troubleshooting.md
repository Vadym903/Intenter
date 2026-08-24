# Troubleshooting

Start here:

```console
$ intenter doctor
```

It checks every part of the installation and prints a fix under each problem it
finds. Most of this page is that output, expanded.

- [`intenter`: command not found](#intenter-command-not-found)
- [Claude runs commands without asking](#claude-runs-commands-without-asking)
- [Nothing appears in history](#nothing-appears-in-history)
- [The daemon is not running](#the-daemon-is-not-running)
- [Daemon version differs from the CLI](#daemon-version-differs-from-the-cli)
- [Hooks point at the wrong binary after an upgrade](#hooks-point-at-the-wrong-binary-after-an-upgrade)
- [I am asked about something I approved](#i-am-asked-about-something-i-approved)
- [I am asked about harmless commands](#i-am-asked-about-harmless-commands)
- [Something was blocked and I disagree](#something-was-blocked-and-i-disagree)
- [Windows](#windows)
- [Corporate proxies and custom CAs](#corporate-proxies-and-custom-cas)
- [Starting over](#starting-over)
- [Reporting a bug](#reporting-a-bug)

## `intenter`: command not found

The installer added a directory to your `PATH`, and your current shell was
started before that happened. A running shell cannot be changed from outside.

Open a new terminal. If that does not fix it:

```sh
export PATH="$HOME/.local/bin:$PATH"     # macOS, Linux
```

```powershell
$env:Path = [Environment]::GetEnvironmentVariable('Path','User') + ';' + $env:Path
```

If the binary is not where you expect, the installer may have used `--prefix`,
or the `PATH` block may have gone into a startup file your shell does not read.
Check which files have it:

```sh
grep -l "intenter" ~/.zshrc ~/.bashrc ~/.profile ~/.zprofile 2>/dev/null
```

macOS zsh users: a login shell reads `~/.zprofile`, not `~/.zshrc`. The installer
writes both.

## Claude runs commands without asking

Three possibilities, in the order worth checking.

**The session predates the setup.** Claude reads its hook configuration once, at
session start. Restart Claude Code.

**The hooks are not installed.**

```console
$ intenter doctor
✗ Hooks    not installed in /Users/you/.claude/settings.json
    → run `intenter setup claude`
```

**The command is allowed, correctly.** Reads inside your project do not prompt.
Check what actually happened:

```console
$ intenter history --limit 5
```

If the decision was `ALLOW` with class `POLICY_READONLY_WORKSPACE`, that is the
baseline doing its job; turn it off in
[configuration](configuration.md#policy) if you want to be asked about
everything.

## Nothing appears in history

The hook is not reaching the daemon. Its log says why:

```sh
tail -50 "$(dirname "$(intenter doctor --json | grep -o '"[^"]*intenter.db"' | tr -d '"')")/logs/hook.log"
```

Common causes: the daemon is not running (below), or the hook entry in Claude's
settings names a binary that no longer exists (below).

## The daemon is not running

```console
$ intenter daemon start
$ intenter daemon status
```

If it will not stay up, run it in the foreground and watch:

```console
$ intenter daemon run
```

Common causes: another instance already holds the lock (`intenter daemon
status` will say), or the data directory is not writable.

Not having a service manager is fine, not a fault:

```text
✓ Service    unmanaged; the daemon starts on demand from the agent hook
```

That is the supported fallback — the first gated command starts the daemon.

## Daemon version differs from the CLI

```console
$ intenter doctor
✗ Daemon    running 0.1.0, this binary is 0.2.0
    → run `intenter daemon restart` to pick up the new binary
```

An upgrade replaced the binary while the old daemon was still running. It
normally notices and restarts itself; if it has not, restart it by hand. Nothing
is wrong with your approvals.

## Hooks point at the wrong binary after an upgrade

```console
$ intenter doctor
✗ Installed paths    the Claude hook runs /opt/homebrew/Cellar/intenter/0.1.0/bin/intenter,
                     but this binary is /opt/homebrew/bin/intenter
    → run `intenter setup claude` to point them at the current binary
```

This is the failure mode a package-manager upgrade used to cause: the hook was
recorded with a version-pinned path that no longer exists, so Claude reports a
hook error and carries on ungated. `intenter setup claude` rewrites the entry
with the stable path.

## No update prompt ever appears

Work down this list; `doctor` answers most of it:

```console
$ intenter doctor
✓ Updates          0.2.0 available (you have 0.1.0) — run `intenter update`
✓ Start-up check   not installed
    → run `intenter update startup enable` to be told about new releases
```

1. **Is the check installed?** `intenter update startup status` says which
   files have it, and `intenter update startup enable` adds it. It only takes
   effect in terminals opened afterwards.
2. **Is checking switched off?** `updates.check = false` in `config.toml`, or
   `INTENTER_NO_UPDATE_CHECK` set in your environment. Either one silences it
   completely — deliberately.
3. **Is there a terminal?** The prompt needs both stdin and stdout to be a
   terminal, and stays silent when `CI` is set. That is why it never appears in
   scripts, editors' task runners or `ssh host cmd`.
4. **Have you already answered?** "Not now" is quiet for 24 hours and "skip this
   version" forever for that version. `intenter update --check` shows
   `skipped` and `not before`; `intenter update --unskip` undoes a skip.
5. **Has a check succeeded?** Nothing is offered until one has. `intenter
   update --check` runs one immediately and prints the failure if there is one.
6. **Windows: is PowerShell allowed to run profiles?** Under a `Restricted` or
   `AllSigned` execution policy no profile script runs at all. `doctor` says so;
   the fix is `Set-ExecutionPolicy -Scope CurrentUser RemoteSigned`. `cmd.exe`
   has no start-up prompt at all.

## Every terminal prompts me

The prompt is meant to appear at most once per day across all terminals, which
needs a state file it can write:

```console
$ ls -l "$(intenter doctor --json | grep -o '"[^"]*/update/state.json"')"
```

If `<data>/update/` is not writable — a read-only home, a permissions mishap —
each terminal starts from nothing and prompts again. Fix the permissions, or
switch the prompt off with `intenter update startup disable`.

## An update failed part-way through

```console
$ intenter update
...
intenter: updater: Intenter 0.2.0 is installed, but the daemon did not restart
finish with: intenter daemon restart && intenter doctor
```

Exit code 6 means the new binary is already in place and only the follow-up
steps did not finish. Run the two commands it names. Your approvals and history
are untouched.

Other exit codes stop *before* anything is replaced:

| Code | Meaning |
|---|---|
| 2 | The check or the download failed |
| 3 | The download did not match the published checksum — nothing was installed |
| 4 | The install location cannot be written |
| 5 | The package manager's own upgrade command failed |
| 7 | Another terminal is already updating |
| 8 | The release signature (`checksums.txt.sig`) did not verify — nothing was installed |

## The daemon still reports the old version after an update

```console
$ intenter daemon restart
$ intenter doctor
```

The update restarts the daemon itself; if that failed you will have seen exit
code 6 above. A daemon started by something else — a login item pointing at an
old path — is reported by `doctor` under **Installed paths**.

## I am asked about something I approved

That is the feature, and the prompt says which approval stopped applying:

```text
Intenter: approval 1 no longer covers this action:
  npm-script:package.json#scripts.cleanup changed
```

Something the approval depended on is different — the script, the lockfile, the
build configuration. Look at what changed:

```console
$ intenter history show <event-id>
$ intenter approval show 1
```

The `valid while unchanged` list on the approval is what is being checked. If
the change was yours and is fine, approve the new behavior:

```console
$ intenter approve <event-id>
```

If it was not yours, you have just learned something worth knowing.

## I am asked about harmless commands

Look at what Intenter understood:

```console
$ intenter history show <event-id>
```

**`resolved: UNRESOLVED`** — it could not tell what the command does, so it asked
rather than guessing. Common with an unmodelled program, a command built from a
variable, or a script that generates its own commands. Approving it is not
possible by design; you can allow it in the moment through Claude's dialog.

**A wider scope than you expected** — a path resolving through a symlink out of
the project, for instance. The `targets:` section shows where it really goes.

**A build directory treated as source** — tell Intenter about it:

```toml
[scope]
generated_dirs_extra = ["artifacts"]
```

## Something was blocked and I disagree

Blocks come from the hard rules, and no approval overrides them — that is what
makes them worth having. The event says which rule:

```console
$ intenter history show <event-id>
  rule:      R2
  reason:    recursively deleting ~/Documents, which is in your home directory
```

If the command genuinely needs to do that, run it yourself outside the agent.
Intenter gates the agent, not you. If you think the rule is wrong for a case
it should not cover, that is worth an issue with the `history show` output.

## Windows

**PowerShell refuses to run the installer.** `irm | iex` runs text in memory and
is unaffected by the execution policy for files. If you downloaded it to disk:

```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1
```

**A connection error on an old Windows PowerShell.** TLS 1.0 is the default on
some 5.1 builds and GitHub refuses it. The installer raises this itself; if you
are scripting around it:

```powershell
[Net.ServicePointManager]::SecurityProtocol = 'Tls12'
```

**SmartScreen warns about the binary.** Releases are not code-signed yet. The
installers call `Unblock-File`, so the normal path does not hit this; a manual
download does. Verify the checksum, then:

```powershell
Unblock-File $env:LOCALAPPDATA\Intenter\bin\intenter.exe
```

**`intenter` not found in a new terminal.** Windows hands the updated
environment only to processes started after the change. Close and reopen the
terminal — including Windows Terminal tabs and VS Code's integrated terminal.

**Git Bash missing.** Claude Code's `Bash` tool uses it, and `intenter doctor`
checks for it. Install Git for Windows.

## Corporate proxies and custom CAs

The installers honour `HTTPS_PROXY`/`https_proxy`, and name the proxy when a
download fails through one:

```text
install.sh: download failed: https://github.com/… (via proxy http://proxy:3128)
```

If your proxy re-signs TLS, install its certificate authority in the system trust
store; `curl` and PowerShell both read it from there.

Nothing in the decision path makes a network request. The one request Intenter
makes on its own is the release check, which honors the same proxy environment
variables and can be switched off entirely — see [updates.md](updates.md).

## Starting over

Remove the integration but keep your approvals:

```sh
intenter uninstall claude
intenter setup claude
```

Remove everything including approvals and history:

```sh
intenter uninstall claude --purge
```

Or the whole installation, via the
[uninstall one-liner](install.md#upgrade-pin-uninstall).

## Reporting a bug

Useful in an issue:

```sh
intenter version
intenter doctor --json
intenter history show <event-id> --json
```

Plus what you expected instead. Turn on `log.level = "debug"`, reproduce, and
attach the relevant part of `daemon.log` if the problem is a decision you cannot
explain.

For anything security-relevant, use GitHub's private security advisory form
rather than a public issue.

---

Next: [FAQ](faq.md) · [configuration](configuration.md) · [README](../README.md)
