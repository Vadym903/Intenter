package resolver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/platform"
)

// repo is a temporary workspace with a fixture .git directory. No git binary is
// ever executed (§15.4).
type repo struct {
	home     string
	root     string
	gitDir   string
	builder  *ContextBuilder
	platform platform.Platform
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	root := filepath.Join(home, "projects", "demo")

	if err := os.MkdirAll(filepath.Join(root, ".git", "hooks"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	t.Setenv(platform.EnvTestMode, "1")
	t.Setenv(platform.EnvTestHome, home)
	t.Setenv(platform.EnvDataDir, filepath.Join(base, "data"))
	t.Setenv(platform.EnvConfigDir, filepath.Join(base, "config"))
	t.Setenv(platform.EnvRuntimeDir, filepath.Join(base, "run"))

	p, err := platform.New()
	if err != nil {
		t.Fatalf("platform.New: %v", err)
	}

	resolvedHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	return &repo{
		home:     resolvedHome,
		root:     resolvedRoot,
		gitDir:   filepath.Join(resolvedRoot, ".git"),
		platform: p,
		builder:  NewContextBuilder(p, config.Default()),
	}
}

func (r *repo) write(t *testing.T, relPath, content string) string {
	t.Helper()
	path := filepath.Join(r.root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
	return path
}

func (r *repo) writeHome(t *testing.T, relPath, content string) string {
	t.Helper()
	path := filepath.Join(r.home, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
	return path
}

func TestWorkspaceIsTheNearestGitRoot(t *testing.T) {
	r := newRepo(t)
	nested := filepath.Join(r.root, "packages", "api")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	ctx := r.builder.Build(nested, "")
	if ctx.Action.Status != action.ContextOK {
		t.Fatalf("status = %s (%s)", ctx.Action.Status, ctx.Action.StatusReason)
	}
	if ctx.Action.WorkspaceRoot != r.root {
		t.Errorf("workspace = %q, want %q", ctx.Action.WorkspaceRoot, r.root)
	}
	if ctx.Action.ProjectID != action.ProjectID(r.root) {
		t.Errorf("project id must be sha256 of the canonical root")
	}
	if ctx.Action.HomeDir != r.home {
		t.Errorf("home = %q, want %q", ctx.Action.HomeDir, r.home)
	}
}

func TestWorkspaceFallsBackToProjectHintThenCwd(t *testing.T) {
	r := newRepo(t)
	plain := filepath.Join(r.home, "plain", "sub")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	hint := filepath.Join(r.home, "plain")

	withHint := r.builder.Build(plain, hint)
	if withHint.Action.WorkspaceRoot != canonicalize(hint) {
		t.Errorf("workspace = %q, want the project hint %q", withHint.Action.WorkspaceRoot, hint)
	}

	withoutHint := r.builder.Build(plain, "")
	if withoutHint.Action.WorkspaceRoot != canonicalize(plain) {
		t.Errorf("workspace = %q, want the cwd %q", withoutHint.Action.WorkspaceRoot, plain)
	}

	// A hint that does not contain the cwd is ignored.
	unrelated := r.builder.Build(plain, filepath.Join(r.home, "elsewhere"))
	if unrelated.Action.WorkspaceRoot != canonicalize(plain) {
		t.Errorf("an unrelated hint must be ignored, got %q", unrelated.Action.WorkspaceRoot)
	}
}

func TestWorkspaceValidationRejectsHomeAndRoots(t *testing.T) {
	r := newRepo(t)

	home := r.builder.Build(r.home, "")
	if home.Action.Status != action.ContextWorkspaceUndefined {
		t.Errorf("HOME as workspace = %s, want WORKSPACE_UNDEFINED (§16.2)", home.Action.Status)
	}
	if home.Action.StatusReason == "" {
		t.Error("the reason must explain why the workspace is undefined")
	}
	if home.OK() {
		t.Error("an undefined workspace context is not OK")
	}

	root := r.builder.Build(string(filepath.Separator), "")
	if root.Action.Status != action.ContextWorkspaceUndefined {
		t.Errorf("filesystem root as workspace = %s, want WORKSPACE_UNDEFINED", root.Action.Status)
	}

	parent := r.builder.Build(filepath.Dir(r.home), "")
	if parent.Action.Status != action.ContextWorkspaceUndefined {
		t.Errorf("an ancestor of HOME = %s, want WORKSPACE_UNDEFINED", parent.Action.Status)
	}
}

func TestContextReportsGeneratedRootsAndPackageManager(t *testing.T) {
	r := newRepo(t)
	r.write(t, "package.json", `{"name":"demo","packageManager":"pnpm@9.0.0"}`)
	if err := os.MkdirAll(filepath.Join(r.root, "dist"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	ctx := r.builder.Build(r.root, "")
	if len(ctx.Action.GeneratedRoots) != 1 || filepath.Base(ctx.Action.GeneratedRoots[0]) != "dist" {
		t.Errorf("generated roots = %v, want [dist]", ctx.Action.GeneratedRoots)
	}
	if ctx.Action.PackageManager.Kind != action.PMPnpm {
		t.Errorf("package manager = %s, want pnpm", ctx.Action.PackageManager.Kind)
	}
}

func TestScopeContextIsCachedPerWorkspace(t *testing.T) {
	r := newRepo(t)

	first := r.builder.Build(r.root, "")
	second := r.builder.Build(filepath.Join(r.root, "src"), "")
	if first.Scope != second.Scope {
		t.Error("the scope classifier must be cached per workspace (§13.2)")
	}
}

func TestSelfProtectionPathsAreSensitiveInContext(t *testing.T) {
	r := newRepo(t)
	ctx := r.builder.Build(r.root, "")

	protected := []string{
		filepath.Join(r.platform.DataDir(), "intenter.db"),
		filepath.Join(r.home, ".claude", "settings.json"),
	}
	for _, path := range protected {
		target := action.Target{Canonical: path}
		ctx.Scope.Classify(&target)
		if !target.HasFlag(action.FlagSensitive) {
			t.Errorf("%s must be sensitive (self-protection, §16.6)", path)
		}
	}
}

func TestWorkspaceClaudeSettingsAreSensitive(t *testing.T) {
	// The workspace settings carry the consent Intenter imports. A shell write
	// to <W>/.claude/settings.local.json could add an allow rule that the next
	// command's import turns into an approval, so the write must be a blocked
	// change (R5), not an ordinary edit.
	r := newRepo(t)
	ctx := r.builder.Build(r.root, "")

	for _, rel := range []string{".claude/settings.json", ".claude/settings.local.json"} {
		target := action.Target{Canonical: filepath.Join(r.root, filepath.FromSlash(rel))}
		ctx.Scope.Classify(&target)
		if !target.HasFlag(action.FlagSensitive) {
			t.Errorf("%s must be sensitive so consent cannot be self-granted (§16.6)", rel)
		}
	}

	// An ordinary workspace file is still not sensitive.
	plain := action.Target{Canonical: filepath.Join(r.root, "src", "main.go")}
	ctx.Scope.Classify(&plain)
	if plain.HasFlag(action.FlagSensitive) {
		t.Errorf("an ordinary source file must not be sensitive: %v", plain.Flags)
	}
}

func TestConfiguredSensitivePathsExtra(t *testing.T) {
	r := newRepo(t)
	cfg := config.Default()
	cfg.Policy.SensitivePathsExtra = []string{platform.RecursivePattern(filepath.Join(r.root, "secrets"))}
	builder := NewContextBuilder(r.platform, cfg)

	ctx := builder.Build(r.root, "")
	target := action.Target{Canonical: filepath.Join(r.root, "secrets", "token.txt")}
	ctx.Scope.Classify(&target)
	if !target.HasFlag(action.FlagSensitive) {
		t.Errorf("policy.sensitive_paths_extra must be honored: %v", target.Flags)
	}
}
