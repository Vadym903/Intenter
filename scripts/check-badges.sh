#!/bin/sh
# check-badges.sh — every badge in README.md must resolve to a real badge.
#
# Usage: scripts/check-badges.sh [README.md]
#
# shields.io answers HTTP 200 for unknown repositories too, with an image that
# says "repo not found" or "invalid", so an HTTP check alone would pass a broken
# badge. The body is inspected as well.
#
# Exit codes: 0 all badges good; 1 at least one badge is broken; 2 the network
# was unreachable (the caller decides whether that is fatal — CI treats it as
# a warning, a 404 or an error badge as a failure).
set -eu

README="${1:-README.md}"
[ -f "$README" ] || { echo "check-badges: $README not found" >&2; exit 1; }
command -v curl >/dev/null 2>&1 || { echo "check-badges: curl is required" >&2; exit 2; }

# Badges inside an HTML comment are not on the page. The release and download
# badges live behind an <!-- after-first-release --> guard because a
# latest-release badge on a repository with no releases renders an error image;
# checking them there would fail every build until the first tag.
visible() {
	awk '
		# A comment that opens and closes on one line is not a block — the
		# example markers around the update-prompt sample are of that shape.
		/<!--.*-->/ { print; next }
		/<!--/ { hidden = 1; next }
		/-->/ { hidden = 0; next }
		hidden { next }
		{ print }
	' "$README"
}

urls="$(visible | grep -o -E 'https://img\.shields\.io/[^ )"]+' | sort -u)"
hidden_count="$(grep -c -E 'https://img\.shields\.io/' "$README" || true)"
shown_count="$(printf '%s' "$urls" | grep -c . || true)"
if [ "$hidden_count" -gt "$shown_count" ]; then
	echo "check-badges: $((hidden_count - shown_count)) badge(s) commented out and not checked (see docs/release-process.md#badges)"
fi
[ -n "$urls" ] || { echo "check-badges: no shields.io badges found in $README"; exit 0; }

broken=0
unreachable=0
for url in $urls; do
	body="$(curl -fsSL -m 15 --retry 2 -A 'intenter-docs-check' "$url" 2>/dev/null)" || {
		echo "check-badges: unreachable: $url" >&2
		unreachable=1
		continue
	}
	if printf '%s' "$body" | grep -q -i -E 'repo not found|not found|invalid|inaccessible|rate limit|no releases'; then
		echo "check-badges: broken (error badge): $url" >&2
		broken=1
		continue
	fi
	echo "check-badges: ok: $url"
done

[ "$broken" -eq 0 ] || exit 1
[ "$unreachable" -eq 0 ] || exit 2
echo "check-badges: all badges resolve"
