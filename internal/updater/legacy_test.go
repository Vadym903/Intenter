package updater

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// legacyBlockText renders the block AgentGuard used to install, for tests
// that need a realistic fixture. Production code never writes this — legacy
// markers are only ever recognized and removed (legacy.go).
func legacyBlockText() string {
	return legacyMarkerBegin + "\n" +
		"# Added by AgentGuard. Remove with: agentguard update startup disable\n" +
		"case $- in *i*)\n" +
		"    if [ -t 0 ] && [ -t 1 ]; then\n" +
		"        agentguard update --startup\n" +
		"    fi\n" +
		"    ;;\n" +
		"esac\n" +
		legacyMarkerEnd + "\n"
}

func TestEnableReplacesALegacyBlockWithTheCurrentOne(t *testing.T) {
	check, _, home := startupCheckFor(t, "linux")
	path := filepath.Join(home, ".zshrc")

	before := "# my prompt\nexport PS1='> '\n"
	original := before + "\n" + legacyBlockText()
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := check.Install([]string{ShellZsh}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	content := readFile(t, path)
	if strings.Contains(content, "agentguard") || strings.Contains(content, legacyMarkerBegin) {
		t.Errorf("the legacy block survived:\n%s", content)
	}
	if got := strings.Count(content, MarkerBegin); got != 1 {
		t.Errorf("want exactly one current block, got %d:\n%s", got, content)
	}
	if !strings.HasPrefix(content, before) {
		t.Errorf("the user's content was not preserved byte-identically:\nwant prefix %q\ngot %q", before, content)
	}
}

func TestDisableRemovesALegacyBlockToo(t *testing.T) {
	// No current block was ever installed on this machine — only the block
	// from before the rename — so disable must still find and remove it.
	check, _, home := startupCheckFor(t, "linux")
	path := filepath.Join(home, ".zshrc")

	before := "# my prompt\nexport PS1='> '\n"
	original := before + "\n" + legacyBlockText()
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := check.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	content := readFile(t, path)
	if content != before {
		t.Errorf("the user's content did not come back byte-identical:\nwant %q\ngot  %q", before, content)
	}
	if strings.Contains(content, "agentguard") {
		t.Errorf("the legacy block was not fully removed:\n%s", content)
	}
}

func TestEnableRemovesTheLegacyFishFileEntirely(t *testing.T) {
	// The legacy fish drop-in existed only to hold the block, exactly like the
	// current one — so once the block is gone, the file it lived in goes too.
	check, _, home := startupCheckFor(t, "linux")
	legacyPath := filepath.Join(home, ".config", "fish", "conf.d", legacyFishBlockFile)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte(legacyBlockText()), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := check.Install([]string{ShellFish}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Errorf("the legacy fish drop-in must be removed, got %v", err)
	}

	currentPath := filepath.Join(home, ".config", "fish", "conf.d", fishBlockFile)
	content := readFile(t, currentPath)
	if !strings.Contains(content, MarkerBegin) {
		t.Errorf("the current fish drop-in must hold the current block:\n%s", content)
	}
	if strings.Contains(content, "agentguard") {
		t.Errorf("the current fish drop-in must not carry the old name:\n%s", content)
	}
}

func TestDisableRemovesTheLegacyFishFileWhenNothingElseIsInstalled(t *testing.T) {
	check, _, home := startupCheckFor(t, "linux")
	legacyPath := filepath.Join(home, ".config", "fish", "conf.d", legacyFishBlockFile)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte(legacyBlockText()), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := check.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Errorf("the legacy fish drop-in must be removed, got %v", err)
	}
	currentPath := filepath.Join(home, ".config", "fish", "conf.d", fishBlockFile)
	if _, err := os.Stat(currentPath); !os.IsNotExist(err) {
		t.Errorf("nothing must be left behind for a shell that was never enabled, got %v", err)
	}
}

func TestEnableReplacesALegacyPowerShellProfileBlock(t *testing.T) {
	check, _, home := startupCheckFor(t, "windows")
	path := filepath.Join(home, "Documents", "WindowsPowerShell", "profile.ps1")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	before := "Set-Alias ll Get-ChildItem\n"
	original := before + "\n" + legacyBlockText()
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := check.Install([]string{ShellPowerShell}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	content := readFile(t, path)
	if strings.Contains(content, "agentguard") || strings.Contains(content, legacyMarkerBegin) {
		t.Errorf("the legacy block survived in the PowerShell profile:\n%s", content)
	}
	if got := strings.Count(content, MarkerBegin); got != 1 {
		t.Errorf("want exactly one current block, got %d:\n%s", got, content)
	}
	if !strings.HasPrefix(content, before) {
		t.Errorf("the user's content was not preserved byte-identically:\nwant prefix %q\ngot %q", before, content)
	}
}

func TestDisableRemovesALegacyPowerShellProfileBlock(t *testing.T) {
	check, _, home := startupCheckFor(t, "windows")
	path := filepath.Join(home, "Documents", "WindowsPowerShell", "profile.ps1")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	before := "Set-Alias ll Get-ChildItem\n"
	original := before + "\n" + legacyBlockText()
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := check.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	content := readFile(t, path)
	if content != before {
		t.Errorf("the user's content did not come back byte-identical:\nwant %q\ngot  %q", before, content)
	}
}

func TestEnableOnAMachineWithoutAnyLegacyLayoutIsUnaffected(t *testing.T) {
	// Regression guard: legacy cleanup must not touch machines that never had
	// the old name installed.
	check, _, home := startupCheckFor(t, "linux")
	path := filepath.Join(home, ".zshrc")
	original := "# my prompt\nexport PS1='> '\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := check.Install([]string{ShellZsh}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := check.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if got := readFile(t, path); got != original {
		t.Errorf("a machine with no legacy layout was changed:\nwant %q\ngot  %q", original, got)
	}
}
