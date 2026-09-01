# Intenter documentation

A permission layer for AI coding agents that remembers what a command *does*,
not what it is called. Start at the [project README](../README.md) if you have
not met it yet.

## Using it

| Page | What is in it |
|---|---|
| [Install](install.md) | The one-liners, pinning, upgrading, proxies, air-gapped machines, uninstalling |
| [Getting started](getting-started.md) | Ten minutes from install to a blocked command, with the reason it gives |
| [Updating](updates.md) | The terminal prompt, `intenter update`, channels, turning it off |
| [Configuration](configuration.md) | Every setting, what it does, and where everything lives |
| [Troubleshooting](troubleshooting.md) | Symptom, check, fix |
| [FAQ](faq.md) | Short answers |

## Understanding it

| Page | What is in it |
|---|---|
| [How it works](how-it-works.md) | Resolution, scopes, the decision order, fingerprints |
| [Security model](security-model.md) | What it protects, what it does not, and the fail-safe behavior |

## Reference

| Page | What is in it |
|---|---|
| [CLI reference](cli/README.md) | Every command and flag, generated from the binary |
| [Changelog](../CHANGELOG.md) | What changed in each release |

## Working on it

| Page | What is in it |
|---|---|
| [Contributing](../CONTRIBUTING.md) | Build, test, and the repository layout |
| [Release process](release-process.md) | Tagging, what the workflows do, required secrets |
| [Definition of done](definition-of-done.md) | Where the prototype stands against its own acceptance criteria |
| [Manual validation template](validation-template.md) | The maintainer's hands-on walkthrough of a release candidate before the final release |
| [Marketing kit](marketing/README.md) | The approved copy, the repository settings, and the checks that keep the README truthful |

The behavioral specification the implementation follows is
[`PROTOTYPE_SPEC.md`](../specs/001-agentguard-prototype/PROTOTYPE_SPEC.md) —
the source of truth for every rule, decision class and invariant named in these
pages.
