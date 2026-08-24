package resolver

import (
	"path/filepath"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
)

func TestPackageManagerDetection(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  action.PackageManagerKind
	}{
		{"npm lockfile", map[string]string{"package.json": "{}", "package-lock.json": "{}"}, action.PMNpm},
		{"pnpm lockfile", map[string]string{"package.json": "{}", "pnpm-lock.yaml": ""}, action.PMPnpm},
		{"yarn classic lockfile", map[string]string{"package.json": "{}", "yarn.lock": ""}, action.PMYarnClassic},
		{"yarn berry rc", map[string]string{"package.json": "{}", ".yarnrc.yml": "nodeLinker: node-modules"}, action.PMYarnBerry},
		{"packageManager yarn 4", map[string]string{"package.json": `{"packageManager":"yarn@4.1.0"}`}, action.PMYarnBerry},
		{"packageManager yarn 1", map[string]string{"package.json": `{"packageManager":"yarn@1.22.19"}`}, action.PMYarnClassic},
		{"packageManager pnpm", map[string]string{"package.json": `{"packageManager":"pnpm@9.0.0"}`}, action.PMPnpm},
		{"package.json only", map[string]string{"package.json": "{}"}, action.PMNpm},
		{"no manifest", map[string]string{}, action.PMUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRepo(t)
			for name, content := range tt.files {
				r.write(t, name, content)
			}
			if got := DetectPackageManager(r.root, r.home).Kind; got != tt.want {
				t.Errorf("kind = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestScriptShellFromNpmrc(t *testing.T) {
	r := newRepo(t)
	r.write(t, "package.json", "{}")
	r.write(t, ".npmrc", "script-shell=/bin/bash\nregistry=https://registry.example.com\n")

	info := DetectPackageManager(r.root, r.home)
	if info.ScriptShell != "/bin/bash" {
		t.Errorf("script shell = %q", info.ScriptShell)
	}
	if info.ScriptShellSource != filepath.Join(r.root, ".npmrc") {
		t.Errorf("script shell source = %q, want the workspace .npmrc", info.ScriptShellSource)
	}
}

func TestScriptShellFromHomeNpmrc(t *testing.T) {
	r := newRepo(t)
	r.write(t, "package.json", "{}")
	r.writeHome(t, ".npmrc", "script-shell = \"pwsh\"\n")

	info := DetectPackageManager(r.root, r.home)
	if info.ScriptShell != "pwsh" {
		t.Errorf("script shell = %q, want pwsh from the home .npmrc", info.ScriptShell)
	}
}

func TestWorkspaceNpmrcWinsOverHome(t *testing.T) {
	r := newRepo(t)
	r.write(t, ".npmrc", "script-shell=sh\n")
	r.writeHome(t, ".npmrc", "script-shell=cmd\n")

	if got := DetectPackageManager(r.root, r.home).ScriptShell; got != "sh" {
		t.Errorf("script shell = %q, want the workspace value", got)
	}
}

func TestScriptDialectsFollowTheConfiguredShell(t *testing.T) {
	tests := []struct {
		shell string
		want  action.Dialect
	}{
		{"sh", action.DialectPosix},
		{"/bin/bash", action.DialectPosix},
		{"zsh", action.DialectPosix},
		{"pwsh", action.DialectPowerShell},
		{"powershell.exe", action.DialectPowerShell},
		{"cmd", action.DialectCmd},
	}
	for _, tt := range tests {
		info := action.PackageManagerInfo{Kind: action.PMNpm, ScriptShell: tt.shell}
		dialects, ok := ScriptDialects(info, "linux")
		if !ok || len(dialects) != 1 || dialects[0] != tt.want {
			t.Errorf("ScriptDialects(%q) = %v, %v; want [%s]", tt.shell, dialects, ok, tt.want)
		}
	}

	unknown := action.PackageManagerInfo{Kind: action.PMNpm, ScriptShell: "fish"}
	if _, ok := ScriptDialects(unknown, "linux"); ok {
		t.Error("an unrecognized script-shell must make resolution fail (§15.5.4)")
	}
}

func TestScriptDialectsOnWindowsEvaluateBothShells(t *testing.T) {
	info := action.PackageManagerInfo{Kind: action.PMNpm}

	dialects, ok := ScriptDialects(info, "windows")
	if !ok || len(dialects) != 2 {
		t.Fatalf("ScriptDialects on windows = %v, %v; want cmd and posix (I-13)", dialects, ok)
	}
	if dialects[0] != action.DialectCmd || dialects[1] != action.DialectPosix {
		t.Errorf("dialects = %v, want [cmd posix]", dialects)
	}

	unix, ok := ScriptDialects(info, "darwin")
	if !ok || len(unix) != 1 || unix[0] != action.DialectPosix {
		t.Errorf("ScriptDialects on darwin = %v, %v; want [posix]", unix, ok)
	}
}

// AG-142: .yarnrc.yml's yarnPath repoints every `yarn` invocation at a
// project-supplied JavaScript file — the standard mechanism Yarn Berry uses
// to pin its own release — which Intenter has no way to inspect.
func TestYarnPathOverrideIsDetected(t *testing.T) {
	r := newRepo(t)
	r.write(t, "package.json", "{}")
	r.write(t, ".yarnrc.yml", "yarnPath: .yarn/releases/yarn-3.6.1.cjs\nnodeLinker: node-modules\n")

	info := DetectPackageManager(r.root, r.home)
	if info.YarnPath != ".yarn/releases/yarn-3.6.1.cjs" {
		t.Errorf("yarn path = %q, want the configured release", info.YarnPath)
	}
}

func TestNoYarnPathWhenUnset(t *testing.T) {
	r := newRepo(t)
	r.write(t, "package.json", "{}")
	r.write(t, ".yarnrc.yml", "nodeLinker: node-modules\n")

	info := DetectPackageManager(r.root, r.home)
	if info.YarnPath != "" {
		t.Errorf("yarn path = %q, want empty when .yarnrc.yml sets no yarnPath", info.YarnPath)
	}
}

// AG-144: a pnpmfile's readPackage/afterAllResolved hooks run during
// dependency resolution and are not disabled by --ignore-scripts, unlike the
// installed packages' own lifecycle scripts.
func TestPnpmFileDefaultNameIsDetected(t *testing.T) {
	r := newRepo(t)
	r.write(t, "package.json", "{}")
	r.write(t, ".pnpmfile.cjs", "module.exports = { hooks: { readPackage() {} } }\n")

	info := DetectPackageManager(r.root, r.home)
	if info.PnpmFile != ".pnpmfile.cjs" {
		t.Errorf("pnpmfile = %q, want .pnpmfile.cjs", info.PnpmFile)
	}
}

func TestPnpmFileConfiguredPathIsDetected(t *testing.T) {
	r := newRepo(t)
	r.write(t, "package.json", "{}")
	r.write(t, ".npmrc", "pnpmfile=scripts/pnpmfile.js\n")
	r.write(t, "scripts/pnpmfile.js", "module.exports = {}\n")

	info := DetectPackageManager(r.root, r.home)
	if info.PnpmFile != "scripts/pnpmfile.js" {
		t.Errorf("pnpmfile = %q, want the configured path", info.PnpmFile)
	}
}

func TestNoPnpmFileWhenAbsent(t *testing.T) {
	r := newRepo(t)
	r.write(t, "package.json", "{}")

	info := DetectPackageManager(r.root, r.home)
	if info.PnpmFile != "" {
		t.Errorf("pnpmfile = %q, want empty when no .pnpmfile.cjs exists", info.PnpmFile)
	}
}

func TestYarnBerryUsesPosixEverywhere(t *testing.T) {
	info := action.PackageManagerInfo{Kind: action.PMYarnBerry}
	dialects, ok := ScriptDialects(info, "windows")
	if !ok || len(dialects) != 1 || dialects[0] != action.DialectPosix {
		t.Errorf("yarn-berry dialects on windows = %v, %v; want [posix] (§15.5.4)", dialects, ok)
	}
}
