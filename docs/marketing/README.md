# Marketing kit

Maintainer-facing, not user documentation. It exists so that the project
describes itself the same way in every place it is described — the repository
page, a directory listing, a post, an AI assistant's answer — and so that the
description stays true as the product changes.

The rule the whole kit rests on: **no new copy written at the moment of
posting.** Anything written under time pressure drifts, and a drifted sentence
about the license or about what the tool is not gets quoted back for years.

| Page | What it is for |
|---|---|
| [canonical.md](canonical.md) | The one-sentence definition. Line 1 is the sentence; it appears verbatim in the README, `llms.txt`, the repository description, the social preview and `pitch.md`. Change it here first. |
| [pitch.md](pitch.md) | Approved copy at every length — tagline, list entry, one line, short, long — plus the licensing sentences and the words never to use. |
| [describe.md](describe.md) | The whole submission: description, category, links, licensing, image alt text. What a directory form asks for. |
| [launch-checklist.md](launch-checklist.md) | Publication day: pre-flight, channels, the questions that always come up, post-launch. |
| [repo-settings.md](repo-settings.md) | Everything on the repository page that is not a file — description, topics, website, social preview, Discussions — and the owner inputs still to fill. |
| [target-queries.md](target-queries.md) | The questions people ask when they have the problem but not the name, with a baseline to measure against. |
| [first-screen-test.md](first-screen-test.md) | The ten-second test, its layout budget, and the results. |
| [claims-audit.md](claims-audit.md) | Every claim the README makes and the document that backs it. |
| [kit-dry-run.md](kit-dry-run.md) | Proof the kit is usable: a post and a listing assembled from it, and the gaps that found. |

Checks that keep it honest — `scripts/check-readme.sh` (placeholders, wording,
alt text, canonical consistency, version drift, asset sizes, licensing, section
order), `scripts/check-readme_test.sh` (that those rules still fire) and
`scripts/check-badges.sh` — run in `make docs-check` and the `docs` CI job.

Assets live outside this directory, next to the pipelines that regenerate them:
[`assets/demo/`](../../assets/demo/README.md) and
[`assets/social/`](../../assets/social/README.md).
