//go:build windows

package platform

import (
	"path/filepath"
	"testing"
)

// TestPathRulesPowerShellProfilesAreSensitive is the AG-123 regression: the
// PowerShell profile directories are Windows' equivalent of ~/.bashrc — any
// file there matching $PROFILE (or a host-specific variant) runs at the
// start of every new session, and modules under ...\PowerShell\Modules are
// auto-loaded by name. A shell write to either directory must be as
// protected as the POSIX startup files homePersistenceFiles covers.
func TestPathRulesPowerShellProfilesAreSensitive(t *testing.T) {
	p := testPlatform(t)
	rules := p.PathRules()
	home := p.HomeDir()

	sensitive := []string{
		filepath.Join(home, "Documents", "WindowsPowerShell", "profile.ps1"),
		filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "Documents", "PowerShell", "profile.ps1"),
		filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "Documents", "PowerShell", "Modules", "Evil", "Evil.psm1"),
	}
	for _, path := range sensitive {
		if !rules.IsSensitive(path) {
			t.Errorf("%q is a PowerShell auto-load path and must be sensitive (§16.6, AG-123)", path)
		}
	}

	notSensitive := []string{
		filepath.Join(home, "Documents", "report.docx"),
		filepath.Join(home, "Documents", "PowerShellNotes.txt"),
	}
	for _, path := range notSensitive {
		if rules.IsSensitive(path) {
			t.Errorf("%q must not be sensitive", path)
		}
	}
}
