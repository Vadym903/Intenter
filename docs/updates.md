# Updating

Intenter tells you when a new release exists, asks whether you want it, and
installs it if you say yes. It never installs anything you did not ask for.

## The prompt

When a terminal starts and a newer release is known, you see this once:

```text
Intenter 0.2.0 is available (you have 0.1.0).
Release notes: https://github.com/Vadym903/Intenter/releases/tag/v0.2.0
Update now? [y]es / [N]ot now / [s]kip this version  (auto "not now" in 30s):
```

| Answer | What happens |
|---|---|
| `y` | The update runs there and then, and prints what it did |
| Enter, anything else, or no answer within 30 seconds | Nothing, and no prompt for the next 24 hours |
| `s` | That version is never offered again; the next release still is |

Opening ten terminals at once produces one prompt, not ten.

## On demand

```sh
intenter update --check          # check now and print the status
intenter update --check --json   # the same, machine-readable
intenter update --plan           # what an update would do; changes nothing
intenter update                  # update, after showing the plan and asking
intenter update --yes            # update without asking (scripts)
intenter update --skip 0.2.0     # never offer this version
intenter update --unskip         # undo that
```

## Turning it off

Any one of these is enough:

```sh
export INTENTER_NO_UPDATE_CHECK=1      # this shell, or your shell profile
intenter update startup disable        # remove the terminal start-up check
```

```toml
[updates]
check = false                            # config.toml: nothing is ever checked
```

With checking off, no network request is made by the update feature at all —
`intenter update` still works when you run it yourself.

## What it checks, and what it sends

The check asks the public release page which release is newest. It sends no
identifiers, no configuration and no telemetry — only a `User-Agent` naming the
tool and its version. There is no update server.

Checks run in the background, at most once a day, and never on the path of a
Claude Code hook or a policy decision. Nothing you do waits for the network.

## What an update does

1. Downloads the build for your OS and architecture over HTTPS.
2. Verifies the release's `checksums.txt` against its signature with the
   release key built into the binary (`cosign.pub` in the repository), then
   verifies the download against those SHA-256 checksums. A missing or bad
   signature, or a mismatch, stops everything and changes nothing.
3. Replaces the executable in place, atomically.
4. Restarts the background service and confirms it reports the new version.
5. Prints old → new and the release notes link.

Your approvals, history and configuration are untouched.

## Installed through a package manager

If Homebrew or winget installed Intenter, it will not overwrite their files.
Instead it runs the right upgrade command for you (with your consent), or prints
it:

```sh
brew upgrade Vadym903/tap/intenter
winget upgrade --id Intenter.Intenter --exact
```

## Pre-releases

Stable installations are never offered a pre-release. To follow them:

```toml
[updates]
channel = "prerelease"
```

## The terminal start-up check

`intenter setup claude` adds a small marked block to the start-up file of each
shell it finds — `~/.zshrc`, `~/.bashrc`, `~/.config/fish/conf.d/`, or your
PowerShell profile. It only runs `intenter update --startup`, and only in
interactive terminals.

```sh
intenter update startup status     # which files have it
intenter update startup enable     # add it
intenter update startup disable    # remove it
```

`intenter uninstall claude` removes it; the rest of each file is left exactly
as it was. Installing with `--no-modify-path` skips it.

On Windows, a `Restricted` or `AllSigned` execution policy stops PowerShell from
running profile scripts at all, so the prompt cannot appear. `intenter doctor`
says so, and the fix is:

```powershell
Set-ExecutionPolicy -Scope CurrentUser RemoteSigned
```

`cmd.exe` has no start-up prompt.

## When something goes wrong

`intenter update --check` prints the last check's outcome, including the
error. Repeated failures back off — an hour, then six, then a day — so a
machine that is offline or behind a captive portal does not retry constantly.

See [troubleshooting.md](troubleshooting.md) for the specific symptoms.
