#!/bin/sh
# check-rename.sh — the product is Intenter; the old name may not creep back.
#
# Usage: scripts/check-rename.sh [repo-root]
#
# The prototype was developed under the name AgentGuard and renamed in one
# change (specs/005-make-product-usable/contracts/identity-and-rename.md).
# After that, the old identity is allowed to survive in exactly three kinds of
# places, and this script fails on anything else:
#
#   1. legacy-cleanup code, which must name what it removes — any file whose
#      basename contains "legacy", plus the legacy sections of the installers,
#      where every such line must also carry the word "legacy";
#   2. the changelog's rename entry and the local security audit's history note,
#      where every such line must also say "rename" or "under the name";
#   3. the archived feature specs under specs/, and the path
#      specs/001-agentguard-prototype/ that tests and docs still point at.
#
# It is wired into `make docs-check` and the CI docs job.
set -eu

ROOT="${1:-.}"
cd "$ROOT"

fail() {
	echo "check-rename: FAIL: $*" >&2
	exit 1
}
ok() { echo "check-rename: ok: $*"; }

# Files that may carry the old name on every line (their whole purpose is the
# old name), matched on basename.
allowed_whole() {
	case "$(basename "$1")" in
	*legacy*) return 0 ;;
	esac
	case "$1" in
	./scripts/check-rename.sh | scripts/check-rename.sh) return 0 ;;
	esac
	return 1
}

# Files that may carry the old name only on lines that also carry a marker word.
marker_for() {
	case "$1" in
	./install.sh | install.sh | ./install.ps1 | install.ps1) echo 'legacy' ;;
	./CHANGELOG.md | CHANGELOG.md | ./SECURITY_AUDIT.md | SECURITY_AUDIT.md) echo 'rename\|under the name' ;;
	*) echo '' ;;
	esac
}

status=0
found=0

# grep -r rather than rg: the CI runners do not all have ripgrep, and the
# check must not depend on .gitignore (the audit is gitignored on purpose).
files="$(grep -rIil 'agentguard' . \
	--exclude-dir=.git --exclude-dir=specs --exclude-dir=.specify \
	--exclude-dir=.claude --exclude-dir=bin --exclude-dir=dist \
	--exclude-dir=node_modules 2>/dev/null || true)"

for f in $files; do
	found=1
	if allowed_whole "$f"; then
		continue
	fi
	marker="$(marker_for "$f")"
	# Drop the archived spec path, then look at what is left on each line.
	offending="$(grep -in 'agentguard' "$f" | sed 's/001-agentguard-prototype//g' | grep -i 'agentguard' || true)"
	if [ -n "$marker" ] && [ -n "$offending" ]; then
		offending="$(printf '%s\n' "$offending" | grep -iv "$marker" || true)"
	fi
	if [ -n "$offending" ]; then
		echo "check-rename: $f still names the old product:" >&2
		printf '%s\n' "$offending" | sed 's/^/  /' >&2
		status=1
	fi
done

[ "$status" -eq 0 ] || fail "the old name survives outside the allowed places (see above)"
if [ "$found" -eq 1 ]; then
	ok "the old name appears only where the contract allows it"
else
	ok "the old name does not appear at all"
fi
