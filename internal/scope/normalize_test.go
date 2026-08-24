package scope

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/platform"
)

// fixture builds a temporary HOME and workspace with the marker files the
// generated-root rules look for (§16.4).
type fixture struct {
	home      string
	workspace string
	temp      string
	ctx       *Context
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()

	home := filepath.Join(base, "home")
	workspace := filepath.Join(home, "projects", "demo")
	temp := filepath.Join(base, "tmp")

	mustMkdir(t,
		filepath.Join(home, "Documents"),
		filepath.Join(home, ".ssh"),
		filepath.Join(workspace, ".git"),
		filepath.Join(workspace, "src"),
		filepath.Join(workspace, "dist"),
		filepath.Join(workspace, "node_modules", "left-pad"),
		filepath.Join(workspace, "build"),
		filepath.Join(workspace, "notes", "build"),
		filepath.Join(workspace, "target"),
		filepath.Join(base, "outside"),
		temp,
	)
	mustWrite(t,
		filepath.Join(workspace, "package.json"), `{"name":"demo"}`,
		filepath.Join(workspace, "pom.xml"), "<project/>",
		filepath.Join(workspace, "src", "main.go"), "package main",
		filepath.Join(home, ".ssh", "id_rsa"), "PRIVATE KEY",
		filepath.Join(workspace, ".env"), "SECRET=1",
	)

	// The workspace root canonicalizes through the temp dir's own symlinks
	// (/var → /private/var on macOS), so resolve it once.
	canonicalHome := mustEval(t, home)
	canonicalWorkspace := mustEval(t, workspace)
	canonicalTemp := mustEval(t, temp)

	// Use the host's real path rules so system roots, case sensitivity and the
	// platform's temp resolution behave exactly as in production; only the
	// home-relative locations are re-pointed at the fixture.
	host, err := platform.New()
	if err != nil {
		t.Fatalf("platform.New: %v", err)
	}
	rules := host.PathRules()
	rules.TempRoots = append(rules.TempRoots, canonicalTemp)
	rules = rules.WithSensitive(
		platform.RecursivePattern(filepath.Join(canonicalHome, ".ssh")),
	)
	rules.ToolCacheDirs = append(rules.ToolCacheDirs,
		platform.RecursivePattern(filepath.Join(canonicalHome, ".npm")))

	return &fixture{
		home:      canonicalHome,
		workspace: canonicalWorkspace,
		temp:      canonicalTemp,
		ctx:       NewContext(rules, canonicalHome, canonicalWorkspace, canonicalTemp, nil),
	}
}

func mustMkdir(t *testing.T, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
}

func mustWrite(t *testing.T, pairs ...string) {
	t.Helper()
	for i := 0; i+1 < len(pairs); i += 2 {
		if err := os.WriteFile(pairs[i], []byte(pairs[i+1]), 0o600); err != nil {
			t.Fatalf("write %s: %v", pairs[i], err)
		}
	}
}

func mustEval(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", path, err)
	}
	return resolved
}

func (f *fixture) normalize(raw string) action.Target {
	return f.ctx.Normalize(Input{Raw: raw, Text: raw, Cwd: f.workspace})
}

func TestNormalizeRelativePathsInsideWorkspace(t *testing.T) {
	f := newFixture(t)

	tests := []struct {
		raw         string
		wantDisplay string
		wantScope   action.Scope
	}{
		{"./dist", "./dist", action.ScopeWorkspaceGenerated},
		{"dist", "./dist", action.ScopeWorkspaceGenerated},
		{"src", "./src", action.ScopeWorkspace},
		{"src/main.go", "./src/main.go", action.ScopeWorkspace},
		{"./src/../dist", "./dist", action.ScopeWorkspaceGenerated},
		{".", ".", action.ScopeWorkspace},
		{"node_modules/left-pad", "./node_modules/left-pad", action.ScopeWorkspaceGenerated},
		{"target", "./target", action.ScopeWorkspaceGenerated},
		{"notes/build", "./notes/build", action.ScopeWorkspace},
	}
	for _, tt := range tests {
		got := f.normalize(tt.raw)
		if got.Display != tt.wantDisplay {
			t.Errorf("Normalize(%q).Display = %q, want %q", tt.raw, got.Display, tt.wantDisplay)
		}
		if got.Scope != tt.wantScope {
			t.Errorf("Normalize(%q).Scope = %s, want %s", tt.raw, got.Scope, tt.wantScope)
		}
		if !filepath.IsAbs(got.Canonical) {
			t.Errorf("Normalize(%q).Canonical = %q, want an absolute path", tt.raw, got.Canonical)
		}
	}
}

func TestNormalizeHomeAndOutsidePaths(t *testing.T) {
	f := newFixture(t)

	documents := f.ctx.Normalize(Input{Raw: "~/Documents", Text: filepath.Join(f.home, "Documents"), Cwd: f.workspace})
	if documents.Scope != action.ScopeHome {
		t.Errorf("~/Documents scope = %s, want HOME", documents.Scope)
	}
	if documents.Display != "~/Documents" {
		t.Errorf("~/Documents display = %q", documents.Display)
	}
	if !documents.HasFlag(action.FlagBroad) {
		t.Error("a standard home directory must be broad (§16.5)")
	}

	home := f.ctx.Normalize(Input{Raw: "~", Text: f.home, Cwd: f.workspace})
	if home.Scope != action.ScopeHome || !home.HasFlag(action.FlagBroad) {
		t.Errorf("~ = %s %v, want HOME + broad", home.Scope, home.Flags)
	}
	if home.Display != "~" {
		t.Errorf("~ display = %q", home.Display)
	}

	outside := f.ctx.Normalize(Input{Raw: "../../../outside", Text: "../../../outside", Cwd: f.workspace})
	if outside.Scope != action.ScopeOutsideWorkspace && outside.Scope != action.ScopeHome {
		t.Errorf("outside scope = %s", outside.Scope)
	}

	systemFile := systemFilePath()
	system := f.ctx.Normalize(Input{Raw: systemFile, Text: systemFile, Cwd: f.workspace})
	if system.Scope != action.ScopeSystem {
		t.Errorf("%s scope = %s, want SYSTEM", systemFile, system.Scope)
	}
	// The display form is the canonical path; macOS resolves /etc to /private/etc.
	if !filepath.IsAbs(system.Display) || !strings.HasSuffix(system.Display, filepath.Base(systemFile)) {
		t.Errorf("system display = %q, want an absolute path", system.Display)
	}

	root := f.ctx.Normalize(Input{Raw: "/", Text: "/", Cwd: f.workspace})
	if root.Scope != action.ScopeSystem || !root.HasFlag(action.FlagBroad) {
		t.Errorf("/ = %s %v, want SYSTEM + broad", root.Scope, root.Flags)
	}

	// A rooted path with no drive letter is anchored at the cwd's drive, never
	// under the cwd: `rm -rf /etc` is not a delete inside the project.
	rooted := f.ctx.Normalize(Input{Raw: "/etc/passwd", Text: "/etc/passwd", Cwd: f.workspace})
	if rooted.Scope == action.ScopeWorkspace || rooted.Scope == action.ScopeWorkspaceGenerated {
		t.Errorf("/etc/passwd scope = %s, must not be inside the workspace", rooted.Scope)
	}
	if !filepath.IsAbs(rooted.Canonical) || filepath.VolumeName(rooted.Canonical) != filepath.VolumeName(f.workspace) {
		t.Errorf("/etc/passwd canonical = %q, want an absolute path on the workspace's drive", rooted.Canonical)
	}
}

// systemFilePath is a file every host keeps in a system location.
func systemFilePath() string {
	if runtime.GOOS != "windows" {
		return "/etc/passwd"
	}
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "drivers", "etc", "hosts")
}

func TestNormalizeTraversalFlag(t *testing.T) {
	f := newFixture(t)

	escaping := f.ctx.Normalize(Input{Raw: "./dist/../../../outside", Text: "./dist/../../../outside", Cwd: f.workspace})
	if !escaping.HasFlag(action.FlagTraversal) {
		t.Errorf("a relative path leaving the workspace must be flagged traversal (§16.1 step 4): %v", escaping.Flags)
	}

	inside := f.ctx.Normalize(Input{Raw: "./src/../dist", Text: "./src/../dist", Cwd: f.workspace})
	if inside.HasFlag(action.FlagTraversal) {
		t.Error("a path that stays inside the workspace must not be flagged traversal")
	}

	absolute := f.ctx.Normalize(Input{Raw: "/etc/passwd", Text: "/etc/passwd", Cwd: f.workspace})
	if absolute.HasFlag(action.FlagTraversal) {
		t.Error("an absolute path outside the workspace is not traversal")
	}
}

func TestNormalizeSymlinkEscape(t *testing.T) {
	f := newFixture(t)

	link := filepath.Join(f.workspace, "build", "link")
	if err := os.Symlink(filepath.Join(f.home, "Documents"), link); err != nil {
		t.Skipf("symlink creation not permitted: %v", err)
	}

	for _, raw := range []string{"build/link", "build/link/"} {
		got := f.ctx.Normalize(Input{Raw: raw, Text: raw, Cwd: f.workspace})
		if got.Scope != action.ScopeHome {
			t.Errorf("%q scope = %s, want HOME via the canonical path (I-14)", raw, got.Scope)
		}
		if !got.HasFlag(action.FlagSymlinkEscape) {
			t.Errorf("%q must be flagged symlink_escape, got %v", raw, got.Flags)
		}
		if got.Scope == action.ScopeWorkspaceGenerated {
			t.Errorf("%q must never be WORKSPACE_GENERATED (S9)", raw)
		}
	}
}

func TestNormalizeWildcardExpandsOnlyEscapingMatches(t *testing.T) {
	f := newFixture(t)

	link := filepath.Join(f.workspace, "build", "link")
	if err := os.Symlink(filepath.Join(f.home, "Documents"), link); err != nil {
		t.Skipf("symlink creation not permitted: %v", err)
	}
	mustWrite(t, filepath.Join(f.workspace, "build", "app.js"), "console.log(1)")

	targets := f.ctx.NormalizeWord(Input{Raw: "build/*", Text: "build/*", Cwd: f.workspace, Glob: true})
	if len(targets) < 2 {
		t.Fatalf("build/* must yield the literal prefix plus the escaping match, got %d: %+v", len(targets), targets)
	}

	primary := targets[0]
	if !primary.HasFlag(action.FlagWildcard) {
		t.Errorf("the primary target must carry the wildcard flag: %v", primary.Flags)
	}
	if primary.Display != "./build/*" {
		t.Errorf("primary display = %q, want ./build/*", primary.Display)
	}

	foundHome := false
	for _, target := range targets[1:] {
		if target.Scope == action.ScopeHome && target.HasFlag(action.FlagSymlinkEscape) {
			foundHome = true
		}
		if strings.HasSuffix(target.Display, "app.js") {
			t.Error("matches that stay inside the prefix must not become targets (approval stability)")
		}
	}
	if !foundHome {
		t.Error("the escaping match must be reported as a HOME target (S9)")
	}
}

func TestNormalizeWildcardWithoutEscapesStaysStable(t *testing.T) {
	f := newFixture(t)
	mustWrite(t,
		filepath.Join(f.workspace, "dist", "a.js"), "a",
		filepath.Join(f.workspace, "dist", "b.js"), "b",
	)

	targets := f.ctx.NormalizeWord(Input{Raw: "dist/*", Text: "dist/*", Cwd: f.workspace, Glob: true})
	if len(targets) != 1 {
		t.Fatalf("a wildcard with no escaping match must yield one stable target, got %+v", targets)
	}
	if targets[0].Scope != action.ScopeWorkspaceGenerated {
		t.Errorf("scope = %s, want WORKSPACE_GENERATED", targets[0].Scope)
	}
}

func TestNormalizeBroadWildcards(t *testing.T) {
	f := newFixture(t)

	homeGlob := f.ctx.Normalize(Input{Raw: "~/*", Text: filepath.Join(f.home, "*"), Cwd: f.workspace, Glob: true})
	if !homeGlob.HasFlag(action.FlagWildcard) || !homeGlob.HasFlag(action.FlagBroad) {
		t.Errorf("~/* must be wildcard + broad (§16.1 step 7), got %v", homeGlob.Flags)
	}
	if homeGlob.Scope != action.ScopeHome {
		t.Errorf("~/* scope = %s, want HOME", homeGlob.Scope)
	}
	if homeGlob.Display != "~/*" {
		t.Errorf("~/* display = %q", homeGlob.Display)
	}

	workspaceGlob := f.ctx.Normalize(Input{Raw: "*", Text: "*", Cwd: f.workspace, Glob: true})
	if !workspaceGlob.HasFlag(action.FlagBroad) {
		t.Errorf("* at the workspace root must be broad, got %v", workspaceGlob.Flags)
	}
}

func TestNormalizeSensitiveAndTempFlags(t *testing.T) {
	f := newFixture(t)

	key := f.ctx.Normalize(Input{Raw: "~/.ssh/id_rsa", Text: filepath.Join(f.home, ".ssh", "id_rsa"), Cwd: f.workspace})
	if !key.HasFlag(action.FlagSensitive) {
		t.Errorf("~/.ssh/id_rsa must be sensitive: %v", key.Flags)
	}

	dotenv := f.normalize(".env")
	if !dotenv.HasFlag(action.FlagSensitive) {
		t.Errorf(".env inside the workspace must be sensitive (§16.6): %v", dotenv.Flags)
	}
	if dotenv.Scope != action.ScopeWorkspace {
		t.Errorf(".env scope = %s, want WORKSPACE", dotenv.Scope)
	}

	tempFile := f.ctx.Normalize(Input{Raw: "tmpfile", Text: filepath.Join(f.temp, "build-1"), Cwd: f.workspace})
	if tempFile.Scope != action.ScopeOutsideWorkspace || !tempFile.HasFlag(action.FlagTemp) {
		t.Errorf("temp paths must be OUTSIDE_WORKSPACE + temp, got %s %v", tempFile.Scope, tempFile.Flags)
	}

	tempRoot := f.ctx.Normalize(Input{Raw: "tmp", Text: f.temp, Cwd: f.workspace})
	if !tempRoot.HasFlag(action.FlagBroad) {
		t.Errorf("the temp root itself must be broad: %v", tempRoot.Flags)
	}
}

func TestNormalizeAmbiguousWord(t *testing.T) {
	f := newFixture(t)

	got := f.ctx.Normalize(Input{Raw: "$SECRET/x", Text: "$SECRET/x", Cwd: f.workspace, Ambiguous: true})
	if got.Status != action.TargetAmbiguous {
		t.Errorf("status = %s, want AMBIGUOUS", got.Status)
	}
	if !got.Ambiguous() {
		t.Error("Ambiguous() must be true")
	}
}

func TestNormalizeStatInformation(t *testing.T) {
	f := newFixture(t)

	dir := f.normalize("src")
	if !dir.Exists || !dir.IsDir {
		t.Errorf("src = exists:%v isDir:%v", dir.Exists, dir.IsDir)
	}

	file := f.normalize("src/main.go")
	if !file.Exists || file.IsDir {
		t.Errorf("src/main.go = exists:%v isDir:%v", file.Exists, file.IsDir)
	}

	missing := f.normalize("does-not-exist")
	if missing.Exists {
		t.Error("a missing path must not be reported as existing")
	}
	if missing.Display != "./does-not-exist" {
		t.Errorf("a missing path must still normalize: %q", missing.Display)
	}
}

func TestNormalizeWindowsStylePaths(t *testing.T) {
	f := newFixture(t)

	unc := f.ctx.Normalize(Input{Raw: `\\server\share\x`, Text: `\\server\share\x`, Cwd: f.workspace, WindowsStyle: true})
	if !unc.HasFlag(action.FlagNetworkPath) {
		t.Errorf("a UNC path must be flagged network_path (§16.1 step 2): %v", unc.Flags)
	}
	if unc.Scope != action.ScopeOutsideWorkspace {
		t.Errorf("UNC scope = %s, want OUTSIDE_WORKSPACE", unc.Scope)
	}

	// A Windows-dialect relative path with backslashes resolves inside the
	// workspace on every host.
	backslash := f.ctx.Normalize(Input{Raw: `src\main.go`, Text: `src\main.go`, Cwd: f.workspace, WindowsStyle: true})
	if backslash.Scope != action.ScopeWorkspace {
		t.Errorf(`src\main.go scope = %s, want WORKSPACE`, backslash.Scope)
	}
	if !strings.HasSuffix(backslash.Display, "src/main.go") {
		t.Errorf("display = %q, want a workspace-relative path", backslash.Display)
	}
}

func TestMsysPathConversion(t *testing.T) {
	got, ok := msysToWindows("/c/Users/u/project")
	if !ok {
		t.Fatal("MSYS paths must be recognized")
	}
	want := "C:" + string(filepath.Separator) + filepath.Join("Users", "u", "project")
	if got != want {
		t.Errorf("msysToWindows = %q, want %q", got, want)
	}

	if _, ok := msysToWindows("/usr/local/bin"); ok {
		t.Error("/usr/local must not be treated as a drive path")
	}
}

func TestSplitGlob(t *testing.T) {
	tests := []struct {
		in            string
		isGlob        bool
		literal, rest string
	}{
		{"dist/*", true, "dist/", "*"},
		{"*", true, ".", "*"},
		{"src/**/*.go", true, "src/", "**/*.go"},
		{"./dist", false, "./dist", ""},
		{"a/b/c*.txt", true, "a/b/", "c*.txt"},
	}
	for _, tt := range tests {
		literal, rest := splitGlob(tt.in, tt.isGlob)
		if literal != tt.literal || rest != tt.rest {
			t.Errorf("splitGlob(%q) = (%q, %q), want (%q, %q)", tt.in, literal, rest, tt.literal, tt.rest)
		}
	}
}

func TestCanonicalizeNonExistingSuffix(t *testing.T) {
	f := newFixture(t)

	got := f.ctx.canonicalize(filepath.Join(f.workspace, "dist", "new", "file.txt"))
	want := filepath.Join(f.workspace, "dist", "new", "file.txt")
	if got != want {
		t.Errorf("canonicalize = %q, want %q", got, want)
	}
}
