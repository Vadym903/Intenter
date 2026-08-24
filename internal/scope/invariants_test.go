package scope

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
)

// The invariant index for scope classification: I-14.
// See internal/approval/invariants_test.go for what this index is for.

func TestInvariant_I14_ClassificationUsesCanonicalPaths(t *testing.T) {
	// I-14: scope classification MUST use canonical (symlink-resolved) paths;
	// a textual prefix under the workspace is never sufficient.
	//
	// A path that reads as project-local and lands in the home directory is
	// the shape that turns a routine cleanup into a destroyed home directory,
	// so the check has to follow the link rather than the spelling.
	//
	// See also TestSymlinkEscapeIsClassifiedByWhereItLands,
	// TestGeneratedRootThatEscapesIsNotGenerated.
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on Windows; junctions are covered separately")
	}
	f := newFixture(t)

	// Every one of these is spelled inside the workspace and is not.
	links := map[string]string{
		"escape":                    filepath.Join(f.home, "Documents"),
		filepath.Join("dist", "up"): filepath.Join(f.home, "Documents"),
		"keys":                      filepath.Join(f.home, ".ssh"),
	}
	for name, destination := range links {
		link := filepath.Join(f.workspace, name)
		if err := os.Symlink(destination, link); err != nil {
			t.Fatalf("symlink %s: %v", name, err)
		}
	}

	tests := map[string]struct {
		raw       string
		wantScope action.Scope
		wantFlag  action.TargetFlag
	}{
		"a link out of the workspace": {
			raw: "./escape", wantScope: action.ScopeHome, wantFlag: action.FlagSymlinkEscape,
		},
		"a link out of a generated directory": {
			raw: "./dist/up", wantScope: action.ScopeHome, wantFlag: action.FlagSymlinkEscape,
		},
		"a file under a link out": {
			raw: "./escape/notes.md", wantScope: action.ScopeHome, wantFlag: action.FlagSymlinkEscape,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			target := f.normalize(tc.raw)
			if target.Scope != tc.wantScope {
				t.Errorf("scope = %s, want %s — %q resolves to %q",
					target.Scope, tc.wantScope, tc.raw, target.Canonical)
			}
			if !target.HasFlag(tc.wantFlag) {
				t.Errorf("want the %s flag on a path that leaves the workspace through a link", tc.wantFlag)
			}
		})
	}

	// The sensitive classification follows the link too: a credential reached
	// through a project-local name is still a credential.
	key := f.normalize("./keys/id_rsa")
	if !key.HasFlag(action.FlagSensitive) {
		t.Errorf("%q resolves to %q and must still be sensitive", "./keys/id_rsa", key.Canonical)
	}
}

func TestInvariant_I14_TheReferencePathsAreCanonicalToo(t *testing.T) {
	// The other half of I-14, and the one easier to miss: if HOME or the
	// workspace root is itself reached through a link, comparing a canonical
	// target against a non-canonical reference silently stops matching. On
	// macOS that is the default state of a temp directory, and the failure is
	// invisible — a home directory that is simply never recognized as one.
	base := t.TempDir()
	home := filepath.Join(base, "home")
	workspace := filepath.Join(home, "projects", "demo")
	mustMkdir(t, filepath.Join(home, "Documents"), filepath.Join(workspace, ".git"))

	canonicalHome := mustEval(t, home)
	canonicalWorkspace := mustEval(t, workspace)
	if canonicalHome == home && canonicalWorkspace == workspace {
		t.Skip("this filesystem gives temp directories canonical paths already")
	}

	// Built from the uncanonicalized paths, exactly as a caller might.
	ctx := NewContext(hostRules(t), home, workspace, filepath.Join(base, "tmp"), nil)

	documents := ctx.Normalize(Input{
		Raw: "~/Documents", Text: filepath.Join(home, "Documents"), Cwd: workspace,
	})
	if documents.Scope != action.ScopeHome {
		t.Errorf("scope = %s, want HOME: the reference paths must be canonicalized too "+
			"(canonical home is %q)", documents.Scope, canonicalHome)
	}

	inside := ctx.Normalize(Input{Raw: "./src", Text: "./src", Cwd: workspace})
	if inside.Scope != action.ScopeWorkspace {
		t.Errorf("scope = %s, want WORKSPACE for a path inside the project", inside.Scope)
	}
}

func TestInvariant_I14_TextualPrefixIsNeverEnough(t *testing.T) {
	// The negative form: a path whose text starts with the workspace root but
	// whose canonical form does not is not in the workspace.
	f := newFixture(t)

	// A sibling directory whose name extends the workspace root, e.g.
	// "…/demo-backup" against a workspace of "…/demo". Prefix matching on the
	// string alone would swallow it.
	sibling := f.workspace + "-backup"
	mustMkdir(t, sibling)

	target := f.ctx.Normalize(Input{Raw: sibling, Text: sibling, Cwd: f.workspace})
	if target.Scope == action.ScopeWorkspace || target.Scope == action.ScopeWorkspaceGenerated {
		t.Errorf("%q was classified %s; it only shares a textual prefix with the workspace",
			sibling, target.Scope)
	}
}
