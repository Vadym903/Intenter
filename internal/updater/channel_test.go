package updater

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstallChannelIsClassifiedFromThePaths(t *testing.T) {
	// The channel decides whether this tool may write over its own executable,
	// so each of these layouts is a decision about someone else's files.
	home := "/Users/dev"

	tests := []struct {
		name     string
		stable   string
		resolved string
		hint     string
		want     string
	}{
		{
			name:     "homebrew on apple silicon",
			stable:   "/opt/homebrew/bin/intenter",
			resolved: "/opt/homebrew/Cellar/intenter/0.1.0/bin/intenter",
			want:     ChannelHomebrew,
		},
		{
			name:     "homebrew on intel macs",
			stable:   "/usr/local/bin/intenter",
			resolved: "/usr/local/Cellar/intenter/0.1.0/bin/intenter",
			want:     ChannelHomebrew,
		},
		{
			name:     "linuxbrew",
			stable:   "/home/linuxbrew/.linuxbrew/bin/intenter",
			resolved: "/home/linuxbrew/.linuxbrew/Cellar/intenter/0.1.0/bin/intenter",
			want:     ChannelHomebrew,
		},
		{
			name:     "the cellar binary run directly",
			stable:   "/opt/homebrew/Cellar/intenter/0.1.0/bin/intenter",
			resolved: "/opt/homebrew/Cellar/intenter/0.1.0/bin/intenter",
			want:     ChannelHomebrew,
		},
		{
			name:     "winget through its links directory",
			stable:   `C:\Users\dev\AppData\Local\Microsoft\WinGet\Links\intenter.exe`,
			resolved: `C:\Users\dev\AppData\Local\Microsoft\WinGet\Packages\Intenter.Intenter_x\intenter.exe`,
			want:     ChannelWinget,
		},
		{
			name:     "winget package directory",
			stable:   `C:\Users\dev\AppData\Local\Microsoft\WinGet\Packages\Intenter.Intenter_x\intenter.exe`,
			resolved: `C:\Users\dev\AppData\Local\Microsoft\WinGet\Packages\Intenter.Intenter_x\intenter.exe`,
			want:     ChannelWinget,
		},
		{
			name:     "the posix installer default",
			stable:   filepath.Join(home, ".local", "bin", "intenter"),
			resolved: filepath.Join(home, ".local", "bin", "intenter"),
			want:     ChannelScript,
		},
		{
			name:     "somewhere the user chose",
			stable:   "/opt/tools/intenter",
			resolved: "/opt/tools/intenter",
			want:     ChannelManual,
		},
		{
			name:     "somewhere the user chose, with the installer's marker",
			stable:   "/opt/tools/intenter",
			resolved: "/opt/tools/intenter",
			hint:     ChannelScript,
			want:     ChannelScript,
		},
		{
			name:     "a package manager beats the marker",
			stable:   "/opt/homebrew/bin/intenter",
			resolved: "/opt/homebrew/Cellar/intenter/0.1.0/bin/intenter",
			hint:     ChannelScript,
			want:     ChannelHomebrew,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifyChannel(test.stable, test.resolved, home, test.hint)
			if got != test.want {
				t.Errorf("channel = %q, want %q", got, test.want)
			}
		})
	}
}

func TestTheWindowsInstallerDefaultIsScript(t *testing.T) {
	// %LOCALAPPDATA% is ~\AppData\Local on every supported Windows.
	home := `C:\Users\dev`
	stable := home + `\AppData\Local\Intenter\bin\intenter.exe`
	if got := classifyChannel(stable, stable, home, ""); got != ChannelScript {
		t.Errorf("channel = %q, want script", got)
	}
}

func TestDetectInstallFollowsASymlinkIntoACellar(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on Windows")
	}
	root := t.TempDir()
	cellar := filepath.Join(root, "Cellar", "intenter", "0.1.0", "bin")
	if err := os.MkdirAll(cellar, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	real := filepath.Join(cellar, "intenter")
	if err := os.WriteFile(real, []byte("binary"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(bin, "intenter")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	install := DetectInstall(link, root, root)
	if install.Channel != ChannelHomebrew {
		t.Errorf("channel = %q, want homebrew", install.Channel)
	}
	if install.PackageManaged() != true || install.SelfManaged() {
		t.Error("a homebrew install must be package-managed, not self-managed")
	}
	if install.Path != link {
		t.Errorf("path = %q, want the stable path %q", install.Path, link)
	}
	if !install.Writable {
		t.Error("the link's directory is writable in this test")
	}
}

func TestDetectInstallReportsAnUnlocatableCopyAsUnknown(t *testing.T) {
	install := DetectInstall("", "/home/dev", "/home/dev/.local/share/intenter")
	if install.Channel != ChannelUnknown {
		t.Errorf("channel = %q, want unknown", install.Channel)
	}
	if install.SelfManaged() || install.PackageManaged() {
		t.Error("an unknown install is neither: the updater must ask first")
	}
}

func TestAReadOnlyLocationIsReported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory mode bits do not deny writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes to read-only directories")
	}
	dir := filepath.Join(t.TempDir(), "readonly")
	if err := os.Mkdir(dir, 0o555); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	install := DetectInstall(filepath.Join(dir, "intenter"), "/home/dev", "")
	if install.Writable {
		t.Error("a read-only directory must not be reported as writable")
	}
}

func TestTheChannelHintIsRead(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, channelHintFile), []byte(" Script \n"), 0o600); err != nil {
		t.Fatalf("write hint: %v", err)
	}
	if got := readChannelHint(dataDir); got != ChannelScript {
		t.Errorf("hint = %q, want script", got)
	}
	if got := readChannelHint(t.TempDir()); got != "" {
		t.Errorf("a missing hint = %q, want empty", got)
	}
}
