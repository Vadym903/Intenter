#!/bin/sh
# check-readme.sh — keeps the marketing surface truthful.
#
# Usage: scripts/check-readme.sh [repo-root]
#
# Runs the README/collateral rules from
# specs/004-github-marketing-page/contracts/readme-and-collateral.md and exits
# non-zero on the first failing rule, naming it. It is wired into
# `make docs-check` and the CI docs job.
#
# The rules exist because a landing page drifts one commit at a time: a badge
# that 404s, a claim that quietly lost its link, "open source" slipping back
# into a sentence, a version number three releases old. None of that is caught
# by a human re-reading the page.
set -eu

ROOT="${1:-.}"
cd "$ROOT"

fail() {
	echo "check-readme: FAIL: $*" >&2
	exit 1
}
ok() { echo "check-readme: ok: $*"; }

README="README.md"
[ -f "$README" ] || fail "README.md not found in $(pwd)"

# 1. Placeholders that must never ship. Owner inputs are collected before
#    merge; while they are missing this check is the gate.
PLACEHOLDER_FILES="README.md CONTRIBUTING.md CHANGELOG.md LICENSE llms.txt CODE_OF_CONDUCT.md SECURITY.md SUPPORT.md"
for f in $PLACEHOLDER_FILES; do
	[ -f "$f" ] || continue
	if grep -n -F -e 'TODO(' -e '<COPYRIGHT HOLDER>' -e '<CONTACT>' -e '<YEAR>' "$f"; then
		fail "$f still contains a placeholder (TODO(, <COPYRIGHT HOLDER>, <CONTACT>, <YEAR>)"
	fi
done
for d in docs .github; do
	[ -d "$d" ] || continue
	if grep -RIn -F -e 'TODO(' -e '<COPYRIGHT HOLDER>' -e '<CONTACT>' -e '<YEAR>' "$d"; then
		fail "$d/ still contains a placeholder"
	fi
done
ok "no placeholders"

# 2. Never describe the project as open source. "not open-source software in
#    the OSI sense" is the one permitted phrase, so lines that negate it pass.
for f in README.md llms.txt; do
	[ -f "$f" ] || continue
	if grep -n -i -E 'open[- ]source (software|project|tool)|is open[- ]source' "$f" |
		grep -v -i -E 'not open[- ]source|OSI' | grep -q .; then
		grep -n -i -E 'open[- ]source (software|project|tool)|is open[- ]source' "$f" | grep -v -i -E 'not open[- ]source|OSI' >&2
		fail "$f describes the project as open source; it is source-available (PolyForm Noncommercial)"
	fi
done
ok "no open-source self-description"

# 3. Every image has alt text.
if grep -n -F '![](' "$README"; then
	fail "README.md has an image without alt text"
fi
ok "images have alt text"

# 4. The canonical sentence is identical everywhere it must appear.
CANON_FILE="docs/marketing/canonical.md"
if [ -f "$CANON_FILE" ]; then
	canonical="$(head -n 1 "$CANON_FILE")"
	[ -n "$canonical" ] || fail "$CANON_FILE line 1 is empty"
	for f in README.md llms.txt docs/marketing/pitch.md assets/social/preview.svg; do
		[ -f "$f" ] || continue
		grep -q -F -- "$canonical" "$f" || fail "$f does not contain the canonical sentence from $CANON_FILE"
	done
	ok "canonical sentence consistent"
fi

# 5. No release version literals outside <!-- example --> blocks. Version
#    strings that are not Intenter releases (the license, the Code of Conduct)
#    are stripped before matching.
if awk '
	/<!-- example -->/ { inex = 1; next }
	/<!-- \/example -->/ { inex = 0; next }
	inex { next }
	{
		line = $0
		gsub(/PolyForm[- ]Noncommercial( License)?[- ]1\.0\.0/, "", line)
		# The license badge carries the same version percent-encoded, and
		# %201.0.0 reads as the version literal "201.0.0" to the rule below.
		gsub(/PolyForm%20Noncommercial%201\.0\.0/, "", line)
		gsub(/Contributor Covenant[ ,]*(version )?[0-9]+\.[0-9]+(\.[0-9]+)?/, "", line)
		gsub(/[Ss]emantic [Vv]ersioning[^ ]* ?[0-9]+\.[0-9]+\.[0-9]+/, "", line)
		if (line ~ /(^|[^A-Za-z0-9.])v?[0-9]+\.[0-9]+\.[0-9]+([^0-9]|$)/) { print NR ": " $0; found = 1 }
	}
	END { exit found ? 0 : 1 }
' "$README"; then
	fail "README.md contains a version literal outside an <!-- example --> block (see lines above)"
fi
ok "no stray version literals"

# 6. Asset size budgets (only when the assets exist).
size_of() {
	# wc -c is portable; stat flags are not.
	wc -c <"$1" | tr -d ' '
}
if [ -f assets/demo/intenter.gif ]; then
	[ "$(size_of assets/demo/intenter.gif)" -le 3145728 ] || fail "assets/demo/intenter.gif exceeds 3 MB"
fi
if [ -f assets/social/preview.png ]; then
	[ "$(size_of assets/social/preview.png)" -le 1048576 ] || fail "assets/social/preview.png exceeds 1 MB"
fi
ok "asset sizes within budget"

# 7. Licensing consistency: LICENSE, README section, badge and contact agree.
if [ -f LICENSE ]; then
	head -n 10 LICENSE | grep -q -F 'SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0' ||
		fail "LICENSE does not start with the PolyForm-Noncommercial-1.0.0 SPDX header"
	grep -q -F 'PolyForm Noncommercial License 1.0.0' LICENSE ||
		fail "LICENSE does not contain the PolyForm Noncommercial License 1.0.0 text"
fi
license_section="$(awk '/^## License/{on=1; next} /^## /{if(on) exit} on' "$README")"
[ -n "$license_section" ] || fail "README.md has no '## License' section"
printf '%s\n' "$license_section" | grep -q -F 'PolyForm Noncommercial License 1.0.0' ||
	fail "README.md License section does not name the PolyForm Noncommercial License 1.0.0"
printf '%s\n' "$license_section" | grep -q -i -E 'commercial licen[sc]' ||
	fail "README.md License section does not explain commercial licensing"
printf '%s\n' "$license_section" | grep -q -i -E 'contact' ||
	fail "README.md License section does not give a commercial-licensing contact"
grep -q -F 'License-PolyForm%20Noncommercial%201.0.0' "$README" ||
	fail "README.md license badge does not name PolyForm Noncommercial 1.0.0"
# One contact, and only one. Two addresses in the License section means one of
# them is stale, and the reader has no way to tell which — SC-004 asks for
# exactly one commercial-use contact. A form URL counts as a contact; the link
# to the license text itself does not.
contacts="$(printf '%s\n' "$license_section" |
	grep -o -E '<CONTACT>|mailto:[^ )>]+|[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z][A-Za-z]+|https?://[^ )>]+' |
	grep -v -F 'polyformproject.org' | wc -l | tr -d ' ')"
[ "$contacts" -eq 1 ] ||
	fail "README.md License section gives $contacts commercial-licensing contacts; it must give exactly one"
ok "licensing consistent"

# 8. Section order follows the contract (ordered subsequence of H2 headings).
expected="Why|How it works|What you get|Compared to alternatives|Install|Set up Claude Code|Try it|CLI at a glance|Security & limitations|Updating|FAQ|Documentation|Status & roadmap|Contributing|License"
headings="$(grep -E '^## ' "$README" | sed 's/^## //')"
prev_idx=0
old_ifs="$IFS"
IFS='|'
for section in $expected; do
	IFS="$old_ifs"
	idx="$(printf '%s\n' "$headings" | grep -n -F -x -- "$section" | head -n 1 | cut -d: -f1 || true)"
	[ -n "$idx" ] || fail "README.md is missing the '## $section' section"
	[ "$idx" -gt "$prev_idx" ] || fail "README.md section '## $section' is out of order"
	prev_idx="$idx"
	IFS='|'
done
IFS="$old_ifs"
ok "section order matches the contract"

echo "check-readme: all checks passed"
