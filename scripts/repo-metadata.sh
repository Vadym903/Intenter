#!/bin/sh
# repo-metadata.sh — apply the repository description, website and topics.
#
# Usage: scripts/repo-metadata.sh [--dry-run] [owner/repo]
#
# The values live in docs/marketing/repo-settings.md; the description is read
# from docs/marketing/canonical.md so the repository page cannot drift from the
# sentence the README, llms.txt and the social preview all use.
#
# Re-running it is safe: `gh repo edit` sets the description and homepage to
# whatever is passed, and --add-topic on a topic that is already there is a
# no-op. Nothing here removes a topic somebody added deliberately.
#
# The social preview and Discussions are not exposed by the API. They are
# printed as manual steps instead of being silently skipped, because a preview
# nobody uploaded is invisible until someone pastes a link somewhere public.
set -eu

DRY_RUN=0
REPO="Vadym903/Intenter"

for arg in "$@"; do
	case "$arg" in
	--dry-run) DRY_RUN=1 ;;
	-h | --help)
		sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	-*)
		echo "repo-metadata: unknown option: $arg" >&2
		exit 2
		;;
	*) REPO="$arg" ;;
	esac
done

ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
CANON="$ROOT/docs/marketing/canonical.md"
[ -f "$CANON" ] || {
	echo "repo-metadata: $CANON not found" >&2
	exit 1
}

DESCRIPTION="$(head -n 1 "$CANON")"
[ -n "$DESCRIPTION" ] || {
	echo "repo-metadata: $CANON line 1 is empty" >&2
	exit 1
}

# GitHub truncates a description over 160 characters in search results and in
# the sidebar, which would cut the sentence mid-clause.
length="$(printf '%s' "$DESCRIPTION" | wc -c | tr -d ' ')"
[ "$length" -le 160 ] || {
	echo "repo-metadata: the canonical sentence is $length characters; GitHub's limit is 160" >&2
	exit 1
}

HOMEPAGE="https://github.com/$REPO/tree/main/docs"
TOPICS="claude-code,claude,ai-coding-agent,ai-agents,permissions,guardrails,security,developer-tools,cli,golang,allowlist,agent-safety,hooks,devsecops"

if [ "$DRY_RUN" -eq 1 ]; then
	echo "would run:"
	echo "  gh repo edit $REPO \\"
	echo "    --description \"$DESCRIPTION\" \\"
	echo "    --homepage \"$HOMEPAGE\" \\"
	echo "    --add-topic \"$TOPICS\""
else
	command -v gh >/dev/null 2>&1 || {
		echo "repo-metadata: the GitHub CLI (gh) is required: https://cli.github.com" >&2
		exit 1
	}
	gh repo edit "$REPO" \
		--description "$DESCRIPTION" \
		--homepage "$HOMEPAGE" \
		--add-topic "$TOPICS"
	echo "applied to $REPO: description, homepage, ${TOPICS} "
fi

cat <<'MANUAL'

Still to do by hand — the API does not expose these:

  1. Social preview
     Settings -> General -> Social preview -> Edit -> Upload an image
     File: assets/social/preview.png  (run `make social` if it is missing)
     Then run the unfurl test on Slack, X and LinkedIn and record the result
     in docs/marketing/repo-settings.md.

  2. Discussions
     Settings -> General -> Features -> Discussions -> Set up discussions
     Categories: Q&A, Ideas, Show and tell. Delete the defaults you do not use.

  3. Private vulnerability reporting
     Settings -> Code security -> Private vulnerability reporting -> Enable
     SECURITY.md and the issue-template contact link both point at it.

  4. Verify
     Insights -> Community standards -> every item checked.
MANUAL
