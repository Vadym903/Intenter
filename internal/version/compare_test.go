package version

import "testing"

// A daemon decides whether to step aside for a newer binary from this
// comparison, so a misread version has a consequence: either a gate that keeps
// running the old code after an upgrade, or one that restarts when it should
// not. Both are quiet failures, which is why the unreadable cases matter as
// much as the ordered ones.

func TestNewerVersionsAreRecognized(t *testing.T) {
	newer := map[string][2]string{
		"a patch":                    {"0.1.1", "0.1.0"},
		"a minor":                    {"0.2.0", "0.1.9"},
		"a major":                    {"1.0.0", "0.99.99"},
		"a release over its own rc":  {"0.2.0", "0.2.0-rc.1"},
		"a later rc":                 {"0.2.0-rc.2", "0.2.0-rc.1"},
		"an rc of a later version":   {"0.3.0-rc.1", "0.2.0"},
		"a release over a dev build": {"0.1.0", "0.1.0-dev"},
		"a v prefix on either side":  {"v0.2.0", "0.1.0"},
		"numeric beats alphabetic":   {"0.2.0-beta", "0.2.0-1"},
		"a longer prerelease":        {"0.2.0-rc.1.1", "0.2.0-rc.1"},
	}

	for name, pair := range newer {
		t.Run(name, func(t *testing.T) {
			if !IsNewer(pair[0], pair[1]) {
				t.Errorf("IsNewer(%q, %q) = false, want true", pair[0], pair[1])
			}
			if IsNewer(pair[1], pair[0]) {
				t.Errorf("IsNewer(%q, %q) = true, want false — the order must not be symmetric",
					pair[1], pair[0])
			}
		})
	}
}

func TestEqualVersionsAreNotNewer(t *testing.T) {
	equal := []string{"0.1.0", "1.2.3", "0.2.0-rc.1", "0.1.0-dev"}
	for _, v := range equal {
		if IsNewer(v, v) {
			t.Errorf("IsNewer(%q, %q) = true, want false", v, v)
		}
	}
	// A v prefix is spelling, not a version.
	if IsNewer("v1.2.3", "1.2.3") || IsNewer("1.2.3", "v1.2.3") {
		t.Error("a leading v must not change the ordering")
	}
	// Build metadata is ignored by semver.
	if IsNewer("1.2.3+abc", "1.2.3") || IsNewer("1.2.3", "1.2.3+abc") {
		t.Error("build metadata must not change the ordering")
	}
}

func TestAnUnreadableVersionIsNeverNewer(t *testing.T) {
	// The fail-safe direction: a version this build cannot parse must not cause
	// an action. It could come from a build we know nothing about.
	unreadable := []string{
		"", "   ", "latest", "0.1", "1.2.3.4", "one.two.three",
		"0.1.x", "-1.0.0", "v", "0.1.0 ", // trailing space is trimmed, so this one is readable
	}
	for _, v := range unreadable[:len(unreadable)-1] {
		if IsNewer(v, "0.0.1") {
			t.Errorf("IsNewer(%q, …) = true; an unreadable version must not read as newer", v)
		}
		if IsNewer("99.99.99", v) {
			t.Errorf("IsNewer(…, %q) = true; an unreadable current version must not be overtaken", v)
		}
	}
	if !IsNewer("0.1.0 ", "0.0.1") {
		t.Error("surrounding whitespace should be trimmed rather than rejected")
	}
}

func TestOrderingIsTransitive(t *testing.T) {
	// Sorted oldest to newest; every pair must agree with the order.
	ordered := []string{
		"0.1.0-dev", "0.1.0-rc.1", "0.1.0-rc.2", "0.1.0",
		"0.1.1", "0.2.0-rc.1", "0.2.0", "1.0.0",
	}
	for i := range ordered {
		for j := range ordered {
			want := i > j
			if got := IsNewer(ordered[i], ordered[j]); got != want {
				t.Errorf("IsNewer(%q, %q) = %v, want %v", ordered[i], ordered[j], got, want)
			}
		}
	}
}
