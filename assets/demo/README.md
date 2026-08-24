# Demo assets

`intenter.svg` is the hero visual embedded in the repository README: three
terminal panels telling the core story — approve once, run without a prompt in
the next session, get blocked with an explanation after `package.json` changes.
It is a hand-maintained, theme-safe SVG (its own dark background, monospace
text) so it renders identically on GitHub's light and dark themes and needs no
external player. Paths and names in it are fixtures (`~/proj`), never real
users.

An animated version can be produced with [VHS](https://github.com/charmbracelet/vhs):

```sh
make demo        # runs assets/demo/intenter.tape → intenter.gif + intenter.png
```

`intenter.tape` drives `session.sh`, a scripted session that uses the real
binary against a fixture project and a Claude shim: `setup claude --dry-run`, an
evaluated hook payload for `npm run cleanup`, `intenter approve`, a second
evaluation that matches the approval, the `package.json` change, and
`intenter history show` on the block that follows. Nothing in it is mocked —
what the recording shows is what the binary printed.

`session.sh` runs against a throwaway `HOME` under a temporary directory and
pipes every command's output through a filter that rewrites that directory back
to `~`. Without it the frames would carry the recorder's own temporary paths,
which both dates the recording and looks like a leak; with it the paths read as
`~/proj` and `~/.local/share/intenter/…`, which is what a viewer sees on their
own machine. The daemon's socket also lives under that root, hence the very
short directory prefix — a unix socket path much over 100 characters cannot be
bound.

`intenter.png` is the closing frame, captured by the tape's `Screenshot`
command: the block and its explanation, which is the one frame worth showing to
someone whose client will not play the GIF.

## The caption has to match what is embedded

The README caption is part of the claim. While the SVG is the hero visual it
says the image *illustrates* the scripted session — because a hand-drawn panel
is not a recording, and calling it one would be the kind of small untruth this
whole page is built to avoid.

**When `intenter.gif` replaces the SVG in the README, change the caption to
"recorded from a scripted session against a fixture project."** That wording is
then accurate, and it is what
`specs/004-github-marketing-page/contracts/readme-and-collateral.md` asks for.

Budgets: GIF ≤ 3 MB and ≤ 30 s at 1200×640. `scripts/check-readme.sh` enforces
the size budget and the alt text; the caption is on you.

Regenerate whenever output formats change; commit the results.
