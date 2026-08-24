package resolver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/parser"
	"github.com/Vadym903/Intenter/internal/parser/posix"
)

// gitRegistry is the recognizer set for the git tests.
func gitRegistry() *Registry {
	registry := NewRegistry()
	registry.Register(FilesystemRecognizers()...)
	registry.Register(GitRecognizer())
	return registry
}

// gitRepoFixture is a repository with a remote, a branch and no hooks.
func gitRepoFixture(t *testing.T) *repo {
	t.Helper()
	r := newRepo(t)
	writeGit(t, r.gitDir, "HEAD", "ref: refs/heads/feature/login\n")
	writeGit(t, r.gitDir, "config", `
[remote "origin"]
	url = git@github.com:acme/demo.git
[remote "upstream"]
	url = https://gitlab.example.com/acme/demo.git
`)
	writeGit(t, r.gitDir, "refs/remotes/origin/HEAD", "ref: refs/remotes/origin/main\n")
	return r
}

// git parses and resolves a single git command in the repository root.
func (r *repo) git(t *testing.T, command string) action.ResolvedCommand {
	t.Helper()
	return r.gitIn(t, r.root, command)
}

func (r *repo) gitIn(t *testing.T, cwd, command string) action.ResolvedCommand {
	t.Helper()

	parsed, err := posix.New().Parse(parser.Input{
		Command: command, Cwd: cwd, Home: r.home, TempDir: os.TempDir(),
	})
	if err != nil {
		t.Fatalf("parse %q: %v", command, err)
	}
	if len(parsed.Commands) != 1 {
		t.Fatalf("parse %q produced %d commands (%v)", command, len(parsed.Commands), parsed.UnsupportedSummary())
	}
	ctx := r.builder.Build(cwd, "")
	return gitRegistry().Recognize(Request{
		Command: parsed.Commands[0], Context: ctx, Dialect: action.DialectPosix,
	})
}

func TestGitReadSubcommandsHaveDistinctOperations(t *testing.T) {
	r := gitRepoFixture(t)

	tests := []struct {
		command string
		op      action.SemanticOp
	}{
		{"git status", action.OpGitStatus},
		{"git status --short", action.OpGitStatus},
		{"git status -uno", action.OpGitStatus},
		{"git diff", action.OpGitDiff},
		{"git diff --cached --stat", action.OpGitDiff},
		{"git log", action.OpGitLog},
		{"git log --oneline -n 5", action.OpGitLog},
		{"git log -5", action.OpGitLog},
		{"git log --pretty=format:%h", action.OpGitLog},
		{"git show", action.OpGitShow},
		{"git branch", action.OpGitBranch},
		{"git branch -a", action.OpGitBranch},
		{"git rev-parse HEAD", action.OpGitRevParse},
		{"git rev-parse --show-toplevel", action.OpGitRevParse},
		{"git --no-pager status", action.OpGitStatus},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			out := r.git(t, tt.command)
			if out.Status != action.StatusResolved {
				t.Fatalf("status = %s (%s), want RESOLVED", out.Status, out.StatusReason)
			}
			if out.SemanticOp != tt.op {
				t.Errorf("semantic op = %s, want %s", out.SemanticOp, tt.op)
			}
			for _, effect := range out.Effects {
				if effect.Type != action.EffectRead {
					t.Errorf("effect = %s, want only reads", effect.Type)
				}
			}
		})
	}
}

func TestGitReadSubcommandsTargetTheWorkspace(t *testing.T) {
	r := gitRepoFixture(t)

	out := r.git(t, "git status")
	if len(out.Targets) != 1 {
		t.Fatalf("targets = %+v, want the repository root", out.Targets)
	}
	if out.Targets[0].Scope != action.ScopeWorkspace {
		t.Errorf("scope = %s, want WORKSPACE", out.Targets[0].Scope)
	}
	if out.Targets[0].Display != "." {
		t.Errorf("display = %q, want .", out.Targets[0].Display)
	}
}

func TestGitDiffCanWriteAPatchFile(t *testing.T) {
	r := gitRepoFixture(t)

	out := r.git(t, "git diff --output=./patch.diff")
	if out.Status != action.StatusResolved {
		t.Fatalf("status = %s (%s)", out.Status, out.StatusReason)
	}
	assertEffects(t, out, "READ .", "CREATE ./patch.diff", "WRITE ./patch.diff")
}

func TestGitDiffPathspecsAfterSeparatorAreRead(t *testing.T) {
	r := gitRepoFixture(t)

	out := r.git(t, "git diff -- ./src/main.go")
	assertEffects(t, out, "READ .", "READ ./src/main.go")
}

func TestGitDiffNoIndexReadsItsOperands(t *testing.T) {
	// `git diff --no-index A B` reads two arbitrary files, ignoring the
	// repository boundary, and prints them. Modeling only a read of `.` let
	// `git diff --no-index ~/.ssh/id_rsa README.md` exfiltrate a private key
	// while Intenter auto-allowed it as a workspace read.
	r := gitRepoFixture(t)

	out := r.git(t, "git diff --no-index ~/.ssh/id_rsa ./README.md")
	summary := strings.Join(effectSummary(out), "\n")
	if !strings.Contains(summary, "READ ~/.ssh/id_rsa") {
		t.Errorf("the sensitive operand must be read, not ignored:\n%s", summary)
	}
	// The operands are the only reads; the whole workspace is not.
	for _, effect := range out.Effects {
		if effect.Target != nil && effect.Target.Display == "." {
			t.Errorf("--no-index does not read the repository root:\n%s", summary)
		}
	}
}

func TestGitDiffNoIndexNeedsTwoPaths(t *testing.T) {
	r := gitRepoFixture(t)

	out := r.git(t, "git diff --no-index ./only-one")
	if out.Status.Approvable() {
		t.Errorf("a malformed --no-index must be non-approvable, got %s", out.Status)
	}
}

func TestGitBranchWriteOptionsAreRefused(t *testing.T) {
	r := gitRepoFixture(t)

	for _, command := range []string{
		"git branch -d feature/old",
		"git branch -D feature/old",
		"git branch -m old new",
		"git branch --set-upstream-to=origin/main",
		"git branch -f main HEAD",
	} {
		out := r.git(t, command)
		if out.Status != action.StatusUnresolved {
			t.Errorf("%q: status = %s, want UNRESOLVED (§15.4)", command, out.Status)
		}
	}
}

func TestGitGlobalOptionsThatChangeBehaviorAreRefused(t *testing.T) {
	r := gitRepoFixture(t)

	for _, command := range []string{
		"git -c core.hooksPath=/tmp/evil status",
		"git --git-dir=/tmp/other status",
		"git --work-tree=/tmp/other status",
		"git --exec-path=/tmp status",
	} {
		out := r.git(t, command)
		if out.Status != action.StatusUnresolved {
			t.Errorf("%q: status = %s, want UNRESOLVED (§15.4)", command, out.Status)
		}
	}

	if out := r.git(t, "git"); out.Status != action.StatusUnresolved {
		t.Errorf("a bare git call must be UNRESOLVED, got %s", out.Status)
	}
	if out := r.git(t, "git bisect start"); out.Status != action.StatusUnresolved {
		t.Errorf("an unmodeled subcommand must be UNRESOLVED, got %s", out.Status)
	}
}

func TestGitDashCMovesTheRepository(t *testing.T) {
	r := gitRepoFixture(t)
	nested := filepath.Join(r.root, "packages", "api")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// -C into a subdirectory still resolves to the same repository.
	out := r.git(t, "git -C ./packages/api status")
	if out.Status != action.StatusResolved {
		t.Fatalf("status = %s (%s)", out.Status, out.StatusReason)
	}
	if out.Targets[0].Display != "." {
		t.Errorf("display = %q, want the repository root", out.Targets[0].Display)
	}
}

func TestGitOutsideTheWorkspaceKeepsItsRealScope(t *testing.T) {
	// §16.2: a git command whose effective cwd lies outside W has its targets
	// classified by their actual location, so W-scoped trust does not apply.
	r := gitRepoFixture(t)

	out := r.gitIn(t, r.home, "git status")
	if len(out.Targets) != 1 {
		t.Fatalf("targets = %+v, want one", out.Targets)
	}
	if out.Targets[0].Scope != action.ScopeHome {
		t.Errorf("scope = %s, want HOME", out.Targets[0].Scope)
	}
}

func TestGitAddReadsPathspecsAndWritesTheGitDir(t *testing.T) {
	r := gitRepoFixture(t)

	out := r.git(t, "git add ./src/main.go")
	if out.SemanticOp != action.OpGitAdd {
		t.Errorf("semantic op = %s, want GIT_ADD", out.SemanticOp)
	}
	assertEffects(t, out, "READ ./src/main.go", "WRITE ./.git")

	all := r.git(t, "git add -A")
	assertEffects(t, all, "READ .", "WRITE ./.git")
}

func TestGitCommitWritesTheGitDir(t *testing.T) {
	r := gitRepoFixture(t)

	out := r.git(t, `git commit -m "fix things"`)
	if out.Status != action.StatusResolved {
		t.Fatalf("status = %s (%s)", out.Status, out.StatusReason)
	}
	if out.SemanticOp != action.OpGitCommit {
		t.Errorf("semantic op = %s, want GIT_COMMIT", out.SemanticOp)
	}
	assertEffects(t, out, "WRITE ./.git")

	staged := r.git(t, `git commit -am "fix things"`)
	assertEffects(t, staged, "READ .", "WRITE ./.git")
}

func TestGitCommitWithAHookIsUnresolved(t *testing.T) {
	r := gitRepoFixture(t)
	writeGit(t, r.gitDir, "hooks/pre-commit", "#!/bin/sh\necho hi\n")

	out := r.git(t, `git commit -m "fix"`)
	if out.Status != action.StatusUnresolved {
		t.Fatalf("status = %s, want UNRESOLVED (§15.7)", out.Status)
	}
	if !strings.Contains(out.StatusReason, "pre-commit") {
		t.Errorf("the reason must name the hook, got %q", out.StatusReason)
	}
}

func TestGitCommitNoVerifySkipsOnlyTheHooksGitSkips(t *testing.T) {
	r := gitRepoFixture(t)
	writeGit(t, r.gitDir, "hooks/pre-commit", "#!/bin/sh\n")

	skipped := r.git(t, `git commit --no-verify -m "fix"`)
	if skipped.Status != action.StatusResolved {
		t.Errorf("--no-verify skips pre-commit, got %s (%s)", skipped.Status, skipped.StatusReason)
	}

	// post-commit still runs even with --no-verify.
	writeGit(t, r.gitDir, "hooks/post-commit", "#!/bin/sh\n")
	still := r.git(t, `git commit --no-verify -m "fix"`)
	if still.Status != action.StatusUnresolved {
		t.Errorf("post-commit is not skipped by --no-verify, got %s", still.Status)
	}
}

func TestGitSampleHooksAreIgnored(t *testing.T) {
	r := gitRepoFixture(t)
	writeGit(t, r.gitDir, "hooks/pre-commit.sample", "#!/bin/sh\n")

	out := r.git(t, `git commit -m "fix"`)
	if out.Status != action.StatusResolved {
		t.Errorf("a *.sample hook does not run, got %s (%s)", out.Status, out.StatusReason)
	}
}

func TestGitCheckoutDiscardsChanges(t *testing.T) {
	r := gitRepoFixture(t)

	tests := []struct {
		command  string
		discards bool
	}{
		{"git checkout main", false},
		{"git checkout -b feature/new", false},
		{"git switch main", false},
		{"git switch -c feature/new", false},
		{"git checkout -f main", true},
		{"git checkout --force main", true},
		{"git checkout -- ./src/main.go", true},
		{"git checkout main -- ./src/main.go", true},
		{"git restore ./src/main.go", true},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			out := r.git(t, tt.command)
			if out.Status != action.StatusResolved {
				t.Fatalf("status = %s (%s)", out.Status, out.StatusReason)
			}
			if out.SemanticOp != action.OpGitCheckout {
				t.Errorf("semantic op = %s, want GIT_CHECKOUT", out.SemanticOp)
			}
			discards := false
			for _, effect := range out.Effects {
				if effect.HasFlag(action.EffectFlagDiscardsChanges) {
					discards = true
				}
			}
			if discards != tt.discards {
				t.Errorf("discards_changes = %v, want %v (effects %v)", discards, tt.discards, effectSummary(out))
			}
		})
	}
}

func TestGitResetHardDiscardsTheWorkingTree(t *testing.T) {
	r := gitRepoFixture(t)

	soft := r.git(t, "git reset --soft HEAD~1")
	assertEffects(t, soft, "WRITE ./.git")

	hard := r.git(t, "git reset --hard")
	if hard.SemanticOp != action.OpGitReset {
		t.Errorf("semantic op = %s, want GIT_RESET", hard.SemanticOp)
	}
	assertEffects(t, hard, "WRITE ./.git", "WRITE(discards_changes) .")
}

func TestGitPushResolvesTheRemoteHost(t *testing.T) {
	r := gitRepoFixture(t)

	tests := []struct {
		name    string
		command string
		host    string
		branch  string
		known   bool
		flags   []action.EffectFlag
	}{
		{"default remote and branch", "git push", "github.com", "feature/login", true, nil},
		{"explicit remote", "git push origin", "github.com", "feature/login", true, nil},
		{"explicit branch", "git push origin main", "github.com", "main", true, nil},
		{"other remote", "git push upstream main", "gitlab.example.com", "main", true, nil},
		{"refspec destination", "git push origin HEAD:main", "github.com", "main", true, nil},
		{"qualified ref", "git push origin HEAD:refs/heads/main", "github.com", "main", true, nil},
		{"force", "git push -f origin main", "github.com", "main", true, []action.EffectFlag{action.EffectFlagForce}},
		{"force with lease", "git push --force-with-lease origin main", "github.com", "main", true, []action.EffectFlag{action.EffectFlagForce}},
		{"plus refspec is force", "git push origin +main", "github.com", "main", true, []action.EffectFlag{action.EffectFlagForce}},
		{"delete option", "git push --delete origin old", "github.com", "old", true, []action.EffectFlag{action.EffectFlagDelete}},
		{"delete refspec", "git push origin :old", "github.com", "old", true, []action.EffectFlag{action.EffectFlagDelete}},
		{"mirror is broad", "git push --mirror origin", "github.com", "feature/login", true, []action.EffectFlag{action.EffectFlagBroad}},
		{"all is broad", "git push --all origin", "github.com", "feature/login", true, []action.EffectFlag{action.EffectFlagBroad}},
		{"tags is broad", "git push --tags origin", "github.com", "feature/login", true, []action.EffectFlag{action.EffectFlagBroad}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := r.git(t, tt.command)
			if out.Status != action.StatusResolved {
				t.Fatalf("status = %s (%s)", out.Status, out.StatusReason)
			}
			if out.SemanticOp != action.OpGitPush {
				t.Errorf("semantic op = %s, want GIT_PUSH", out.SemanticOp)
			}

			var network *action.Effect
			for i := range out.Effects {
				if out.Effects[i].Type == action.EffectNetwork {
					network = &out.Effects[i]
				}
			}
			if network == nil {
				t.Fatalf("effects = %v, want a NETWORK effect", effectSummary(out))
			}
			if network.Network.Host != tt.host {
				t.Errorf("host = %q, want %q", network.Network.Host, tt.host)
			}
			for _, flag := range tt.flags {
				if !network.HasFlag(flag) {
					t.Errorf("flags = %v, want %s", network.Flags, flag)
				}
			}
			if out.Git == nil {
				t.Fatal("a push must record which ref it targets (R7)")
			}
			if out.Git.BranchKnown != tt.known {
				t.Errorf("branch_known = %v, want %v", out.Git.BranchKnown, tt.known)
			}
			if out.Git.Branch != tt.branch {
				t.Errorf("branch = %q, want %q", out.Git.Branch, tt.branch)
			}
		})
	}
}

func TestGitPushToAnUnknownRemoteIsUnresolved(t *testing.T) {
	r := gitRepoFixture(t)

	out := r.git(t, "git push nowhere main")
	if out.Status != action.StatusUnresolved {
		t.Errorf("status = %s, want UNRESOLVED for an unconfigured remote (§15.4)", out.Status)
	}
	if !strings.Contains(out.StatusReason, "nowhere") {
		t.Errorf("the reason must name the remote, got %q", out.StatusReason)
	}
}

func TestGitPushWithoutABranchIsUnknown(t *testing.T) {
	// A detached HEAD has no branch name, which R7 treats like a protected one.
	r := gitRepoFixture(t)
	writeGit(t, r.gitDir, "HEAD", "9fceb02d0ae598e95dc970b74767f19372d61af8\n")

	out := r.git(t, "git push")
	if out.Status != action.StatusResolved {
		t.Fatalf("status = %s (%s)", out.Status, out.StatusReason)
	}
	if out.Git == nil || out.Git.BranchKnown {
		t.Errorf("branch must be unknown on a detached HEAD, got %+v", out.Git)
	}
}

func TestGitPushHookIsDetected(t *testing.T) {
	r := gitRepoFixture(t)
	writeGit(t, r.gitDir, "hooks/pre-push", "#!/bin/sh\n")

	out := r.git(t, "git push origin main")
	if out.Status != action.StatusUnresolved {
		t.Errorf("status = %s, want UNRESOLVED with a pre-push hook (§15.7)", out.Status)
	}

	skipped := r.git(t, "git push --no-verify origin main")
	if skipped.Status != action.StatusResolved {
		t.Errorf("--no-verify skips pre-push, got %s (%s)", skipped.Status, skipped.StatusReason)
	}
}

func TestGitPushBroadInvalidatesAPlainPushEnvelope(t *testing.T) {
	// I-1: the delete and broad flags are part of the envelope, so an approval
	// for a plain push can never cover --mirror.
	r := gitRepoFixture(t)

	plain := action.Envelope(r.git(t, "git push origin main").Effects)
	mirror := action.Envelope(r.git(t, "git push --mirror origin").Effects)

	plainKeys := make([]string, 0, len(plain))
	for _, entry := range plain {
		plainKeys = append(plainKeys, entry.Key())
	}
	for _, entry := range mirror {
		if entry.Type != action.EffectNetwork {
			continue
		}
		if contains(plainKeys, entry.Key()) {
			t.Errorf("--mirror envelope %q must differ from a plain push", entry.Key())
		}
	}
}

func TestGitCheckoutHookIsDetected(t *testing.T) {
	r := gitRepoFixture(t)
	writeGit(t, r.gitDir, "hooks/post-checkout", "#!/bin/sh\n")

	out := r.git(t, "git checkout main")
	if out.Status != action.StatusUnresolved {
		t.Errorf("status = %s, want UNRESOLVED with a post-checkout hook", out.Status)
	}
}

func TestGitHooksPathRedirectionIsHonored(t *testing.T) {
	// core.hooksPath moves the hooks directory; a hook there still counts.
	r := gitRepoFixture(t)
	writeGit(t, r.gitDir, "config", `
[core]
	hooksPath = .githooks
[remote "origin"]
	url = git@github.com:acme/demo.git
`)
	r.write(t, ".githooks/pre-commit", "#!/bin/sh\n")

	out := r.git(t, `git commit -m "fix"`)
	if out.Status != action.StatusUnresolved {
		t.Errorf("status = %s, want UNRESOLVED for a redirected hook (§15.7)", out.Status)
	}
}

func TestGitUnknownSubcommandOptionsAreRefused(t *testing.T) {
	r := gitRepoFixture(t)

	for _, command := range []string{
		"git status --zap",
		"git add --zap ./a.go",
		"git commit --zap",
		"git push --zap origin main",
		"git reset --zap",
	} {
		out := r.git(t, command)
		if out.Status != action.StatusUnresolved {
			t.Errorf("%q: status = %s, want UNRESOLVED (§15.3)", command, out.Status)
		}
	}
}

// The rest of this file is the git safety surface of §15.4 and rules R7/R8:
// the flags that make a push or a checkout destructive have to reach the policy
// engine, because those are the git operations a user cannot undo.

func TestGitForcePushFlagsReachThePolicy(t *testing.T) {
	r := gitRepoFixture(t)

	tests := []struct {
		name    string
		command string
		flags   []action.EffectFlag
		branch  string
		known   bool
	}{
		{"force to the default branch", "git push -f origin main",
			[]action.EffectFlag{action.EffectFlagForce}, "main", true},
		{"force with lease", "git push --force-with-lease origin main",
			[]action.EffectFlag{action.EffectFlagForce}, "main", true},
		{"force with lease and a ref", "git push --force-with-lease=main origin main",
			[]action.EffectFlag{action.EffectFlagForce}, "main", true},
		{"force if includes", "git push --force-if-includes origin main",
			[]action.EffectFlag{action.EffectFlagForce}, "main", true},
		{"plus refspec", "git push origin +main",
			[]action.EffectFlag{action.EffectFlagForce}, "main", true},
		{"delete a branch", "git push --delete origin feature/old",
			[]action.EffectFlag{action.EffectFlagDelete}, "feature/old", true},
		{"delete by refspec", "git push origin :feature/old",
			[]action.EffectFlag{action.EffectFlagDelete}, "feature/old", true},
		{"mirror", "git push --mirror origin",
			[]action.EffectFlag{action.EffectFlagBroad}, "feature/login", true},
		{"all branches", "git push --all origin",
			[]action.EffectFlag{action.EffectFlagBroad}, "feature/login", true},
		{"all tags", "git push --tags origin",
			[]action.EffectFlag{action.EffectFlagBroad}, "feature/login", true},
		{"force and delete together", "git push -f --delete origin old",
			[]action.EffectFlag{action.EffectFlagForce, action.EffectFlagDelete}, "old", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := r.git(t, tt.command)
			if out.Status != action.StatusResolved {
				t.Fatalf("status = %s (%s)", out.Status, out.StatusReason)
			}

			network := networkOf(t, out)
			for _, flag := range tt.flags {
				if !network.HasFlag(flag) {
					t.Errorf("flags = %v, want %s", network.Flags, flag)
				}
			}
			if out.Git == nil {
				t.Fatal("a push must record the ref it targets (R7)")
			}
			if out.Git.Branch != tt.branch || out.Git.BranchKnown != tt.known {
				t.Errorf("git detail = %+v, want branch %q known=%v", out.Git, tt.branch, tt.known)
			}
		})
	}
}

func TestGitPushToAnUnknownBranchIsReportedAsUnknown(t *testing.T) {
	// R7 treats an undeterminable branch like a protected one, so it must be
	// reported honestly rather than guessed.
	r := gitRepoFixture(t)
	writeGit(t, r.gitDir, "HEAD", "9fceb02d0ae598e95dc970b74767f19372d61af8\n")

	out := r.git(t, "git push -f")
	if out.Git == nil || out.Git.BranchKnown {
		t.Errorf("git detail = %+v, want an unknown branch on a detached HEAD", out.Git)
	}
}

func TestGitPushToAnUnconfiguredRemoteIsUnresolved(t *testing.T) {
	r := gitRepoFixture(t)

	for _, command := range []string{
		"git push nowhere main",
		"git push -f nowhere main",
	} {
		out := r.git(t, command)
		if out.Status != action.StatusUnresolved {
			t.Errorf("%q: status = %s, want UNRESOLVED — the host is unknown", command, out.Status)
		}
	}
}

func TestGitDiscardingCommandsAreFlagged(t *testing.T) {
	// R8: everything that can overwrite uncommitted work.
	r := gitRepoFixture(t)

	tests := []struct {
		name     string
		command  string
		op       action.SemanticOp
		discards bool
	}{
		{"hard reset", "git reset --hard", action.OpGitReset, true},
		{"hard reset to a ref", "git reset --hard origin/main", action.OpGitReset, true},
		{"merge reset", "git reset --merge", action.OpGitReset, true},
		{"keep reset", "git reset --keep", action.OpGitReset, true},
		{"soft reset", "git reset --soft HEAD~1", action.OpGitReset, false},
		{"mixed reset", "git reset --mixed", action.OpGitReset, false},
		{"forced checkout", "git checkout -f main", action.OpGitCheckout, true},
		{"checkout paths", "git checkout -- ./src", action.OpGitCheckout, true},
		{"checkout a ref and paths", "git checkout main -- ./src", action.OpGitCheckout, true},
		{"restore", "git restore ./src", action.OpGitCheckout, true},
		{"restore staged", "git restore --staged ./src", action.OpGitCheckout, true},
		{"plain checkout", "git checkout main", action.OpGitCheckout, false},
		{"new branch", "git checkout -b feature/new", action.OpGitCheckout, false},
		{"switch", "git switch main", action.OpGitCheckout, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := r.git(t, tt.command)
			if out.Status != action.StatusResolved {
				t.Fatalf("status = %s (%s)", out.Status, out.StatusReason)
			}
			if out.SemanticOp != tt.op {
				t.Errorf("semantic op = %s, want %s", out.SemanticOp, tt.op)
			}

			discards := false
			for _, effect := range out.Effects {
				if effect.HasFlag(action.EffectFlagDiscardsChanges) {
					discards = true
				}
			}
			if discards != tt.discards {
				t.Errorf("discards_changes = %v, want %v (effects %v)",
					discards, tt.discards, effectSummary(out))
			}
		})
	}
}

func TestGitCommitHooksMakeItUnresolved(t *testing.T) {
	// §15.7: a client-side hook runs project code Intenter has not read, so
	// the commit stops being something it can model.
	hooks := map[string]string{
		"pre-commit":         "commit",
		"prepare-commit-msg": "commit",
		"commit-msg":         "commit",
		"post-commit":        "commit",
	}

	for hook := range hooks {
		t.Run(hook, func(t *testing.T) {
			r := gitRepoFixture(t)
			writeGit(t, r.gitDir, "hooks/"+hook, "#!/bin/sh\n")

			out := r.git(t, `git commit -m "fix"`)
			if out.Status != action.StatusUnresolved {
				t.Fatalf("status = %s, want UNRESOLVED with a %s hook", out.Status, hook)
			}
			if !strings.Contains(out.StatusReason, hook) {
				t.Errorf("reason = %q, want it to name the hook", out.StatusReason)
			}
		})
	}
}

func TestGitNoVerifySkipsOnlyWhatGitSkips(t *testing.T) {
	// --no-verify skips pre-commit and commit-msg. Claiming it skips the
	// others would let unread code run under an approval.
	skipped := map[string]bool{
		"pre-commit":         true,
		"commit-msg":         true,
		"prepare-commit-msg": false,
		"post-commit":        false,
	}

	for hook, isSkipped := range skipped {
		t.Run(hook, func(t *testing.T) {
			r := gitRepoFixture(t)
			writeGit(t, r.gitDir, "hooks/"+hook, "#!/bin/sh\n")

			out := r.git(t, `git commit --no-verify -m "fix"`)
			resolved := out.Status == action.StatusResolved
			if resolved != isSkipped {
				t.Errorf("with --no-verify and a %s hook: status = %s, want resolved = %v",
					hook, out.Status, isSkipped)
			}
		})
	}
}

func TestGitPushHookIsSkippedByNoVerify(t *testing.T) {
	r := gitRepoFixture(t)
	writeGit(t, r.gitDir, "hooks/pre-push", "#!/bin/sh\n")

	if out := r.git(t, "git push origin main"); out.Status != action.StatusUnresolved {
		t.Errorf("status = %s, want UNRESOLVED with a pre-push hook", out.Status)
	}
	if out := r.git(t, "git push --no-verify origin main"); out.Status != action.StatusResolved {
		t.Errorf("status = %s, want RESOLVED with --no-verify", out.Status)
	}
}

func TestGitGlobalConfigHooksPathIsHonored(t *testing.T) {
	// A hooks directory configured globally applies to every repository.
	r := gitRepoFixture(t)

	hooks := filepath.Join(r.home, "global-hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	r.writeHome(t, ".gitconfig", "[core]\n\thooksPath = "+hooks+"\n")

	out := r.git(t, `git commit -m "fix"`)
	if out.Status != action.StatusUnresolved {
		t.Errorf("status = %s, want UNRESOLVED for a globally configured hook (§15.7)", out.Status)
	}
}

func TestGitRevParseAcceptsUnlistedOptions(t *testing.T) {
	r := gitRepoFixture(t)

	for _, command := range []string{
		"git rev-parse --abbrev-ref HEAD",
		"git rev-parse --verify HEAD",
		"git rev-parse --is-inside-work-tree",
	} {
		out := r.git(t, command)
		if out.Status != action.StatusResolved {
			t.Errorf("%q: status = %s (%s), want RESOLVED", command, out.Status, out.StatusReason)
		}
	}
}
