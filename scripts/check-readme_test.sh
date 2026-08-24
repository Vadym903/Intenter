#!/bin/sh
# check-readme_test.sh — the rules in check-readme.sh, each proved to fire.
#
# Usage: scripts/check-readme_test.sh
#
# A check that silently stops checking is worse than no check, because the
# green tick is still there. Every rule gets a fixture repository that violates
# exactly that rule and nothing else, and the rule has to be the one that
# fails. One fixture violates nothing, and has to pass — that is what catches a
# rule which fires on correct input.
set -eu

ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
CHECK="$ROOT/scripts/check-readme.sh"
[ -x "$CHECK" ] || {
	echo "check-readme_test: $CHECK is not executable" >&2
	exit 1
}

TMP="$(mktemp -d "${TMPDIR:-/tmp}/ag-readme-test.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT INT TERM

CANON='Intenter resolves commands before deciding.'
TEMPLATE="$TMP/template"

mkdir -p "$TEMPLATE/docs/marketing" "$TEMPLATE/assets/social" "$TEMPLATE/assets/demo"

cat >"$TEMPLATE/README.md" <<'README'
# Intenter

**Approve what a command does, not what it is called.**

[![License: PolyForm Noncommercial 1.0.0](https://img.shields.io/badge/License-PolyForm%20Noncommercial%201.0.0-blue)](LICENSE)

Intenter resolves commands before deciding.

![A terminal showing a blocked command](assets/demo/intenter.svg)

## Why

## How it works

## What you get

## Compared to alternatives

## Install

## Set up Claude Code

## Try it

## CLI at a glance

## Security & limitations

## Updating

<!-- example -->
```text
Intenter 0.2.0 is available (you have 0.1.0).
```
<!-- /example -->

## FAQ

## Documentation

## Status & roadmap

## Contributing

## License

Intenter is source-available under the PolyForm Noncommercial License 1.0.0.
Selling it or using it commercially requires a separate commercial license —
contact licensing@example.com.
README

cat >"$TEMPLATE/LICENSE" <<'LICENSE'
Intenter

Copyright (c) 2026 Example Ltd
SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

# PolyForm Noncommercial License 1.0.0

The licensor grants you a copyright license for the software.
LICENSE

printf '%s\n' "$CANON" >"$TEMPLATE/docs/marketing/canonical.md"
printf '# Pitch\n\n%s\n' "$CANON" >"$TEMPLATE/docs/marketing/pitch.md"
printf '# Intenter\n\n> %s\n' "$CANON" >"$TEMPLATE/llms.txt"
printf '<svg xmlns="http://www.w3.org/2000/svg"><desc>%s</desc></svg>\n' "$CANON" \
	>"$TEMPLATE/assets/social/preview.svg"

passed=0
failed=0

# fixture prints the path of a fresh copy of the template, for the caller to
# break in exactly one way. It has to name the directory itself rather than
# count cases: it runs inside a command substitution, so a counter incremented
# here would be incremented in a subshell and every case would reuse — and
# accumulate — the same directory.
fixture() {
	dir="$(mktemp -d "$TMP/case.XXXXXX")"
	cp -R "$TEMPLATE"/. "$dir"/
	printf '%s' "$dir"
}

report() {
	if [ "$1" = pass ]; then
		passed=$((passed + 1))
		echo "  ok   $2"
	else
		failed=$((failed + 1))
		echo "  FAIL $2"
		[ -z "${3:-}" ] || printf '       %s\n' "$3"
	fi
}

# expect_pass <dir> <name>
expect_pass() {
	if output="$("$CHECK" "$1" 2>&1)"; then
		report pass "$2"
	else
		report fail "$2" "expected the checks to pass; got: $(printf '%s' "$output" | tail -n 1)"
	fi
}

# expect_fail <dir> <name> <substring the failure must mention>
expect_fail() {
	if output="$("$CHECK" "$1" 2>&1)"; then
		report fail "$2" "expected a failure, but every check passed"
		return
	fi
	if printf '%s' "$output" | grep -q -i -F -- "$3"; then
		report pass "$2"
	else
		report fail "$2" "failed for the wrong reason: $(printf '%s' "$output" | tail -n 1)"
	fi
}

echo "check-readme_test: rules"

d="$(fixture)"
expect_pass "$d" "a correct repository passes every rule"

d="$(fixture)"
printf 'Contact <CONTACT> for licensing.\n' >>"$d/README.md"
expect_fail "$d" "an unfilled placeholder in README.md fails" "placeholder"

d="$(fixture)"
mkdir -p "$d/.github"
printf 'Ask <CONTACT>.\n' >"$d/.github/SUPPORT_NOTES.md"
expect_fail "$d" "an unfilled placeholder under .github/ fails" "placeholder"

d="$(fixture)"
printf 'Intenter is open source software.\n' >>"$d/README.md"
expect_fail "$d" "calling the project open source fails" "open source"

d="$(fixture)"
printf 'The negation is allowed: it is not open-source software in the OSI sense.\n' >>"$d/README.md"
expect_pass "$d" "the permitted negation of the open-source claim passes"

d="$(fixture)"
printf '![](assets/demo/intenter.svg)\n' >>"$d/README.md"
expect_fail "$d" "an image without alt text fails" "alt text"

d="$(fixture)"
printf '# Pitch\n\nA different sentence entirely.\n' >"$d/docs/marketing/pitch.md"
expect_fail "$d" "a drifted canonical sentence fails" "canonical"

d="$(fixture)"
printf 'Requires Intenter 1.2.3 or newer.\n' >>"$d/README.md"
expect_fail "$d" "a version literal outside an example block fails" "version literal"

d="$(fixture)"
expect_pass "$d" "a version literal inside an example block passes"

d="$(fixture)"
dd if=/dev/zero of="$d/assets/demo/intenter.gif" bs=1024 count=3200 >/dev/null 2>&1
expect_fail "$d" "a demo GIF over 3 MB fails" "3 MB"

d="$(fixture)"
dd if=/dev/zero of="$d/assets/social/preview.png" bs=1024 count=1100 >/dev/null 2>&1
expect_fail "$d" "a social preview over 1 MB fails" "1 MB"

d="$(fixture)"
printf 'Intenter\n\nNo SPDX header here.\n\n# PolyForm Noncommercial License 1.0.0\n' >"$d/LICENSE"
expect_fail "$d" "a LICENSE without the SPDX header fails" "SPDX"

d="$(fixture)"
printf 'Or write to sales@example.com instead.\n' >>"$d/README.md"
expect_fail "$d" "two commercial contacts fail" "exactly one"

# Moving "Why" past the end puts every section after it earlier than it, which
# is what a reordered README looks like to the rule: the first occurrence of a
# section, not any occurrence, has to come after the one before it.
d="$(fixture)"
sed 's/^## Why$/## Overview/' "$d/README.md" >"$d/reordered" &&
	mv "$d/reordered" "$d/README.md"
printf '\n## Why\n' >>"$d/README.md"
expect_fail "$d" "sections in the wrong order fail" "out of order"

d="$(fixture)"
grep -v '^## FAQ$' "$d/README.md" >"$d/README.trimmed" && mv "$d/README.trimmed" "$d/README.md"
expect_fail "$d" "a missing section fails" "missing"

echo
echo "check-readme_test: $passed passed, $failed failed"
[ "$failed" -eq 0 ]
