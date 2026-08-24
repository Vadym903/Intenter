package updater

import (
	"fmt"
	"strings"

	"github.com/Vadym903/Intenter/internal/version"
)

// The ordering itself lives in internal/version, which already implements the
// semver 2.0.0 rules this project can produce and is used by the daemon's
// self-refresh. Two comparison implementations that disagree by one edge case
// would mean the daemon and the updater disagreeing about which build is newer,
// so there is deliberately only one.

// Normalize strips the `v` prefix release tags carry, so `v0.2.0` and `0.2.0`
// are the same version. It does not validate; use ParseVersion for that.
func Normalize(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// ParseVersion returns the normalized form of a version string, or an error.
//
// Release tags arrive from the network, so "unparsable" is a real case rather
// than a programming mistake: it must stop an update rather than be rounded to
// something plausible.
func ParseVersion(v string) (string, error) {
	normalized := Normalize(v)
	if _, ok := version.Compare(normalized, "0.0.0"); !ok {
		return "", fmt.Errorf("updater: %q is not a version", strings.TrimSpace(v))
	}
	return normalized, nil
}

// Newer reports whether candidate is strictly newer than current. A version
// either side cannot read is never newer.
func Newer(candidate, current string) bool {
	return version.IsNewer(candidate, current)
}

// Older reports whether candidate is strictly older than current — a downgrade,
// which the updater refuses unless it was asked for explicitly.
func Older(candidate, current string) bool {
	order, ok := version.Compare(candidate, current)
	return ok && order < 0
}

// Same reports whether two version strings name the same release.
func Same(a, b string) bool {
	order, ok := version.Compare(a, b)
	return ok && order == 0
}

// Prerelease reports whether a version is a pre-release (`0.2.0-rc.1`).
func Prerelease(v string) bool { return version.IsPrerelease(v) }
