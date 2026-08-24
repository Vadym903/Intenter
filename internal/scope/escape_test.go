package scope

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
)

// This file is the hardening pass for §16.1 and INVARIANT I-14: a path must be
// classified by where it actually lands, never by how it is spelled. Every case
// here is a way to make a path look like it stays inside the workspace when it
// does not.

// classifyIn normalizes one word written in the fixture's workspace.
func (f *fixture) classifyIn(raw string) action.Target {
	return f.ctx.Normalize(Input{Raw: raw, Text: raw, Cwd: f.workspace})
}

func TestTraversalOutOfTheWorkspaceIsFlagged(t *testing.T) {
	f := newFixture(t)

	tests := []struct {
		name  string
		raw   string
		scope action.Scope
	}{
		{"up into the home directory", "../../Documents", action.ScopeHome},
		{"through a subdirectory", "./build/../../../Documents", action.ScopeHome},
		{"out of the home tree", "../../../outside", action.ScopeOutsideWorkspace},
		{"deep traversal", "src/../../../../etc", action.ScopeOutsideWorkspace},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := f.classifyIn(tt.raw)
			if !target.HasFlag(action.FlagTraversal) {
				t.Errorf("flags = %v, want traversal for %q", target.Flags, tt.raw)
			}
			if target.Scope != tt.scope {
				t.Errorf("scope = %s, want %s", target.Scope, tt.scope)
			}
		})
	}
}

func TestPathsThatStayInsideAreNotFlagged(t *testing.T) {
	// The control: `..` that resolves back inside the workspace is ordinary.
	f := newFixture(t)

	// `notes` is an ordinary directory; `build` beside a package.json would be
	// generated output, which is a different scope but still inside.
	for _, raw := range []string{"./src", "src/../notes", "./build/../src", "."} {
		target := f.classifyIn(raw)
		if target.HasFlag(action.FlagTraversal) {
			t.Errorf("%q resolves inside the workspace, flags = %v", raw, target.Flags)
		}
		if target.Scope != action.ScopeWorkspace {
			t.Errorf("%q: scope = %s, want WORKSPACE", raw, target.Scope)
		}
	}

	generated := f.classifyIn("src/../build")
	if generated.HasFlag(action.FlagTraversal) {
		t.Errorf("build resolves inside the workspace, flags = %v", generated.Flags)
	}
	if generated.Scope != action.ScopeWorkspaceGenerated {
		t.Errorf("scope = %s, want WORKSPACE_GENERATED", generated.Scope)
	}
}

func TestSymlinkEscapeIsClassifiedByWhereItLands(t *testing.T) {
	// INVARIANT I-14: a link inside the workspace pointing at the home
	// directory is a HOME target, whatever its path looks like.
	f := newFixture(t)

	link := filepath.Join(f.workspace, "build", "link")
	if err := os.Symlink(filepath.Join(f.home, "Documents"), link); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	tests := []struct {
		name string
		raw  string
	}{
		{"the link itself", "./build/link"},
		{"with a trailing slash", "./build/link/"},
		{"a path through the link", "./build/link/notes.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := f.classifyIn(tt.raw)
			if target.Scope != action.ScopeHome {
				t.Errorf("scope = %s, want HOME — the link leaves the workspace", target.Scope)
			}
			if !target.HasFlag(action.FlagSymlinkEscape) {
				t.Errorf("flags = %v, want symlink_escape", target.Flags)
			}
			if target.Scope == action.ScopeWorkspaceGenerated {
				t.Error("an escaping link must never be treated as build output")
			}
		})
	}
}

func TestSymlinkStayingInsideIsNotAnEscape(t *testing.T) {
	f := newFixture(t)

	link := filepath.Join(f.workspace, "build", "inner")
	if err := os.Symlink(filepath.Join(f.workspace, "src"), link); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	target := f.classifyIn("./build/inner")
	if target.HasFlag(action.FlagSymlinkEscape) {
		t.Errorf("a link that stays inside is not an escape, flags = %v", target.Flags)
	}
	if target.Scope != action.ScopeWorkspace {
		t.Errorf("scope = %s, want WORKSPACE", target.Scope)
	}
}

func TestGeneratedRootThatEscapesIsNotGenerated(t *testing.T) {
	// §16.4: a generated root is computed after canonicalization, so a
	// directory named `build` that is really a link out of the workspace is
	// not build output — otherwise deleting it would look like housekeeping.
	f := newFixture(t)

	// `dist` is a generated root in this fixture (package.json is beside it);
	// replace it with a link to the home directory.
	dist := filepath.Join(f.workspace, "dist")
	if err := os.Remove(dist); err != nil {
		t.Fatalf("remove dist: %v", err)
	}
	if err := os.Symlink(filepath.Join(f.home, "Documents"), dist); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	target := f.classifyIn("./dist")
	if target.Scope == action.ScopeWorkspaceGenerated {
		t.Fatal("a generated root that escapes the workspace is not generated output (I-14)")
	}
	if target.Scope != action.ScopeHome {
		t.Errorf("scope = %s, want HOME", target.Scope)
	}
	if !target.HasFlag(action.FlagSymlinkEscape) {
		t.Errorf("flags = %v, want symlink_escape", target.Flags)
	}
}

func TestWildcardMatchesThatEscapeAreReported(t *testing.T) {
	// §16.1 step 7 classifies a glob by its literal prefix, which alone would
	// miss `rm -rf build/*` where one entry links out of the workspace.
	f := newFixture(t)

	link := filepath.Join(f.workspace, "build", "link")
	if err := os.Symlink(filepath.Join(f.home, "Documents"), link); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	targets := f.ctx.NormalizeWord(Input{
		Raw: "build/*", Text: "build/*", Cwd: f.workspace, Glob: true,
	})
	if len(targets) < 2 {
		t.Fatalf("targets = %+v, want the prefix plus the escaping match", targets)
	}

	escaping := false
	for _, target := range targets[1:] {
		if target.Scope == action.ScopeHome {
			escaping = true
		}
	}
	if !escaping {
		t.Errorf("an entry linking out of the workspace must be reported: %+v", targets)
	}
}

func TestWindowsJunctionIsFollowed(t *testing.T) {
	// A junction is Windows' own way to point a workspace directory elsewhere;
	// it must be resolved exactly like a symlink.
	if runtime.GOOS != "windows" {
		t.Skip("junctions only exist on Windows")
	}
	f := newFixture(t)

	link := filepath.Join(f.workspace, "build", "junction")
	if err := os.Symlink(filepath.Join(f.home, "Documents"), link); err != nil {
		t.Skipf("creating a link requires privileges here: %v", err)
	}

	target := f.classifyIn(`./build/junction`)
	if target.Scope != action.ScopeHome {
		t.Errorf("scope = %s, want HOME", target.Scope)
	}
}

func TestSensitivePathsAreFlaggedWhereverTheyAre(t *testing.T) {
	f := newFixture(t)

	if err := os.MkdirAll(filepath.Join(f.workspace, "config"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Both ways a path becomes sensitive: by living in a credential directory,
	// and by its own file name wherever it happens to be (§16.6).
	tests := []struct {
		name string
		path string
	}{
		{"ssh directory", filepath.Join(f.home, ".ssh", "id_rsa")},
		{"key by name outside the ssh directory", filepath.Join(f.workspace, "id_ed25519")},
		{"env file in the workspace", filepath.Join(f.workspace, ".env")},
		{"env variant", filepath.Join(f.workspace, ".env.production")},
		{"private key by extension", filepath.Join(f.workspace, "config", "server.pem")},
		{"keystore", filepath.Join(f.workspace, "config", "app.jks")},
		{"service account json", filepath.Join(f.workspace, "config", "service-account.json")},
		{"credentials json", filepath.Join(f.workspace, "config", "credentials.json")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := f.ctx.Normalize(Input{Raw: tt.path, Text: tt.path, Cwd: f.workspace})
			if !target.HasFlag(action.FlagSensitive) {
				t.Errorf("%s must be flagged sensitive (§16.6), flags = %v", tt.path, target.Flags)
			}
		})
	}

	ordinary := f.ctx.Normalize(Input{
		Raw: filepath.Join(f.workspace, "README.md"), Text: filepath.Join(f.workspace, "README.md"), Cwd: f.workspace,
	})
	if ordinary.HasFlag(action.FlagSensitive) {
		t.Error("an ordinary file must not be flagged sensitive")
	}
}

func TestStandardHomeDirectoriesAreBroad(t *testing.T) {
	// §16.5: targeting one of these as a whole is targeting a whole area.
	f := newFixture(t)

	for _, name := range []string{"Documents", "Desktop", "Downloads", ".ssh", ".config"} {
		path := filepath.Join(f.home, name)
		target := f.ctx.Normalize(Input{Raw: path, Text: path, Cwd: f.workspace})
		if !target.HasFlag(action.FlagBroad) {
			t.Errorf("~/%s must be broad, flags = %v", name, target.Flags)
		}
	}

	// A file inside one of them is not itself broad.
	notes := filepath.Join(f.home, "Documents", "notes.txt")
	target := f.ctx.Normalize(Input{Raw: notes, Text: notes, Cwd: f.workspace})
	if target.HasFlag(action.FlagBroad) {
		t.Errorf("a single file is not broad, flags = %v", target.Flags)
	}
}

func TestTheHomeDirectoryAndRootsAreBroad(t *testing.T) {
	f := newFixture(t)

	home := f.ctx.Normalize(Input{Raw: f.home, Text: f.home, Cwd: f.workspace})
	if !home.HasFlag(action.FlagBroad) || home.Scope != action.ScopeHome {
		t.Errorf("the home directory itself = %s %v, want HOME and broad", home.Scope, home.Flags)
	}

	workspace := f.ctx.Normalize(Input{Raw: ".", Text: ".", Cwd: f.workspace})
	if !workspace.HasFlag(action.FlagBroad) {
		t.Errorf("the workspace root is broad, flags = %v", workspace.Flags)
	}
}

func TestUNCPathsAreOutsideAndFlagged(t *testing.T) {
	f := newFixture(t)

	target := f.ctx.Normalize(Input{
		Raw: `\\server\share\data`, Text: `\\server\share\data`,
		Cwd: f.workspace, WindowsStyle: true,
	})
	if target.Scope != action.ScopeOutsideWorkspace {
		t.Errorf("scope = %s, want OUTSIDE_WORKSPACE", target.Scope)
	}
	if !target.HasFlag(action.FlagNetworkPath) {
		t.Errorf("flags = %v, want network_path", target.Flags)
	}
}

func TestTempTargetsAreFlaggedButKeepTheirScope(t *testing.T) {
	// The temp carve-out exists to keep the temp directory out of SYSTEM; it
	// must not stop a path being classified by where it really is.
	f := newFixture(t)

	scratch := filepath.Join(f.temp, "build")
	target := f.ctx.Normalize(Input{Raw: scratch, Text: scratch, Cwd: f.workspace})
	if target.Scope != action.ScopeOutsideWorkspace {
		t.Errorf("scope = %s, want OUTSIDE_WORKSPACE", target.Scope)
	}
	if !target.HasFlag(action.FlagTemp) {
		t.Errorf("flags = %v, want temp", target.Flags)
	}
	if target.HasFlag(action.FlagBroad) {
		t.Errorf("a directory inside the temp tree is not the temp root, flags = %v", target.Flags)
	}

	// The temp root itself is broad: deleting it wholesale is not housekeeping.
	root := f.ctx.Normalize(Input{Raw: f.temp, Text: f.temp, Cwd: f.workspace})
	if !root.HasFlag(action.FlagTemp) || !root.HasFlag(action.FlagBroad) {
		t.Errorf("the temp root = %v, want both temp and broad (R3)", root.Flags)
	}
}
