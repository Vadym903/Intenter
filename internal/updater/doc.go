// Package updater notices newer published releases and installs them.
//
// It owns everything that happens between "a release exists" and "this machine
// is running it": discovering the latest version, remembering what the user was
// told and what they answered, prompting at terminal start-up, downloading and
// verifying an archive, and replacing the executable in place.
//
// The package is deliberately reachable from both the daemon (which does the
// periodic checking) and the CLI (which does the prompting and updating), so it
// depends only on internal/platform, internal/config and internal/version. It
// must never import internal/adapter/... or internal/cli (invariant I-7,
// enforced by depguard): the Claude-specific half of "did the update break
// anything" is checked by the CLI layer afterwards, using the adapter's own
// doctor helpers.
//
// File map:
//
//	semver.go        version ordering and pre-release detection
//	state.go         <DataDir>/update/state.json: what is known and what was answered
//	lock.go          state.lock (writers) and prompt.lock (one prompt/update at a time)
//	history.go       <DataDir>/update/history.jsonl: why the user was or was not asked
//	check.go         latest-release discovery over HTTPS, with back-off
//	channel.go       how this copy was installed, which decides whether we may replace it
//	download.go      archive + checksums, SHA-256 verification, extraction
//	replace.go       the swap itself (rename-based; replace_windows.go for in-use files)
//	apply.go         the update as a whole: plan, replace or delegate, restart, verify
//	prompt.go        the three-way start-up prompt and its gates
//	startupcheck.go  the managed block in shell start-up files
package updater
