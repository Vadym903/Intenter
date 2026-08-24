#!/bin/sh
# Intenter installer for macOS and Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/Vadym903/Intenter/main/install.sh | sh
#
# Downloads the release build for this machine, verifies its checksum, and puts
# the binary somewhere the shell can find it. Run it again to upgrade, or with
# --uninstall to remove it.
#
# Written in POSIX sh — no bashisms — because /bin/sh is dash on Debian and
# BusyBox on Alpine, and an installer that only works on the author's shell is
# not an installer.
set -eu

# Distribution constants. These strings are identical in install.ps1 and in the
# README install section, so a repository move is a single search and replace.
REPO="${INTENTER_REPO:-Vadym903/Intenter}"
DOWNLOAD_BASE="${INTENTER_DOWNLOAD_BASE:-https://github.com/${REPO}/releases/download}"
LATEST_URL="${INTENTER_LATEST_URL:-https://github.com/${REPO}/releases/latest}"

# Downloads from the published release are pinned to HTTPS, so a redirect can
# never move them to plaintext. Pointing the installer somewhere else — which
# the tests and the pre-publish verification job do, at a local server — lifts
# the restriction, because by then the user has chosen the source themselves.
CURL_PROTO="--proto =https --tlsv1.2"
case "${DOWNLOAD_BASE}${LATEST_URL}" in
*http://*) CURL_PROTO="" ;;
esac

VERSION="${INTENTER_VERSION:-latest}"
INSTALL_DIR="${INTENTER_INSTALL_DIR:-$HOME/.local/bin}"
MODIFY_PATH=1
[ "${INTENTER_NO_MODIFY_PATH:-}" = "1" ] && MODIFY_PATH=0

MODE=install
SETUP_AGENT=""
PURGE=0
DRY_RUN=0

# Exit codes, so a script wrapping this one can tell the cases apart.
#   1 usage, unsupported platform
#   2 download or version resolution failed
#   3 checksum verification failed
#   4 could not write the install directory
#   5 uninstall completed with warnings
#   6 post-install step failed (daemon restart, setup)
#   8 signature verification failed (same family as 3; see internal/updater's
#     ExitSignature, contracts/release-and-signing.md §3)
EXIT_USAGE=1
EXIT_DOWNLOAD=2
EXIT_CHECKSUM=3
EXIT_WRITE=4
EXIT_UNINSTALL=5
EXIT_POSTINSTALL=6
EXIT_SIGNATURE=8

PATH_MARKER_BEGIN='# >>> intenter >>>'
PATH_MARKER_END='# <<< intenter <<<'

# is_test_mode reports whether the test-only overrides below apply. Same gate
# as internal/platform's EnvTestMode: a real installation must never be
# steerable by a variable left in a shell profile (audit AG-08).
is_test_mode() { [ "${INTENTER_TEST_MODE:-}" = "1" ]; }

# embedded_public_key prints the pinned release signing key (research R-05,
# contracts/release-and-signing.md §2): the same PEM committed at the
# repository root as cosign.pub, embedded so verification does not depend on
# finding that file on the machine it is protecting.
embedded_public_key() {
	cat <<'EOF'
-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE+9zI6vPn9ZfPtjb4MWC1z2NcL1oB
KeWZibnfrHoQzxZttzl1kcFzmroK9jlPfn4LdCQbZVN9rAec09WtMMo+tA==
-----END PUBLIC KEY-----
EOF
}

# Names and markers the pre-rename installer wrote. Recognized only so an
# upgrade or --uninstall can remove them (contracts/identity-and-rename.md
# §2.3) — nothing legacy is ever used or trusted. # legacy
LEGACY_BINARY_NAME='agentguard' # legacy: pre-rename binary name
LEGACY_PATH_MARKER_BEGIN='# >>> agentguard >>>' # legacy: pre-rename PATH block marker
LEGACY_PATH_MARKER_END='# <<< agentguard <<<' # legacy: pre-rename PATH block marker
LEGACY_FISH_CONF_NAME='agentguard.fish' # legacy: pre-rename fish conf file name

# POSIX sh has no local variables: every assignment inside a function is global,
# so a helper that reuses a caller's name silently overwrites it. Helper
# variables are therefore prefixed with the function's initials — the bug this
# prevents (a `target` in a download helper redirecting the install) is invisible
# until someone looks at where the binary actually went.

say() { echo "$*"; }
warn() { echo "install.sh: $*" >&2; }

die() {
	_d_code="$1"
	shift
	warn "$*"
	exit "$_d_code"
}

usage() {
	cat <<'USAGE'
Install Intenter, a semantic permission layer for AI coding agents.

Usage:
  curl -fsSL <url>/install.sh | sh
  curl -fsSL <url>/install.sh | sh -s -- [options]

Options:
  --version <v>      install a specific version instead of the latest
  --prefix <dir>     install into <dir> (default: ~/.local/bin)
  --no-modify-path   do not touch shell startup files
  --setup claude     run `intenter setup claude` after installing
  --uninstall        remove Intenter (hooks, service, binary, PATH entry)
  --purge            with --uninstall, also delete approvals and history
  --dry-run          print what would happen and change nothing
  --yes              accepted and ignored; this script never prompts
  --help             show this message

Environment:
  INTENTER_VERSION, INTENTER_INSTALL_DIR, INTENTER_NO_MODIFY_PATH=1,
  INTENTER_REPO, INTENTER_DOWNLOAD_BASE, INTENTER_LATEST_URL
  (a flag always wins over the matching variable)
USAGE
}

parse_args() {
	while [ $# -gt 0 ]; do
		case "$1" in
		--version)
			[ $# -ge 2 ] || die "$EXIT_USAGE" "--version needs a value"
			VERSION="$2"
			shift 2
			;;
		--version=*)
			VERSION="${1#--version=}"
			shift
			;;
		--prefix)
			[ $# -ge 2 ] || die "$EXIT_USAGE" "--prefix needs a directory"
			INSTALL_DIR="$2"
			shift 2
			;;
		--prefix=*)
			INSTALL_DIR="${1#--prefix=}"
			shift
			;;
		--no-modify-path)
			MODIFY_PATH=0
			shift
			;;
		--setup)
			[ $# -ge 2 ] || die "$EXIT_USAGE" "--setup needs an agent name, e.g. --setup claude"
			SETUP_AGENT="$2"
			shift 2
			;;
		--setup=*)
			SETUP_AGENT="${1#--setup=}"
			shift
			;;
		--uninstall)
			MODE=uninstall
			shift
			;;
		--purge)
			PURGE=1
			shift
			;;
		--dry-run)
			DRY_RUN=1
			shift
			;;
		--yes | -y)
			# There are no prompts; accepted so a habitual --yes does not fail.
			shift
			;;
		--help | -h)
			usage
			exit 0
			;;
		*)
			usage >&2
			die "$EXIT_USAGE" "unknown option: $1"
			;;
		esac
	done

	if [ -n "$SETUP_AGENT" ] && [ "$SETUP_AGENT" != "claude" ]; then
		die "$EXIT_USAGE" "unknown agent: $SETUP_AGENT (only 'claude' is supported)"
	fi
}

# detect_platform prints "<os>_<arch>" for the release asset names.
detect_platform() {
	_dp_os="$(uname -s)"
	_dp_arch="$(uname -m)"

	case "$_dp_os" in
	Darwin) _dp_os="darwin" ;;
	Linux) _dp_os="linux" ;;
	*)
		die "$EXIT_USAGE" "unsupported operating system: $_dp_os
On Windows, run the PowerShell installer instead:
  irm https://raw.githubusercontent.com/${REPO}/main/install.ps1 | iex
Otherwise build from source: https://github.com/${REPO}#building-from-source"
		;;
	esac

	case "$_dp_arch" in
	x86_64 | amd64) _dp_arch="amd64" ;;
	arm64 | aarch64) _dp_arch="arm64" ;;
	*)
		die "$EXIT_USAGE" "unsupported architecture: $_dp_arch
Intenter publishes amd64 and arm64 builds. To run on $_dp_arch, build from source:
  https://github.com/${REPO}#building-from-source"
		;;
	esac

	echo "${_dp_os}_${_dp_arch}"
}

# resolve_version prints the version to install, without a leading v.
#
# "latest" is resolved by following the redirect from the releases/latest URL
# rather than by calling the GitHub API, which is rate-limited per IP and would
# fail for exactly the users behind a large corporate NAT.
resolve_version() {
	if [ "$VERSION" != "latest" ]; then
		echo "${VERSION#v}"
		return
	fi

	_rv_effective="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$LATEST_URL" 2>/dev/null || true)"
	_rv_tag="${_rv_effective##*/tag/}"
	case "$_rv_tag" in
	"" | "$_rv_effective")
		die "$EXIT_DOWNLOAD" "cannot determine the latest release from $LATEST_URL$(proxy_hint)
Install a specific version instead:
  curl -fsSL <url>/install.sh | sh -s -- --version X.Y.Z"
		;;
	esac
	echo "${_rv_tag#v}"
}

# proxy_hint names the proxy in an error when one is configured, because a
# failure through a proxy looks identical to a failure without one.
proxy_hint() {
	_ph_proxy="${HTTPS_PROXY:-${https_proxy:-}}"
	[ -n "$_ph_proxy" ] && printf ' (via proxy %s)' "$_ph_proxy"
}

# download fetches a URL to a path.
download() {
	_dl_url="$1"
	_dl_target="$2"
	# Unquoted on purpose: CURL_PROTO is either empty or two flags.
	# shellcheck disable=SC2086
	curl -fsSL $CURL_PROTO "$_dl_url" -o "$_dl_target" ||
		die "$EXIT_DOWNLOAD" "download failed: $_dl_url$(proxy_hint)
Nothing was installed. Download the release by hand from
  https://github.com/${REPO}/releases
and follow the manual steps in docs/install.md."
}

# verify_checksum checks one archive against the release checksums file.
verify_checksum() {
	_vc_dir="$1"
	_vc_archive="$2"

	_vc_line="$(grep " \{1,2\}${_vc_archive}\$" "${_vc_dir}/checksums.txt" || true)"
	[ -n "$_vc_line" ] ||
		die "$EXIT_CHECKSUM" "checksum verification failed: ${_vc_archive} is not listed in checksums.txt
Nothing was installed. This should not happen for a published release; please
report it at https://github.com/${REPO}/issues"

	if command -v sha256sum >/dev/null 2>&1; then
		_vc_tool="sha256sum -c -"
	elif command -v shasum >/dev/null 2>&1; then
		_vc_tool="shasum -a 256 -c -"
	else
		die "$EXIT_CHECKSUM" "neither sha256sum nor shasum is available, so the download cannot be verified
Verification is not optional here. Install coreutils (or use a machine that has
it), or verify the download by hand following
  https://github.com/${REPO}/blob/main/docs/install.md"
	fi

	# shellcheck disable=SC2086
	(cd "$_vc_dir" && echo "$_vc_line" | $_vc_tool >/dev/null 2>&1) ||
		die "$EXIT_CHECKSUM" "checksum verification failed for ${_vc_archive}
The download does not match the checksum published with the release. Nothing was
installed. Try again; if it keeps failing, report it at
  https://github.com/${REPO}/issues"

	# The hash is printed so it can be compared with the release page by hand.
	say "verified sha256 $(echo "$_vc_line" | cut -d' ' -f1)"
}

# signature_notice is the one line printed when neither verifier is on PATH:
# the download was still checksum-verified, but provenance was not confirmed.
signature_notice() {
	echo "intenter: signature not verified (no cosign or openssl on PATH); the download was checksum-verified. See https://github.com/${REPO}/blob/main/docs/install.md#verifying-a-download-by-hand" >&2
}

# verify_signature checks checksums.txt.sig against the pinned release key:
# cosign if it is on PATH, else openssl, else the one-line notice above. A
# failed verification with a verifier present is fatal — nothing is installed
# from a release whose checksums cannot be trusted (research R-05,
# contracts/release-and-signing.md §3).
verify_signature() {
	_vs_dir="$1"
	_vs_checksums="${_vs_dir}/checksums.txt"
	_vs_sig="${_vs_dir}/checksums.txt.sig"

	_vs_keyfile="${_vs_dir}/cosign.pub"
	if is_test_mode && [ -n "${INTENTER_SIGNING_KEY_FILE:-}" ]; then
		_vs_keyfile="$INTENTER_SIGNING_KEY_FILE"
	else
		embedded_public_key >"$_vs_keyfile"
	fi

	# Test-only: exercises the no-verifier notice without manipulating PATH.
	if is_test_mode && [ "${INTENTER_TEST_NO_VERIFIER:-}" = "1" ]; then
		signature_notice
		return 0
	fi

	# openssl first: it checks exactly what the updater checks (the ECDSA
	# signature against the pinned key), works offline, and is on nearly every
	# machine. cosign is the fallback; its transparency-log lookup is switched
	# off because the pinned key is the trust anchor here and a lookup would
	# need the network (air-gapped mirrors, the release pipeline's own local
	# verification) — the signature itself is still fully verified.
	if command -v openssl >/dev/null 2>&1; then
		_vs_sig_der="${_vs_dir}/checksums.txt.sig.der"
		# A signature that does not decode is as bad as one that does not
		# verify: both refuse the release.
		if ! base64 -d <"$_vs_sig" >"$_vs_sig_der" 2>/dev/null ||
			! openssl dgst -sha256 -verify "$_vs_keyfile" -signature "$_vs_sig_der" "$_vs_checksums" >/dev/null 2>&1; then
			die "$EXIT_SIGNATURE" "signature verification failed for checksums.txt
Nothing was installed; the release may have been tampered with. Report it at
  https://github.com/${REPO}/issues"
		fi
		say "verified signature (openssl)"
		return 0
	fi

	if command -v cosign >/dev/null 2>&1; then
		cosign verify-blob --key "$_vs_keyfile" --signature "$_vs_sig" --insecure-ignore-tlog=true "$_vs_checksums" >/dev/null 2>&1 ||
			die "$EXIT_SIGNATURE" "signature verification failed for checksums.txt
Nothing was installed; the release may have been tampered with. Report it at
  https://github.com/${REPO}/issues"
		say "verified signature (cosign)"
		return 0
	fi

	signature_notice
}

# installed_version prints the version of an existing install, or nothing.
installed_version() {
	_iv_binary="$1"
	[ -x "$_iv_binary" ] || return 0
	"$_iv_binary" version 2>/dev/null | head -n 1 | awk '{print $2}' || true
}

# change_verb prints "upgraded" or "downgraded" for a version change.
#
# Going backwards is allowed — it is how someone bisects a regression, and how
# support says "please try 0.1.0" — but it should be named for what it is, so
# nobody reads a summary line and believes they are now on the newest release.
change_verb() {
	_cv_from="$1"
	_cv_to="$2"

	# Numeric fields only: a pre-release suffix is dropped for the comparison,
	# and equal cores fall through to "upgraded", which is the common case
	# (0.2.0-rc.1 → 0.2.0).
	_cv_older="$(printf '%s\n%s\n' "${_cv_from%%-*}" "${_cv_to%%-*}" | awk -F. '
		NR == 1 { a1 = $1 + 0; a2 = $2 + 0; a3 = $3 + 0; next }
		{
			b1 = $1 + 0; b2 = $2 + 0; b3 = $3 + 0
			if (b1 < a1 || (b1 == a1 && (b2 < a2 || (b2 == a2 && b3 < a3)))) print "yes"
		}
	')"

	if [ "$_cv_older" = "yes" ]; then
		echo "downgraded"
	else
		echo "upgraded"
	fi
}

# shell_rc_files lists the startup files worth adding a PATH line to.
#
# Which shell the user will open next is not knowable from inside a pipe, so
# every rc file that already exists is updated, plus the one belonging to the
# login shell.
shell_rc_files() {
	_rc_files=""
	_rc_shell="$(basename "${SHELL:-/bin/sh}")"

	case "$_rc_shell" in
	zsh) _rc_files="$_rc_files $HOME/.zshrc" ;;
	bash) _rc_files="$_rc_files $HOME/.bashrc" ;;
	esac

	for _rc_candidate in "$HOME/.zshrc" "$HOME/.bashrc" "$HOME/.profile" "$HOME/.bash_profile"; do
		[ -f "$_rc_candidate" ] && _rc_files="$_rc_files $_rc_candidate"
	done
	# On macOS a login shell reads .zprofile rather than .zshrc.
	if [ "$(uname -s)" = "Darwin" ] && { [ "$_rc_shell" = "zsh" ] || [ -f "$HOME/.zprofile" ]; }; then
		_rc_files="$_rc_files $HOME/.zprofile"
	fi

	# Deduplicate while keeping the order stable.
	# shellcheck disable=SC2086
	printf '%s\n' $_rc_files | awk 'NF && !seen[$0]++'
}

# on_path reports whether the install directory is already searched.
on_path() {
	case ":${PATH}:" in
	*":${INSTALL_DIR}:"*) return 0 ;;
	*) return 1 ;;
	esac
}

# add_path_block appends the marker block to one rc file, once.
add_path_block() {
	_ab_file="$1"
	if [ -f "$_ab_file" ] && grep -Fq "$PATH_MARKER_BEGIN" "$_ab_file"; then
		return 0
	fi
	mkdir -p "$(dirname "$_ab_file")"
	{
		echo ""
		echo "$PATH_MARKER_BEGIN"
		echo "# Added by the Intenter installer. Remove this block, or run the"
		echo "# installer with --uninstall, to undo it."
		echo "export PATH=\"${INSTALL_DIR}:\$PATH\""
		echo "$PATH_MARKER_END"
	} >>"$_ab_file"
	say "  added ${INSTALL_DIR} to PATH in ${_ab_file}"
}

# strip_marker_block deletes exactly the block between two markers in one
# file, leaving every other line as it was. Returns 1 without touching the
# file when the begin marker is absent, so callers only report what they
# actually changed. Shared by the current-name and legacy cleanup paths.
strip_marker_block() {
	_sb_file="$1"
	_sb_begin="$2"
	_sb_end="$3"
	[ -f "$_sb_file" ] || return 1
	grep -Fq "$_sb_begin" "$_sb_file" || return 1

	_sb_tmp="${_sb_file}.intenter.tmp"
	# The block was appended after a blank line, so the blank line that
	# immediately precedes it is dropped with it: an install/uninstall cycle
	# must leave the file exactly as it started.
	awk -v begin="$_sb_begin" -v end="$_sb_end" '
		$0 == begin { inblock = 1; pending = 0; next }
		$0 == end { inblock = 0; next }
		inblock { next }
		/^$/ { pending++; next }
		{ while (pending > 0) { print ""; pending-- } print }
	' "$_sb_file" >"$_sb_tmp"

	mv -f "$_sb_tmp" "$_sb_file"
	return 0
}

# remove_path_block deletes exactly the block the installer wrote, leaving every
# other line of the file as it was.
remove_path_block() {
	_rb_file="$1"
	strip_marker_block "$_rb_file" "$PATH_MARKER_BEGIN" "$PATH_MARKER_END" || return 0
	say "  removed the PATH entry from ${_rb_file}"
}

# remove_legacy_path_block strips a PATH block the pre-rename installer wrote
# to one file, if present. # legacy: agentguard PATH block cleanup
remove_legacy_path_block() {
	_lp_file="$1"
	strip_marker_block "$_lp_file" "$LEGACY_PATH_MARKER_BEGIN" "$LEGACY_PATH_MARKER_END" || return 0
	say "  removed the legacy agentguard PATH entry from ${_lp_file}"
}

# remove_legacy_path_blocks strips the pre-rename PATH block from every rc
# file this installer manages. # legacy: agentguard PATH block cleanup
remove_legacy_path_blocks() {
	shell_rc_files | while read -r _lpb_file; do
		[ -n "$_lpb_file" ] && remove_legacy_path_block "$_lpb_file"
	done
}

# remove_legacy_binary deletes a pre-rename binary left in the install
# directory. # legacy: agentguard binary cleanup
remove_legacy_binary() {
	_lb_target="${INSTALL_DIR}/${LEGACY_BINARY_NAME}"
	[ -e "$_lb_target" ] || return 0
	rm -f "$_lb_target"
	say "  removed the legacy agentguard binary at ${_lb_target}"
}

# fish uses its own configuration directory rather than an rc file.
fish_config() { echo "$HOME/.config/fish/conf.d/intenter.fish"; }

# legacy_fish_config is the pre-rename fish conf path. # legacy: agentguard fish conf path
legacy_fish_config() { echo "$HOME/.config/fish/conf.d/${LEGACY_FISH_CONF_NAME}"; }

# remove_legacy_fish_conf deletes the pre-rename fish conf file, if present.
# legacy: agentguard fish conf cleanup
remove_legacy_fish_conf() {
	_lf_file="$(legacy_fish_config)"
	[ -f "$_lf_file" ] || return 0
	rm -f "$_lf_file"
	say "  removed the legacy agentguard fish conf ${_lf_file}"
}

register_path() {
	if [ "$MODIFY_PATH" -eq 0 ]; then
		if ! on_path; then
			say ""
			say "Add ${INSTALL_DIR} to your PATH:"
			say "  export PATH=\"${INSTALL_DIR}:\$PATH\""
			PATH_CHANGED=0
		fi
		return 0
	fi

	# Cleaning up the pre-rename PATH block does not depend on whether the new
	# directory is already searched, so it happens before that check.
	# legacy: agentguard PATH block cleanup on install
	remove_legacy_path_blocks
	remove_legacy_fish_conf

	if on_path; then
		return 0
	fi

	# A `while read` on the right of a pipe runs in a subshell, so anything it
	# assigns is lost; the loop only writes files, and PATH_CHANGED is set here.
	shell_rc_files | while read -r _rp_file; do
		[ -n "$_rp_file" ] && add_path_block "$_rp_file"
	done

	if [ -d "$HOME/.config/fish" ] || [ "$(basename "${SHELL:-}")" = "fish" ]; then
		_rp_fish="$(fish_config)"
		if [ ! -f "$_rp_fish" ]; then
			mkdir -p "$(dirname "$_rp_fish")"
			{
				echo "$PATH_MARKER_BEGIN"
				echo "fish_add_path ${INSTALL_DIR}"
				echo "$PATH_MARKER_END"
			} >"$_rp_fish"
			say "  added ${INSTALL_DIR} to PATH in ${_rp_fish}"
		fi
	fi

	PATH_CHANGED=1
	say ""
	say "For this shell, run:"
	say "  export PATH=\"${INSTALL_DIR}:\$PATH\""
}

unregister_path() {
	shell_rc_files | while read -r _up_file; do
		if [ -n "$_up_file" ]; then
			remove_path_block "$_up_file"
			remove_legacy_path_block "$_up_file"
		fi
	done

	_up_fish="$(fish_config)"
	if [ -f "$_up_fish" ] && grep -Fq "$PATH_MARKER_BEGIN" "$_up_fish"; then
		rm -f "$_up_fish"
		say "  removed ${_up_fish}"
	fi

	remove_legacy_fish_conf
}

# restart_daemon picks up the new binary when a daemon is already registered.
restart_daemon() {
	_rd_binary="$1"
	"$_rd_binary" daemon status >/dev/null 2>&1 || return 0

	if "$_rd_binary" daemon restart >/dev/null 2>&1; then
		say "  restarted the Intenter daemon"
		return 0
	fi
	warn "could not restart the daemon; run this yourself:
  ${_rd_binary} daemon restart"
	return "$EXIT_POSTINSTALL"
}

do_install() {
	command -v curl >/dev/null 2>&1 ||
		die "$EXIT_DOWNLOAD" "curl is required to download Intenter
Install curl, or download the release by hand from
  https://github.com/${REPO}/releases/latest"
	command -v tar >/dev/null 2>&1 || die "$EXIT_DOWNLOAD" "tar is required to unpack the release"

	platform="$(detect_platform)"
	version="$(resolve_version)"
	archive="intenter_${version}_${platform}.tar.gz"
	base="${DOWNLOAD_BASE}/v${version}"
	target="${INSTALL_DIR}/intenter"
	previous="$(installed_version "$target")"

	if [ "$DRY_RUN" -eq 1 ]; then
		say "Would install Intenter ${version} (${platform})"
		say "  from ${base}/${archive}"
		say "  to   ${target}"
		[ -n "$previous" ] && say "  replacing ${previous}"
		[ "$MODIFY_PATH" -eq 1 ] && ! on_path && say "  and add ${INSTALL_DIR} to PATH"
		return 0
	fi

	if [ -n "$previous" ] && [ "$previous" = "$version" ]; then
		say "Intenter ${version} is already installed at ${target}"
		return 0
	fi

	tmp="$(mktemp -d 2>/dev/null || mktemp -d -t intenter)"
	trap 'rm -rf "$tmp"' EXIT INT TERM

	say "Downloading Intenter ${version} (${platform})"
	download "${base}/${archive}" "${tmp}/${archive}"
	download "${base}/checksums.txt" "${tmp}/checksums.txt"
	download "${base}/checksums.txt.sig" "${tmp}/checksums.txt.sig"
	verify_signature "$tmp"
	verify_checksum "$tmp" "$archive"

	tar -xzf "${tmp}/${archive}" -C "$tmp" intenter ||
		die "$EXIT_DOWNLOAD" "the release archive does not contain an intenter binary
Nothing was installed. Please report it at https://github.com/${REPO}/issues"

	mkdir -p "$INSTALL_DIR" || die "$EXIT_WRITE" "cannot create ${INSTALL_DIR}"

	# Staged and renamed rather than written in place, so a half-written binary
	# is never left where the shell would find it.
	staged="${target}.tmp.$$"
	cp "${tmp}/intenter" "$staged" || die "$EXIT_WRITE" "cannot write to ${INSTALL_DIR}"
	chmod 0755 "$staged"
	mv -f "$staged" "$target" || die "$EXIT_WRITE" "cannot replace ${target}"

	# The pre-rename binary, if any, sits next to the one just installed and is
	# never used going forward. # legacy: agentguard binary cleanup on install
	remove_legacy_binary

	PATH_CHANGED=0
	register_path

	postinstall_failed=0
	restart_daemon "$target" || postinstall_failed="$EXIT_POSTINSTALL"

	if [ -n "$SETUP_AGENT" ]; then
		say ""
		# Somebody who declined an edit to their shell files did not ask for a
		# different one, so --no-modify-path also declines the start-up check.
		if [ "$MODIFY_PATH" -eq 0 ]; then
			"$target" setup "$SETUP_AGENT" --no-startup-check || postinstall_failed="$EXIT_POSTINSTALL"
		else
			"$target" setup "$SETUP_AGENT" || postinstall_failed="$EXIT_POSTINSTALL"
		fi
	fi

	say ""
	if [ -n "$previous" ]; then
		say "Intenter ${version} installed to ${target} ($(change_verb "$previous" "$version") from ${previous})"
	else
		say "Intenter ${version} installed to ${target}"
	fi
	if [ -z "$SETUP_AGENT" ]; then
		say "Next step: intenter setup claude"
	elif [ "$MODIFY_PATH" -eq 0 ]; then
		say "To be told about new releases when you open a terminal:"
		say "  intenter update startup enable"
	fi
	if [ "${PATH_CHANGED:-0}" -eq 1 ]; then
		say "Open a new terminal to pick up PATH changes."
	fi

	[ "$postinstall_failed" -eq 0 ] || exit "$postinstall_failed"
}

do_uninstall() {
	target="${INSTALL_DIR}/intenter"

	if [ ! -x "$target" ]; then
		say "Intenter is not installed in ${INSTALL_DIR}; nothing to remove."
		remove_legacy_binary
		unregister_path
		return 0
	fi

	if [ "$DRY_RUN" -eq 1 ]; then
		say "Would remove Intenter from ${target}"
		say "  and its Claude Code hooks and background service"
		[ "$PURGE" -eq 1 ] && say "  and delete approvals and history"
		return 0
	fi

	warnings=0
	say "Removing Intenter"

	# The binary removes its own integration, so hooks and the service go
	# through the same code that installed them (I-9).
	if [ "$PURGE" -eq 1 ]; then
		"$target" uninstall claude --purge || warnings=1
	else
		"$target" uninstall claude || warnings=1
	fi
	[ "$warnings" -eq 0 ] || warn "could not fully remove the Claude Code integration; continuing"

	rm -f "$target" && say "  removed ${target}"
	remove_legacy_binary
	unregister_path

	say ""
	if [ "$PURGE" -eq 1 ]; then
		say "Intenter and its data have been removed."
	else
		say "Intenter has been removed. Your approvals and history are kept;"
		say "re-run the installer with --uninstall --purge to delete them too."
	fi

	[ "$warnings" -eq 0 ] || exit "$EXIT_UNINSTALL"
}

main() {
	parse_args "$@"
	case "$MODE" in
	install) do_install ;;
	uninstall) do_uninstall ;;
	esac
}

# Sourcing the script defines its functions without running anything, so a
# helper can be tested on its own. Nothing in normal use sets this.
if [ "${INTENTER_SOURCE_ONLY:-}" != "1" ]; then
	main "$@"
fi
