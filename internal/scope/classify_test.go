package scope

import (
	"path/filepath"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/platform"
)

// hostRules returns the real path rules of the running OS.
func hostRules(t *testing.T) platform.PathRules {
	t.Helper()
	host, err := platform.New()
	if err != nil {
		t.Fatalf("platform.New: %v", err)
	}
	return host.PathRules()
}

func TestScopesAreDisjointAndOrdered(t *testing.T) {
	f := newFixture(t)

	tests := []struct {
		name string
		path string
		want action.Scope
	}{
		{"workspace file", filepath.Join(f.workspace, "src", "main.go"), action.ScopeWorkspace},
		{"workspace root", f.workspace, action.ScopeWorkspace},
		{"generated dir", filepath.Join(f.workspace, "dist"), action.ScopeWorkspaceGenerated},
		{"generated deep", filepath.Join(f.workspace, "node_modules", "left-pad"), action.ScopeWorkspaceGenerated},
		{"home dir", filepath.Join(f.home, "Documents"), action.ScopeHome},
		{"home root", f.home, action.ScopeHome},
		{"outside", filepath.Join(filepath.Dir(f.home), "outside"), action.ScopeOutsideWorkspace},
		{"temp", filepath.Join(f.temp, "build"), action.ScopeOutsideWorkspace},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := action.Target{Canonical: tt.path}
			f.ctx.Classify(&target)
			if target.Scope != tt.want {
				t.Errorf("scope of %s = %s, want %s", tt.path, target.Scope, tt.want)
			}
		})
	}
}

func TestGeneratedBeatsWorkspace(t *testing.T) {
	f := newFixture(t)

	target := action.Target{Canonical: filepath.Join(f.workspace, "dist", "bundle.js")}
	f.ctx.Classify(&target)
	if target.Scope != action.ScopeWorkspaceGenerated {
		t.Errorf("scope = %s, want WORKSPACE_GENERATED to win over WORKSPACE (§16.3 order)", target.Scope)
	}
}

func TestWorkspaceBeatsHomeForNestedWorkspaces(t *testing.T) {
	f := newFixture(t)

	// The fixture workspace lives under HOME; workspace membership must win.
	target := action.Target{Canonical: filepath.Join(f.workspace, "src")}
	f.ctx.Classify(&target)
	if target.Scope != action.ScopeWorkspace {
		t.Errorf("scope = %s, want WORKSPACE for a workspace inside HOME", target.Scope)
	}
}

func TestBroadFlagCoversWholeAreas(t *testing.T) {
	f := newFixture(t)

	broad := []string{
		f.workspace,
		f.home,
		filepath.Join(f.home, "Documents"),
		filepath.Join(f.home, ".ssh"),
		f.temp,
	}
	for _, path := range broad {
		target := action.Target{Canonical: path}
		f.ctx.Classify(&target)
		if !target.HasFlag(action.FlagBroad) {
			t.Errorf("%s must be broad (§16.3, §16.5)", path)
		}
	}

	narrow := []string{
		filepath.Join(f.workspace, "src"),
		filepath.Join(f.home, "Documents", "notes.txt"),
		filepath.Join(f.temp, "build"),
	}
	for _, path := range narrow {
		target := action.Target{Canonical: path}
		f.ctx.Classify(&target)
		if target.HasFlag(action.FlagBroad) {
			t.Errorf("%s must not be broad", path)
		}
	}
}

func TestSensitiveAndToolCacheFlags(t *testing.T) {
	f := newFixture(t)

	sensitive := []string{
		filepath.Join(f.home, ".ssh", "id_rsa"),
		filepath.Join(f.home, ".ssh", "config"),
		filepath.Join(f.workspace, ".env"),
		filepath.Join(f.workspace, ".env.production"),
		filepath.Join(f.workspace, "certs", "server.pem"),
	}
	for _, path := range sensitive {
		target := action.Target{Canonical: path}
		f.ctx.Classify(&target)
		if !target.HasFlag(action.FlagSensitive) {
			t.Errorf("%s must be sensitive (§16.6)", path)
		}
	}

	cache := action.Target{Canonical: filepath.Join(f.home, ".npm", "_cacache", "index")}
	f.ctx.Classify(&cache)
	if !cache.HasFlag(action.FlagToolCache) {
		t.Errorf("tool caches must be flagged: %v", cache.Flags)
	}

	plain := action.Target{Canonical: filepath.Join(f.workspace, "src", "main.go")}
	f.ctx.Classify(&plain)
	if plain.HasFlag(action.FlagSensitive) || plain.HasFlag(action.FlagToolCache) {
		t.Errorf("ordinary source files carry no flags: %v", plain.Flags)
	}
}

func TestSelfProtectionPathsAreSensitive(t *testing.T) {
	f := newFixture(t)

	host, err := platform.New()
	if err != nil {
		t.Fatalf("platform.New: %v", err)
	}
	settings := filepath.Join(f.home, ".claude", "settings.json")

	rules := f.ctx.Rules.WithSensitive(SelfProtectionPaths(host, []string{settings})...)
	ctx := NewContext(rules, f.home, f.workspace, f.temp, nil)

	protected := []string{
		filepath.Join(host.DataDir(), "intenter.db"),
		filepath.Join(host.ConfigDir(), "config.toml"),
		filepath.Join(host.RuntimeDir(), "intenter.sock"),
		settings,
	}
	for _, path := range protected {
		target := action.Target{Canonical: path}
		ctx.Classify(&target)
		if !target.HasFlag(action.FlagSensitive) {
			t.Errorf("%s must be self-protected (§16.6)", path)
		}
	}
}

func TestClassifyWithoutCanonicalPath(t *testing.T) {
	f := newFixture(t)

	target := action.Target{Raw: "$UNKNOWN", Status: action.TargetAmbiguous}
	f.ctx.Classify(&target)
	if target.Scope != action.ScopeOutsideWorkspace {
		t.Errorf("a target with no canonical path must fall back to OUTSIDE_WORKSPACE, got %s", target.Scope)
	}
}
