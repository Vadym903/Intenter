# Getting help

- **Questions and "how do I…"** → [GitHub Discussions](https://github.com/Vadym903/Intenter/discussions) (Q&A category).
- **Something is broken** → [open an issue](https://github.com/Vadym903/Intenter/issues/new/choose) using the bug template.
- **Security problems** → never a public issue; see [SECURITY.md](SECURITY.md).
- **Ideas** → Discussions (Ideas) or the feature-request template.

Before asking, the docs answer most things: [getting started](docs/getting-started.md),
[troubleshooting](docs/troubleshooting.md), [FAQ](docs/faq.md),
[CLI reference](docs/cli/README.md).

## What to include

```sh
intenter version
intenter doctor --json     # redact anything you consider private (paths, project names)
```

plus your OS/architecture, your Claude Code version (`claude --version`), what
you ran, what happened and what you expected. For a decision you disagree with,
`intenter history show <id>` explains exactly which rule or approval decided.

## Response expectations

This is a small project. Issues are triaged within a week; there is no
guaranteed turnaround. Commercial licensing questions go to
[Discussions](https://github.com/Vadym903/Intenter/discussions).
