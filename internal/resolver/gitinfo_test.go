package resolver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
)

func writeGit(t *testing.T, gitDir, relPath, content string) {
	t.Helper()
	path := filepath.Join(gitDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

func TestReadGitInfoFromFixtureRepository(t *testing.T) {
	r := newRepo(t)

	writeGit(t, r.gitDir, "HEAD", "ref: refs/heads/feature/login\n")
	writeGit(t, r.gitDir, "config", `
[core]
	repositoryformatversion = 0
[remote "origin"]
	url = git@github.com:acme/demo.git
	fetch = +refs/heads/*:refs/remotes/origin/*
[remote "upstream"]
	url = https://gitlab.example.com/acme/demo.git
[branch "main"]
	remote = origin
`)
	writeGit(t, r.gitDir, "refs/remotes/origin/HEAD", "ref: refs/remotes/origin/main\n")

	info := ReadGitInfo(r.root, r.home)
	if info == nil {
		t.Fatal("ReadGitInfo returned nil for a valid repository")
	}
	if info.CurrentBranch != "feature/login" {
		t.Errorf("current branch = %q", info.CurrentBranch)
	}
	if info.DefaultBranch != "main" {
		t.Errorf("default branch = %q, want main", info.DefaultBranch)
	}
	if info.Remotes["origin"] != "github.com" {
		t.Errorf("origin host = %q, want github.com", info.Remotes["origin"])
	}
	if info.Remotes["upstream"] != "gitlab.example.com" {
		t.Errorf("upstream host = %q", info.Remotes["upstream"])
	}
	if info.RemoteURLs["origin"] != "git@github.com:acme/demo.git" {
		t.Errorf("origin url = %q", info.RemoteURLs["origin"])
	}
	if info.HooksDir != filepath.Join(r.gitDir, "hooks") {
		t.Errorf("hooks dir = %q", info.HooksDir)
	}
}

func TestDetachedHeadHasNoBranch(t *testing.T) {
	r := newRepo(t)
	writeGit(t, r.gitDir, "HEAD", "9fceb02f2d1c2b0b0b2b5d7b0a3f6e5d4c3b2a19\n")

	info := ReadGitInfo(r.root, r.home)
	if info.CurrentBranch != "" {
		t.Errorf("a detached HEAD must yield no branch, got %q", info.CurrentBranch)
	}
}

func TestGitDirFileForWorktrees(t *testing.T) {
	r := newRepo(t)
	real := filepath.Join(t.TempDir(), "actual-git")
	if err := os.MkdirAll(filepath.Join(real, "hooks"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(real, "HEAD"), []byte("ref: refs/heads/wt\n"), 0o644); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}

	if err := os.RemoveAll(r.gitDir); err != nil {
		t.Fatalf("remove .git: %v", err)
	}
	if err := os.WriteFile(r.gitDir, []byte("gitdir: "+real+"\n"), 0o644); err != nil {
		t.Fatalf("write .git file: %v", err)
	}

	info := ReadGitInfo(r.root, r.home)
	if info == nil {
		t.Fatal("a .git file must be followed (§15.7)")
	}
	if info.GitDir != filepath.Clean(real) {
		t.Errorf("gitdir = %q, want %q", info.GitDir, real)
	}
	if info.CurrentBranch != "wt" {
		t.Errorf("current branch = %q, want wt", info.CurrentBranch)
	}
}

func TestHooksDetectionIgnoresSamples(t *testing.T) {
	r := newRepo(t)
	writeGit(t, r.gitDir, "hooks/pre-commit.sample", "#!/bin/sh\n")

	info := ReadGitInfo(r.root, r.home)
	if len(info.HooksPresent) != 0 {
		t.Errorf("sample hooks must be ignored, got %v", info.HooksPresent)
	}
	if info.HasHook("pre-commit") {
		t.Error("a .sample file is not an installed hook")
	}

	writeGit(t, r.gitDir, "hooks/pre-commit", "#!/bin/sh\nnpm test\n")
	info = ReadGitInfo(r.root, r.home)
	if !info.HasHook("pre-commit") {
		t.Errorf("an installed hook must be detected, got %v", info.HooksPresent)
	}
}

func TestHooksPathFromRepositoryConfig(t *testing.T) {
	r := newRepo(t)
	custom := filepath.Join(r.root, ".githooks")
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(custom, "pre-push"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	writeGit(t, r.gitDir, "config", "[core]\n\thooksPath = "+custom+"\n")

	info := ReadGitInfo(r.root, r.home)
	if info.HooksDir != custom {
		t.Errorf("hooks dir = %q, want the configured %q", info.HooksDir, custom)
	}
	if !info.HasHook("pre-push") {
		t.Errorf("hooks in core.hooksPath must be detected, got %v", info.HooksPresent)
	}
}

func TestHooksPathFromGlobalConfig(t *testing.T) {
	r := newRepo(t)
	custom := filepath.Join(r.home, "global-hooks")
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(custom, "pre-commit"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	r.writeHome(t, ".gitconfig", "[core]\n\thooksPath = "+custom+"\n")

	info := ReadGitInfo(r.root, r.home)
	if info.HooksDir != custom {
		t.Errorf("hooks dir = %q, want the global %q", info.HooksDir, custom)
	}

	// The XDG location is honored too.
	if err := os.Remove(filepath.Join(r.home, ".gitconfig")); err != nil {
		t.Fatalf("remove .gitconfig: %v", err)
	}
	r.writeHome(t, ".config/git/config", "[core]\n\thooksPath = "+custom+"\n")
	info = ReadGitInfo(r.root, r.home)
	if info.HooksDir != custom {
		t.Errorf("hooks dir = %q, want the XDG global config value", info.HooksDir)
	}
}

func TestRelativeHooksPathResolvesAgainstTheWorkingTree(t *testing.T) {
	// git runs hooks from the top level of the working tree, so `.githooks`
	// means <root>/.githooks and not <gitdir>/.githooks. Getting this wrong
	// reports a repository that runs hooks as hook-free (§15.7).
	r := newRepo(t)
	custom := filepath.Join(r.root, ".githooks")
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(custom, "pre-commit"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	writeGit(t, r.gitDir, "config", "[core]\n\thooksPath = .githooks\n")

	info := ReadGitInfo(r.root, r.home)
	if info.HooksDir != custom {
		t.Errorf("hooks dir = %q, want %q", info.HooksDir, custom)
	}
	if !info.HasHook("pre-commit") {
		t.Errorf("a hook under a relative core.hooksPath must be detected, got %v", info.HooksPresent)
	}
}

func TestRemoteHostParsing(t *testing.T) {
	tests := map[string]string{
		"git@github.com:acme/demo.git":           "github.com",
		"ssh://git@gitlab.example.com/acme/demo": "gitlab.example.com",
		"https://github.com/acme/demo.git":       "github.com",
		"https://user:token@github.com/a/b.git":  "github.com",
		"git://git.kernel.org/pub/scm/x.git":     "git.kernel.org",
		"ssh://git@github.com:2222/acme/demo":    "github.com",
		"":                                       "",
	}
	for url, want := range tests {
		if got := RemoteHost(url); got != want {
			t.Errorf("RemoteHost(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestHooksForOperation(t *testing.T) {
	if hooks := HooksForOperation(action.OpGitCommit); len(hooks) != 4 {
		t.Errorf("commit hooks = %v", hooks)
	}
	if hooks := HooksForOperation(action.OpGitPush); len(hooks) != 1 || hooks[0] != "pre-push" {
		t.Errorf("push hooks = %v", hooks)
	}
	if hooks := HooksForOperation(action.OpGitStatus); len(hooks) != 0 {
		t.Errorf("read-only operations have no hooks, got %v", hooks)
	}
}

func TestReadGitInfoWithoutRepository(t *testing.T) {
	dir := t.TempDir()
	if info := ReadGitInfo(dir, dir); info != nil {
		t.Errorf("a directory without .git must yield no git info, got %+v", info)
	}
}
