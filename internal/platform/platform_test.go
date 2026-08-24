package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
)

func testPlatform(t *testing.T) Platform {
	t.Helper()
	home := t.TempDir()
	data := filepath.Join(t.TempDir(), "data")
	config := filepath.Join(t.TempDir(), "config")
	runtimeDir := filepath.Join(t.TempDir(), "run")

	t.Setenv(EnvTestMode, "1")
	t.Setenv(EnvTestHome, home)
	t.Setenv(EnvDataDir, data)
	t.Setenv(EnvConfigDir, config)
	t.Setenv(EnvRuntimeDir, runtimeDir)

	p, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestEnvironmentOverridesWin(t *testing.T) {
	p := testPlatform(t)

	if got, want := p.DataDir(), envDir(EnvDataDir); got != want {
		t.Errorf("DataDir = %q, want %q", got, want)
	}
	if got, want := p.ConfigDir(), envDir(EnvConfigDir); got != want {
		t.Errorf("ConfigDir = %q, want %q", got, want)
	}
	if got, want := p.RuntimeDir(), envDir(EnvRuntimeDir); got != want {
		t.Errorf("RuntimeDir = %q, want %q", got, want)
	}
	// HomeDir is symlink-resolved so it can be compared with canonical target
	// paths (I-14).
	wantHome, err := filepath.EvalSymlinks(envDir(EnvTestHome))
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if got := p.HomeDir(); got != filepath.Clean(wantHome) {
		t.Errorf("HomeDir = %q, want %q", got, wantHome)
	}
	if p.OS() != runtime.GOOS {
		t.Errorf("OS = %q, want %q", p.OS(), runtime.GOOS)
	}
}

func TestTestHomeIsIgnoredWithoutTestMode(t *testing.T) {
	t.Setenv(EnvTestMode, "")
	t.Setenv(EnvTestHome, filepath.Join(t.TempDir(), "fake-home"))

	home, err := homeDir()
	if err != nil {
		t.Fatalf("homeDir: %v", err)
	}
	real, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no user home available: %v", err)
	}
	if home != filepath.Clean(real) {
		t.Errorf("INTENTER_TEST_HOME must be ignored without INTENTER_TEST_MODE=1 (got %q)", home)
	}
}

func TestDefaultDirsWithoutOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv(EnvTestMode, "1")
	t.Setenv(EnvTestHome, home)
	t.Setenv(EnvDataDir, "")
	t.Setenv(EnvConfigDir, "")
	t.Setenv(EnvRuntimeDir, "")
	t.Setenv(EnvEndpoint, "")

	p, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for name, dir := range map[string]string{
		"DataDir":    p.DataDir(),
		"ConfigDir":  p.ConfigDir(),
		"RuntimeDir": p.RuntimeDir(),
	} {
		if dir == "" || !filepath.IsAbs(dir) {
			t.Errorf("%s = %q, want an absolute path", name, dir)
		}
	}
	if p.IPCEndpoint() == "" {
		t.Error("IPCEndpoint must not be empty")
	}
	if runtime.GOOS == "windows" {
		if !strings.HasPrefix(p.IPCEndpoint(), `\\.\pipe\intenter-`) {
			t.Errorf("endpoint = %q, want a per-user named pipe", p.IPCEndpoint())
		}
	} else if filepath.Base(p.IPCEndpoint()) != "intenter.sock" {
		t.Errorf("endpoint = %q, want a unix socket in the runtime dir", p.IPCEndpoint())
	}
}

func TestEndpointOverride(t *testing.T) {
	t.Setenv(EnvTestMode, "1")
	t.Setenv(EnvTestHome, t.TempDir())
	t.Setenv(EnvEndpoint, "/custom/endpoint.sock")

	p, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.IPCEndpoint() != "/custom/endpoint.sock" {
		t.Errorf("endpoint = %q, want the INTENTER_ENDPOINT override", p.IPCEndpoint())
	}
}

func TestEnsureDirsCreatesOwnerOnlyDirectories(t *testing.T) {
	p := testPlatform(t)
	if err := EnsureDirs(p); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	for _, dir := range []string{p.DataDir(), p.ConfigDir(), p.RuntimeDir(), LogDir(p)} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", dir)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != DirMode {
			t.Errorf("%s mode = %v, want %v", dir, info.Mode().Perm(), DirMode)
		}
	}
}

func TestDerivedPaths(t *testing.T) {
	p := testPlatform(t)
	if filepath.Base(DatabasePath(p)) != "intenter.db" {
		t.Errorf("DatabasePath = %q", DatabasePath(p))
	}
	if filepath.Dir(DatabasePath(p)) != p.DataDir() {
		t.Errorf("database must live in the data dir, got %q", DatabasePath(p))
	}
	if filepath.Base(DaemonInfoPath(p)) != "daemon.json" {
		t.Errorf("DaemonInfoPath = %q", DaemonInfoPath(p))
	}
	if filepath.Dir(PidFilePath(p)) != p.RuntimeDir() {
		t.Errorf("pid file must live in the runtime dir, got %q", PidFilePath(p))
	}
	if filepath.Base(ConfigFilePath(p)) != "config.toml" {
		t.Errorf("ConfigFilePath = %q", ConfigFilePath(p))
	}
}

func TestDefaultShellDialect(t *testing.T) {
	p := testPlatform(t)
	want := action.DialectPosix
	if runtime.GOOS == "windows" {
		want = action.DialectCmd
	}
	if got := p.DefaultShellDialect(); got != want {
		t.Errorf("DefaultShellDialect = %q, want %q", got, want)
	}
}

func TestFindExecutableResolvesAndFails(t *testing.T) {
	name := "go"
	if runtime.GOOS == "windows" {
		name = "cmd"
	}
	resolved, err := FindExecutable(name)
	if err != nil {
		t.Skipf("%s not on PATH: %v", name, err)
	}
	if !filepath.IsAbs(resolved) {
		t.Errorf("FindExecutable(%s) = %q, want an absolute path", name, resolved)
	}
	if _, err := FindExecutable("intenter-definitely-not-installed"); err == nil {
		t.Error("expected an error for a missing executable")
	}
	if _, err := FindExecutable(""); err == nil {
		t.Error("expected an error for an empty name")
	}
}

func TestFindExecutableInPrefersCandidates(t *testing.T) {
	dir := t.TempDir()
	candidate := filepath.Join(dir, "claude")
	if runtime.GOOS == "windows" {
		candidate += ".exe"
	}
	if err := os.WriteFile(candidate, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write candidate: %v", err)
	}

	got, err := FindExecutableIn("claude", []string{filepath.Join(dir, "missing"), candidate})
	if err != nil {
		t.Fatalf("FindExecutableIn: %v", err)
	}
	if got != candidate {
		t.Errorf("FindExecutableIn = %q, want %q", got, candidate)
	}
}

func TestSelfExecutablePathIsAbsolute(t *testing.T) {
	got, err := SelfExecutablePath()
	if err != nil {
		t.Fatalf("SelfExecutablePath: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("SelfExecutablePath = %q, want an absolute path", got)
	}
}

func TestPathRulesSystemAndTempClassification(t *testing.T) {
	p := testPlatform(t)
	rules := p.PathRules()

	if len(rules.SystemRoots) == 0 {
		t.Fatal("SystemRoots must not be empty")
	}
	system := rules.SystemRoots[len(rules.SystemRoots)-1]
	if !rules.IsSystem(system) {
		t.Errorf("%q must be classified as a system root", system)
	}

	if len(rules.TempRoots) > 0 {
		temp := rules.TempRoots[0]
		if !rules.IsTemp(temp) {
			t.Errorf("%q must be classified as temp", temp)
		}
		if !rules.IsTempRoot(temp) {
			t.Errorf("%q must be recognized as a temp root itself", temp)
		}
		if rules.IsSystem(temp) {
			t.Errorf("%q is a temp carve-out and must not be SYSTEM (§16.3)", temp)
		}
		child := filepath.Join(temp, "build-123")
		if !rules.IsTemp(child) || rules.IsTempRoot(child) {
			t.Errorf("%q must be temp but not the temp root", child)
		}
	}
}

func TestPathRulesSensitiveAndToolCache(t *testing.T) {
	p := testPlatform(t)
	rules := p.PathRules()
	home := p.HomeDir()

	sensitive := []string{
		filepath.Join(home, ".ssh", "id_rsa"),
		filepath.Join(home, ".aws", "credentials"),
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".gitconfig"),
		filepath.Join("/somewhere", "project", ".env"),
		filepath.Join("/somewhere", "project", ".env.production"),
		filepath.Join("/somewhere", "certs", "server.pem"),
		filepath.Join("/somewhere", "keys", "service-account-prod.json"),
	}
	for _, path := range sensitive {
		if !rules.IsSensitive(path) {
			t.Errorf("%q must be sensitive (§16.6)", path)
		}
	}

	// Shell and login startup files: a write runs code on the next shell or
	// login, so they are as protected as credential files (§16.6).
	persistence := []string{
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".bash_profile"),
		filepath.Join(home, ".profile"),
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".zshenv"),
		filepath.Join(home, ".zprofile"),
		filepath.Join(home, ".config", "fish", "config.fish"),
		filepath.Join(home, ".config", "fish", "conf.d", "x.fish"),
		filepath.Join(home, ".config", "autostart", "evil.desktop"),
		filepath.Join(home, ".config", "systemd", "user", "evil.service"),
	}
	for _, path := range persistence {
		if !rules.IsSensitive(path) {
			t.Errorf("%q is a persistence file and must be sensitive (§16.6)", path)
		}
	}

	notSensitive := []string{
		filepath.Join(home, "projects", "app", "README.md"),
		filepath.Join(home, "projects", "app", "environment.ts"),
		// A project's own bashrc-like file outside HOME is not a login file.
		filepath.Join("/somewhere", "project", ".bashrc"),
	}
	for _, path := range notSensitive {
		if rules.IsSensitive(path) {
			t.Errorf("%q must not be sensitive", path)
		}
	}

	caches := []string{
		filepath.Join(home, ".gradle", "caches", "modules-2"),
		filepath.Join(home, ".m2", "repository"),
		filepath.Join(home, ".npm", "_cacache"),
	}
	for _, path := range caches {
		if !rules.IsToolCache(path) {
			t.Errorf("%q must be a tool cache (§16.6)", path)
		}
	}
	if rules.IsToolCache(filepath.Join(home, "projects", "app")) {
		t.Error("a project directory must not be a tool cache")
	}
}

func TestPathRulesExtraSensitivePatterns(t *testing.T) {
	p := testPlatform(t)
	extra := filepath.Join(p.DataDir())
	rules := p.PathRules().WithSensitive(RecursivePattern(extra))

	if !rules.IsSensitive(filepath.Join(extra, "intenter.db")) {
		t.Error("self-protection paths must be sensitive (§16.6)")
	}
	if p.PathRules().IsSensitive(filepath.Join(extra, "intenter.db")) {
		t.Error("WithSensitive must not mutate the original rules")
	}
}

func TestPathRulesStandardHomeDirs(t *testing.T) {
	p := testPlatform(t)
	rules := p.PathRules()
	home := p.HomeDir()

	for _, name := range []string{"Documents", "Downloads", ".ssh", ".config"} {
		if !rules.IsStandardHomeDir(filepath.Join(home, name), home) {
			t.Errorf("~/%s must be a standard home directory (§16.5)", name)
		}
	}
	if rules.IsStandardHomeDir(filepath.Join(home, "projects"), home) {
		t.Error("~/projects must not be a standard home directory")
	}
	if rules.IsStandardHomeDir(filepath.Join(home, "Documents", "notes"), home) {
		t.Error("only the directory itself is standard, not its children")
	}
}

func TestPathRulesUnderRespectsBoundaries(t *testing.T) {
	rules := PathRules{}
	if rules.Under("/a/bc", "/a/b") {
		t.Error("/a/bc must not be under /a/b")
	}
	if !rules.Under("/a/b/c", "/a/b") {
		t.Error("/a/b/c must be under /a/b")
	}
	if !rules.Under("/a/b", "/a/b") {
		t.Error("a path is under itself")
	}
	if rules.StrictlyUnder("/a/b", "/a/b") {
		t.Error("a path is not strictly under itself")
	}
}

func TestPathRulesCaseSensitivity(t *testing.T) {
	sensitive := PathRules{CaseInsensitive: false}
	insensitive := PathRules{CaseInsensitive: true}

	if sensitive.EqualPath("/a/B", "/a/b") {
		t.Error("case-sensitive rules must distinguish case")
	}
	if !insensitive.EqualPath("/a/B", "/a/b") {
		t.Error("case-insensitive rules must fold case")
	}
	if !insensitive.Under("/A/B/c", "/a/b") {
		t.Error("case-insensitive containment must fold case")
	}
}

func TestPathRulesIsRoot(t *testing.T) {
	rules := PathRules{}
	if runtime.GOOS == "windows" {
		if !rules.IsRoot(`C:\`) {
			t.Error(`C:\ must be a drive root`)
		}
		if rules.IsRoot(`C:\Users`) {
			t.Error(`C:\Users must not be a root`)
		}
		return
	}
	if !rules.IsRoot("/") {
		t.Error("/ must be a root")
	}
	if rules.IsRoot("/usr") {
		t.Error("/usr must not be a root")
	}
}
