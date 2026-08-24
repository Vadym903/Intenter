#!/bin/sh
# Print the CHANGELOG.md section for a version, and fail if there is none.
#
#   scripts/changelog-section.sh v0.2.0
#
# The release workflow runs this before building. A release whose notes nobody
# wrote is a release nobody can read: the tag says what changed only if someone
# said so, and the moment to notice that is before publishing, not after.
set -eu

CHANGELOG="${CHANGELOG_FILE:-CHANGELOG.md}"

if [ $# -ne 1 ]; then
	echo "usage: $0 <version>" >&2
	exit 2
fi

# Accept both `v0.2.0` and `0.2.0`.
version="${1#v}"

if [ ! -f "$CHANGELOG" ]; then
	echo "changelog-section: $CHANGELOG not found" >&2
	exit 1
fi

# Print from the matching `## [<version>]` heading up to the next `## ` heading.
section="$(awk -v version="$version" '
	$0 ~ "^## \\[" version "\\]" { found = 1; print; next }
	found && /^## / { exit }
	found { print }
' "$CHANGELOG")"

if [ -z "$section" ]; then
	echo "changelog-section: $CHANGELOG has no '## [$version]' section" >&2
	echo "Add one before tagging, so the release notes say what changed." >&2
	exit 1
fi

printf '%s\n' "$section"
