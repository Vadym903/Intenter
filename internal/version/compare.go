package version

import (
	"strconv"
	"strings"
)

// IsNewer reports whether candidate is a strictly newer release than current.
//
// Both arguments come from binaries rather than from users, but a version this
// build cannot parse is treated as "not newer": the caller acts on this answer,
// and acting on a misread version is worse than not acting at all.
func IsNewer(candidate, current string) bool {
	newer, ok := compareSemver(candidate, current)
	return ok && newer > 0
}

// Compare orders two versions, reporting whether both could be read: -1 when a
// is older, 0 when they are equal, 1 when a is newer.
//
// IsNewer answers the only question the daemon asks; the updater also needs to
// recognize a downgrade, which is the same comparison read the other way.
func Compare(a, b string) (int, bool) { return compareSemver(a, b) }

// IsPrerelease reports whether a version carries a pre-release suffix. An
// unreadable version is not a pre-release: the caller uses this to decide
// whether an installation follows the pre-release channel, and guessing "yes"
// would opt someone in to builds they never asked for.
func IsPrerelease(v string) bool {
	_, prerelease, ok := splitSemver(v)
	return ok && prerelease != ""
}

// compareSemver orders two versions, reporting whether both could be read. It
// implements the ordering rules of semver 2.0.0 that this project can produce:
// numeric major/minor/patch, and a pre-release suffix that sorts *before* the
// release it leads to (so 0.2.0-rc.1 < 0.2.0).
func compareSemver(a, b string) (int, bool) {
	aCore, aPre, aOK := splitSemver(a)
	bCore, bPre, bOK := splitSemver(b)
	if !aOK || !bOK {
		return 0, false
	}

	for i := 0; i < 3; i++ {
		if aCore[i] != bCore[i] {
			return compareInt(aCore[i], bCore[i]), true
		}
	}

	switch {
	case aPre == "" && bPre == "":
		return 0, true
	case aPre == "":
		// A release outranks any pre-release of the same core version.
		return 1, true
	case bPre == "":
		return -1, true
	}
	return comparePrerelease(aPre, bPre), true
}

// splitSemver parses "v1.2.3-rc.1" into its numeric core and pre-release.
// Build metadata ("+abc") is ignored, as semver requires.
func splitSemver(v string) (core [3]int, prerelease string, ok bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return core, "", false
	}
	if plus := strings.IndexByte(v, '+'); plus >= 0 {
		v = v[:plus]
	}
	if dash := strings.IndexByte(v, '-'); dash >= 0 {
		prerelease = v[dash+1:]
		v = v[:dash]
	}

	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return core, "", false
	}
	for i, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return core, "", false
		}
		core[i] = number
	}
	return core, prerelease, true
}

// comparePrerelease orders two pre-release suffixes field by field: numeric
// fields numerically, others lexically, numeric below non-numeric, and a
// shorter run of equal fields below a longer one.
func comparePrerelease(a, b string) int {
	aFields, bFields := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(aFields) && i < len(bFields); i++ {
		if result := comparePrereleaseField(aFields[i], bFields[i]); result != 0 {
			return result
		}
	}
	return compareInt(len(aFields), len(bFields))
}

func comparePrereleaseField(a, b string) int {
	aNumber, aNumeric := strconv.Atoi(a)
	bNumber, bNumeric := strconv.Atoi(b)
	switch {
	case aNumeric == nil && bNumeric == nil:
		return compareInt(aNumber, bNumber)
	case aNumeric == nil:
		return -1
	case bNumeric == nil:
		return 1
	}
	return strings.Compare(a, b)
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
