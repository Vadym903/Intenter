# Security policy

Intenter is a permission layer for AI coding agents, so a bug in it can mean a
command runs that should not have. Reports are taken seriously and handled
privately.

## Reporting a vulnerability

Please **do not open a public issue** for a security problem.

- Preferred: use GitHub's private vulnerability reporting on this repository
  ("Security" tab → "Report a vulnerability").
- Direct link: <https://github.com/Vadym903/Intenter/security/advisories/new>.
  There is no e-mail channel; private reporting keeps the report between you
  and the maintainer until a fix is out.

Include what you can: the Intenter version (`intenter version`), operating
system, the command or hook payload involved, what Intenter decided and what
it should have decided, and — if it helps — the output of
`intenter history show <id>` for the decision (redact paths you consider
private).

You will get an acknowledgement within **3 business days**, an assessment within
**10 business days**, and a fix or a mitigation plan agreed with you before any
public disclosure. Credit is given in the release notes unless you prefer
otherwise.

## Supported versions

Only the **latest published release** receives security fixes. The one-line
installer and `intenter update` bring you to it; there are no long-term
support branches before 1.0.

## Scope

In scope: any way to make Intenter **allow** something its rules say it must
block or ask about, bypass a hard rule, reuse an approval after the resolved
behavior changed, tamper with the installer/update path (checksums, download
sources), or escalate through the daemon's IPC.

Out of scope by design (documented in [docs/security-model.md](docs/security-model.md)):
Intenter is not a sandbox — an allowed command runs with your privileges — and
it only gates Claude Code's shell tools, not its file-edit tools. Reports about
those limitations are welcome as feature discussions rather than
vulnerabilities.

## Coordinated disclosure

We follow a 90-day disclosure window from acknowledgement, shortened when a fix
ships earlier and extended only by mutual agreement.
