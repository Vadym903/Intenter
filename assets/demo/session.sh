#!/bin/sh
# session.sh — the scripted session that intenter.tape records.
#
# Usage: assets/demo/session.sh
#
# It runs the real binary against a fixture project in a throwaway HOME, so the
# recording shows actual output rather than a mock-up, and shows no path,
# project or user name belonging to whoever recorded it. Everything it creates
# lives under one temporary directory and is removed on exit.
#
# The story is the product in four beats: an unknown command is asked about,
# an approval remembers what it resolved to, the same command runs untouched,
# and the moment package.json points the script somewhere else the approval
# stops matching and a hard rule refuses it.
set -eu

REPO="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
BIN="${INTENTER_BIN:-$REPO/bin/intenter}"

if [ ! -x "$BIN" ]; then
	echo "building $BIN ..." >&2
	(cd "$REPO" && go build -o bin/intenter ./cmd/intenter) || {
		echo "session.sh: could not build the binary; set INTENTER_BIN to one you have" >&2
		exit 1
	}
fi

# The prefix is short on purpose: the daemon's unix socket lives under this
# root, and a socket path over ~104 characters cannot be bound at all.
TMP="${TMPDIR:-/tmp}"
TMP="${TMP%/}"
# `cd`-then-`pwd` collapses the duplicate slashes a trailing-slash TMPDIR would
# otherwise leave in the path. Without that the display filter below looks for
# `…/T//ag-demo` while the binary prints `…/T/ag-demo`, and every path in the
# recording keeps the machine's temporary directory in it.
ROOT="$(CDPATH='' cd -- "$(mktemp -d "$TMP/ag-demo.XXXXXX")" && pwd)"
# macOS reaches its temporary directory through a symlink, so some output
# carries the resolved form; the filter has to know both spellings.
ROOT_REAL="$(CDPATH='' cd -- "$ROOT" && pwd -P)"

HOME_DIR="$ROOT/home"
PROJ="$HOME_DIR/proj"
SHIMS="$ROOT/shims"
# The data, config and runtime directories keep their real-world layout under
# the throwaway HOME, so paths the binary prints read as `~/.local/share/...`
# once the display filter has replaced the home directory — the same string a
# viewer will see on their own machine.
mkdir -p "$HOME_DIR" "$PROJ/dist" "$PROJ/src" "$PROJ/.git" "$SHIMS" \
	"$HOME_DIR/.local/share/intenter" "$HOME_DIR/.config/intenter" \
	"$ROOT/run"

# A workspace is a directory with a .git; git itself is not needed to record.
printf 'ref: refs/heads/main\n' >"$PROJ/.git/HEAD"
printf '[remote "origin"]\n\turl = git@github.com:example/proj.git\n' >"$PROJ/.git/config"
printf '{\n  "name": "proj",\n  "scripts": {\n    "cleanup": "rm -rf ./dist"\n  }\n}\n' >"$PROJ/package.json"
printf '# proj\n' >"$PROJ/README.md"
: >"$PROJ/dist/bundle.js"

# `setup claude --dry-run` needs a Claude Code to detect; the fixture provides
# one that only knows how to state its version.
printf '#!/bin/sh\necho 2.1.233\n' >"$SHIMS/claude"
chmod +x "$SHIMS/claude"

HOME="$HOME_DIR"
PATH="$(dirname -- "$BIN"):$SHIMS:/usr/bin:/bin"
INTENTER_TEST_MODE=1
INTENTER_TEST_HOME="$HOME_DIR"
INTENTER_DATA_DIR="$HOME_DIR/.local/share/intenter"
INTENTER_CONFIG_DIR="$HOME_DIR/.config/intenter"
INTENTER_RUNTIME_DIR="$ROOT/run"
INTENTER_ENDPOINT=""
INTENTER_NO_UPDATE_CHECK=1
CLAUDE_PROJECT_DIR="$PROJ"
export HOME PATH INTENTER_TEST_MODE INTENTER_TEST_HOME INTENTER_DATA_DIR \
	INTENTER_CONFIG_DIR INTENTER_RUNTIME_DIR INTENTER_ENDPOINT \
	INTENTER_NO_UPDATE_CHECK CLAUDE_PROJECT_DIR

DAEMON_PID=""
cleanup() {
	[ -z "$DAEMON_PID" ] || kill "$DAEMON_PID" 2>/dev/null || true
	rm -rf "$ROOT"
}
trap cleanup EXIT INT TERM

intenter daemon run >"$ROOT/daemon.log" 2>&1 &
DAEMON_PID=$!
i=0
while [ "$i" -lt 50 ]; do
	if intenter daemon status >/dev/null 2>&1; then break; fi
	sleep 0.1
	i=$((i + 1))
done
if ! intenter daemon status >/dev/null 2>&1; then
	echo "session.sh: the daemon did not start; see $ROOT/daemon.log" >&2
	exit 1
fi

cd "$PROJ"

# Absolute fixture paths are rewritten to `~/proj` so the recording carries no
# temporary directory names, which would date it and look like a leak.
neutral() {
	sed -e "s|$HOME_DIR|~|g" -e "s|$ROOT_REAL/home|~|g" \
		-e "s|$ROOT|<tmp>|g" -e "s|$ROOT_REAL|<tmp>|g"
}

pause() { sleep "${1:-1.2}"; }

# Each step prints the command the way a shell would echo it, then runs it, so
# the viewer sees exactly what produced the output below it.
step() {
	printf '\033[1;34m$\033[0m %s\n' "$1"
	pause 0.6
	shift
	"$@" 2>&1 | neutral || true
	pause
	printf '\n'
}

note() {
	printf '\033[2m# %s\033[0m\n' "$1"
	pause 0.8
}

hook() {
	printf '{"hook_event_name":"PreToolUse","session_id":"%s","cwd":"%s","permission_mode":"default","tool_name":"Bash","tool_use_id":"%s","tool_input":{"command":"%s"}}' \
		"$1" "$PROJ" "$2" "$3" | intenter hook claude >/dev/null 2>&1 || true
}

printf '\033[1mIntenter — approve what a command does, not what it is called\033[0m\n\n'
pause 1.5

step 'intenter setup claude --dry-run' intenter setup claude --dry-run

note 'Claude Code wants to run: npm run cleanup'
hook session-1 toolu_1 'npm run cleanup'
step 'intenter history --limit 1' intenter history --limit 1

note 'It resolves to a delete inside the project. Remember what it does:'
step 'intenter approve 1' intenter approve 1

note 'Next session, same command — no prompt this time.'
hook session-2 toolu_2 'npm run cleanup'
step 'intenter history --limit 1' intenter history --limit 1

note 'A bad merge edits package.json — the same script name, a different target.'
printf '{\n  "name": "proj",\n  "scripts": {\n    "cleanup": "rm -rf ~/Documents"\n  }\n}\n' >"$PROJ/package.json"
step 'cat package.json' cat package.json

note 'Same three words. Ask a third time.'
hook session-3 toolu_3 'npm run cleanup'
# The explanation is the payoff and it is taller than the frame, so the screen
# is cleared first rather than letting the verdict scroll off the top.
clear
step 'intenter history show 3' intenter history show 3

printf '\033[1;32mdemo complete\033[0m — https://github.com/Vadym903/Intenter\n'
pause 2
