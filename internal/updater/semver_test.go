package updater

import "testing"

func TestVersionsAreOrderedBySemver(t *testing.T) {
	tests := []struct {
		candidate string
		current   string
		newer     bool
		older     bool
	}{
		{"0.2.0", "0.1.0", true, false},
		{"0.1.0", "0.2.0", false, true},
		{"0.1.0", "0.1.0", false, false},
		{"v0.2.0", "0.1.0", true, false},
		{"0.2.0", "v0.1.0", true, false},
		{"1.0.0", "0.99.99", true, false},
		{"0.10.0", "0.9.0", true, false},
		// A pre-release sorts below the release it leads to, and above the
		// previous one — the case that decides whether an rc user is offered
		// the final build.
		{"0.2.0", "0.2.0-rc.1", true, false},
		{"0.2.0-rc.1", "0.2.0", false, true},
		{"0.2.0-rc.2", "0.2.0-rc.1", true, false},
		{"0.2.0-rc.1", "0.1.0", true, false},
	}

	for _, test := range tests {
		if got := Newer(test.candidate, test.current); got != test.newer {
			t.Errorf("Newer(%q, %q) = %v, want %v", test.candidate, test.current, got, test.newer)
		}
		if got := Older(test.candidate, test.current); got != test.older {
			t.Errorf("Older(%q, %q) = %v, want %v", test.candidate, test.current, got, test.older)
		}
	}
}

func TestAnUnreadableVersionIsNeverNewerOrOlder(t *testing.T) {
	// Release tags come off the network. Treating one we cannot read as "newer"
	// would offer an update to nothing; treating it as "older" would suppress a
	// real one. Both directions have to answer no.
	// Surrounding whitespace is not corruption — a tag read from an HTTP header
	// or a file arrives with it — so it is trimmed rather than rejected.
	for _, bad := range []string{"", "latest", "1.2", "1.2.3.4", "v", "one.two.three", "-1.0.0"} {
		if Newer(bad, "0.1.0") {
			t.Errorf("Newer(%q, 0.1.0) must be false", bad)
		}
		if Older(bad, "0.1.0") {
			t.Errorf("Older(%q, 0.1.0) must be false", bad)
		}
		if Newer("0.1.0", bad) || Older("0.1.0", bad) {
			t.Errorf("comparisons against %q must all be false", bad)
		}
	}
}

func TestParseVersionRejectsWhatItCannotOrder(t *testing.T) {
	for _, good := range map[string]string{"v0.2.0": "0.2.0", " 0.2.0 ": "0.2.0", "0.2.0-rc.1": "0.2.0-rc.1"} {
		if _, err := ParseVersion(good); err != nil {
			t.Errorf("ParseVersion(%q): %v", good, err)
		}
	}
	if got, err := ParseVersion("v0.2.0"); err != nil || got != "0.2.0" {
		t.Errorf("ParseVersion(v0.2.0) = %q, %v; want 0.2.0", got, err)
	}
	for _, bad := range []string{"", "latest", "1.2", "main"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("ParseVersion(%q) must fail", bad)
		}
	}
}

func TestPrereleaseDetection(t *testing.T) {
	for _, v := range []string{"0.2.0-rc.1", "v1.0.0-beta", "0.1.0-dev"} {
		if !Prerelease(v) {
			t.Errorf("Prerelease(%q) = false", v)
		}
	}
	for _, v := range []string{"0.2.0", "v1.0.0", "", "nonsense"} {
		if Prerelease(v) {
			t.Errorf("Prerelease(%q) = true", v)
		}
	}
}

func TestSameIgnoresThePrefix(t *testing.T) {
	if !Same("v0.2.0", "0.2.0") {
		t.Error("a tag and its version must compare equal")
	}
	if Same("0.2.0", "0.2.1") || Same("0.2.0", "nonsense") {
		t.Error("different or unreadable versions must not compare equal")
	}
}
