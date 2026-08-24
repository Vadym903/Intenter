# Kit dry run

[SC-008](../../specs/004-github-marketing-page/spec.md) asks that a repeat
marketing action needs no new copywriting: every word comes from
[`pitch.md`](pitch.md) and [`describe.md`](describe.md). The only way to know
whether that is true is to try to do one, so this is the attempt — a social post
and a directory submission, assembled from the kit with nothing written on the
spot, and a record of what was missing.

Re-run it whenever the kit changes shape. It takes ten minutes and it is the
difference between a kit and a folder of documents.

## Attempt 1 — an X post

Assembled from: the [one-line pitch](pitch.md#one-line-130-characters), the
repository link from [`describe.md`](describe.md#links), and
`assets/social/preview.png` as the card.

```text
A local permission layer for AI coding agents: it approves what a command does,
not what it is called. Free for noncommercial use.

github.com/Vadym903/Intenter
```

**Result: complete, no new copy needed.** The card carries the tagline and the
install command, so the post text does not repeat them.

## Attempt 2 — an "awesome-claude-code" list entry

Assembled from: `describe.md` for the link, `pitch.md` for the description.

```markdown
- [Intenter](https://github.com/Vadym903/Intenter) — A permission layer for AI coding agents that approves what a command does, not its name.
```

**Result: one gap.** List entries are a link plus a clause, usually under about
100 characters. The shortest copy in the kit was the 130-character one-liner,
which is too long for the shape and ends in a full stop that reads oddly
mid-row. Writing a shorter one on the spot is exactly what SC-008 forbids.

## Attempt 3 — the reply that always follows

Every post produces "so it is not open source?" within minutes, usually before
anything else. The kit said what *not* to write ("never describe it as open
source") but had no approved sentence to send.

**Result: one gap.** A rule about wording is not copy. Answering from memory is
where the license description drifts, and a drifted answer in a public thread is
quoted back for years.

## Gaps found, and the fixes

| # | Gap | Fix |
|---|---|---|
| 1 | No copy short enough for a directory row | Added [List entry (88 characters)](pitch.md#list-entry-88-characters) to `pitch.md` |
| 2 | No approved answer to "so it is not open source?" | Added the reply to [the licensing section](pitch.md#licensing-sentence) of `pitch.md` |

Both were used to re-assemble attempts 2 and 3 above; nothing else in either
attempt required a word that was not already approved.

## Not gaps

- **The Show HN title.** It is in
  [`launch-checklist.md`](launch-checklist.md#channels), which is part of the
  kit. Copy does not have to live in `pitch.md` to count.
- **Answers to "is it a sandbox", "does it use an LLM", "why not `npm run *`".**
  They are the README FAQ, quoted verbatim; the launch checklist points at each
  one. Duplicating them into the kit would create two versions to keep in step.
- **Image alt text.** Already in [`describe.md`](describe.md#screenshot-and-preview-image).
