# Security model

What Intenter protects, what it does not, and the assumptions underneath.
Read the second section first — a security tool that oversells itself is worse
than none, because you stop watching the things it does not cover.

- [What it is for](#what-it-is-for)
- [Limitations](#limitations)
- [Threat model](#threat-model)
- [Fail-safe behavior](#fail-safe-behavior)
- [What stays on your machine](#what-stays-on-your-machine)
- [The installer](#the-installer)
- [Reporting a problem](#reporting-a-problem)

## What it is for

Intenter reduces the blast radius of an AI coding agent running shell commands
on your machine, in one specific way: **an approval is tied to what a command
does, not to how it is spelled.**

That closes a gap every string-matching permission system has. Approve
`npm run cleanup` once and the approval survives any later edit to that script.
Intenter's approval does not: it names the effects, records a hash of the
script it read, and stops applying when either changes.

On top of that it enforces a small set of rules no approval can override —
deleting your home directory, writing to system locations, changing credential
files, force-pushing to a protected branch, piping a download into a shell.
The rules judge the effects Intenter resolved, so they are as complete as the
resolver's view of the command: every recognised program and every option it
models. What the resolver cannot see it does not guess — an unmodelled option,
an unknown program or an unexamined tail makes the command unresolved, and
unresolved is asked (rule R13 forces the prompt when the line could not be
examined to the end), never allowed.

## Limitations

**It cannot see inside opaque command strings.** `bash -c '<script>'`,
`sh -c`, `eval`, a command assembled at runtime: the shell will run whatever is
in the string, and Intenter cannot resolve it statically. Such commands are
never approved by Intenter — outside bypass mode they go to Claude's own prompt.
In `bypassPermissions` mode there is no prompt to go to, so Claude runs them as
that mode intends; the hard rules hold in bypass mode for every command Intenter
*can* resolve, and only for those.

**It gates shell commands only.** Claude Code's `Write`, `Edit` and `Read` tools
touch files directly, without a shell, and Intenter sees none of it. An agent
can still rewrite your source files; that is the tool working as intended, and
if you need it gated, that is a different mechanism.

**It is not a sandbox.** Once a command is allowed, it runs with your full
privileges and can do anything you can. Intenter decides *whether* to run
something, not what it may touch while running. If you need containment, run the
agent in a container or a VM — and note that Intenter is useful there too, for
the same reason a seatbelt is useful in a car with airbags.

**A resolved command can still surprise you.** Resolution follows `npm run`,
Gradle and Maven, but a script that reads its work from a file at runtime, or
generates a command on the fly, cannot be resolved statically. Those cases end
up unresolved, which means asked — not silently allowed — but the prompt cannot
tell you what will happen either.

**Approvals are per project and per machine.** They do not sync, and a cloned
checkout at a different path is a different project.

**One agent.** Claude Code is the only integration, and only its `Bash` and
`PowerShell` tools. Other agents run ungated.

**Releases are signed, binaries are not code-signed.** Every release's
`checksums.txt` carries a signature made with the release key
(`cosign.pub` in the repository); the updater always verifies it, and the
installers verify it when `cosign`, `openssl` or PowerShell 7 is available and
say so when they could only check the checksum
([install.md](install.md#verifying-a-download-by-hand)). What is *not* done:
macOS notarization and Windows Authenticode signing of the executable itself,
so a manual download still meets Gatekeeper and SmartScreen warnings.

**The prototype's storage may change.** The approvals schema is versioned but
not yet frozen; a future release may require re-approving.

## Threat model

**Defends against** — the realistic accidents and the one realistic attack:

- An agent running a destructive command because it looked routine.
- A script whose meaning changed after you approved it, whether by your own
  edit, a dependency update, a merge, or a malicious commit in a repository you
  pulled.
- A permission rule that is far broader than the person who granted it realised.
- Commands that reach outside the project you are working in.
- Credential files being read or overwritten in passing.

**Does not defend against** — and no amount of care here would change it:

- A hostile agent that avoids the shell entirely.
- Anything running as another user, or as root.
- An attacker who can already write to Intenter's own database or binary. At
  that point they can also write to your shell profile, and the game is over.
- The contents of what an allowed command does. Approving `npm install` approves
  running whatever lifecycle scripts the dependency tree contains — which is why
  installs resolve as unresolved unless `--ignore-scripts` is passed.

**Assumes** — your user account is not already compromised, your operating
system's file permissions work, and the agent is the thing you are cautious
about rather than the machine it runs on.

## Fail-safe behavior

Every failure mode was chosen so that breaking makes Intenter *less* permissive
or *absent*, never more permissive:

| When | What happens |
|---|---|
| The daemon is not running or does not answer | The hook says nothing; Claude's own permission flow decides, exactly as if Intenter were not installed. It never says "allow". |
| A command cannot be parsed | Asked, not allowed. |
| A program is not modelled | Asked, not allowed. |
| A path cannot be pinned down | Asked, not allowed. |
| The database is locked or corrupt | Asked, with the reason. |
| A bug panics mid-decision | Caught; asked. |
| The audit row cannot be written | The decision is downgraded to ask, because a decision nobody can explain later is not one to act on. |
| The hook itself crashes | Exits 0 with no output; the session is unaffected. |

The one direction this does not go: Intenter cannot make a command run that
Claude would have refused. It can only refuse, or step aside.

## What stays on your machine

Everything.

- **No network in a decision.** Nothing on the path of a hook or a policy
  decision touches the network — not for telemetry, not for a model. The only
  outbound request the binary makes is the optional update check: an anonymous
  read of the public release page, never on a decision path, and switchable off
  ([updates](updates.md#turning-it-off)). Beyond that, only the installer and
  `intenter update` use the network, to download a release.
- **No accounts, no keys, no configuration to register.**
- **The database** holds approvals and the decision log: commands, resolved
  effects, paths, and timestamps. It lives under your user account with
  restrictive permissions.
- **Command output** is stored in the log only as a summary, capped at 1 KiB,
  and can be turned off entirely with `audit.store_response_summary = false`.
- **The daemon socket** is per-user: a Unix socket in a `0700` directory whose
  peer credentials are checked, or a Windows named pipe restricted to your SID.

`intenter history` shows you everything Intenter knows. There is nowhere
else for it to be.

## The installer

The one-liners run a script from the internet, which deserves scrutiny. What
they do and do not do:

- **HTTPS only**, and the download is pinned to it — a redirect cannot move it to
  plaintext.
- **Checksums are verified before anything is installed**, against the
  `checksums.txt` published with the release. A mismatch stops the installer with
  exit code 3 and leaves nothing behind. This is fail-closed: verification is
  never skipped.
- **The checksums file's signature is verified when it can be**: with `openssl`
  (macOS/Linux) or .NET (PowerShell 7) — the same check the updater makes — and
  otherwise with `cosign` if it is installed (its transparency-log lookup is
  skipped: the pinned key is the trust anchor, and a lookup would need the
  network). A bad signature stops the installer; a machine with no verifier gets exactly one
  line saying the download was checksum-verified only, and
  [install.md](install.md#verifying-a-download-by-hand) shows how to check by
  hand. `intenter update` always verifies the signature.
- **No `sudo`, ever.** Everything is written under your user account.
- **Bounded writes**: the install directory, your shell startup files (in a
  marked block the uninstaller removes exactly), and on Windows your user PATH.
  Nothing else.
- **No prompts**, because a script running under `curl | sh` has the script
  itself on stdin and could not read an answer honestly.
- **Nothing is executed from the archive** except the binary you installed, and
  only if you passed `--setup`.

Reviewed against this list, and re-reviewed whenever either script changes:

| Property | How |
|---|---|
| No remote code beyond the documented entry point | No `eval`; the only thing executed from the download is the binary, and only with `--setup` |
| Every variable quoted | ShellCheck in CI; the one deliberate exception carries a `disable` and a reason |
| Fails on error and on unset variables | `set -eu`; PowerShell `$ErrorActionPreference = 'Stop'` with `Set-StrictMode` |
| No world-writable temp | `mktemp -d`, which is `0700`; cleaned up by a trap on `EXIT INT TERM` |
| HTTPS only | `--proto =https --tlsv1.2`, relaxed only when the user has pointed the installer elsewhere themselves |
| Nothing logged that should not be | No credentials, no tokens; a configured proxy is named in errors because it is the usual cause |
| Every error says what to do next | Each failure names either the manual download, the docs, or the issue tracker |

All user-visible output is plain ASCII, so it stays readable in a terminal
without a Unicode font, in a CI log, and in an agent's own transcript.

If you would rather read it first: `curl -fsSL <url>/install.sh | less`, then run
it from disk. Or skip it entirely and
[install by hand](install.md#verifying-a-download-by-hand).

The release the installers fetch is itself verified before publication: the
release workflow installs the exact artifacts with these exact scripts on macOS,
Linux and Windows, and a release that fails is demoted so no one-liner picks it
up.

## Reporting a problem

Security issues: please report privately through GitHub's security advisory
form on the repository rather than in a public issue.

Useful in a report: what the command was, what `intenter history show <id>`
says about it, and what you expected instead. If the command is sensitive,
describe its shape rather than pasting it.

---

Next: [configuration](configuration.md) · [how it works](how-it-works.md) ·
[README](../README.md)
