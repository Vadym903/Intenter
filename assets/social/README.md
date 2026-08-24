# Social preview

`preview.svg` is the source for the GitHub social preview — the card that Slack,
X, LinkedIn and Discord render when someone pastes a link to the repository. It
is 1280×640 and carries the project name, the canonical one-sentence definition
from [`docs/marketing/canonical.md`](../../docs/marketing/canonical.md), the
differentiator and the install one-liner. Everything is plain SVG text and
shapes: no external fonts, no embedded raster images, so it stays legible when a
client scales it down to about 600 px wide.

```sh
make social      # preview.svg -> preview.png (1280x640, <= 1 MB)
```

The conversion needs `rsvg-convert` (`librsvg`), Inkscape or ImageMagick —
whichever is installed; `make social` picks one and says so if none is present.
Commit both files: GitHub's social-preview upload takes the PNG, and the SVG is
what the next edit starts from.

Budgets and rules enforced by `scripts/check-readme.sh`: PNG ≤ 1 MB, and the
canonical sentence must appear verbatim inside `preview.svg`. Upload steps and
the unfurl test live in
[`docs/marketing/repo-settings.md`](../../docs/marketing/repo-settings.md).
