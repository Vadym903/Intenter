package resolver

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/parser"
)

// GitRecognizer models the git subcommands of §15.4. No git process is ever
// executed: remotes, branches and hooks are read from the repository files
// (§15.4, §15.7).
func GitRecognizer() Recognizer { return gitRecognizer{} }

type gitRecognizer struct{}

func (gitRecognizer) Names() []string { return []string{"git"} }

// gitSafeGlobals are the global options that do not change which repository is
// operated on or how commands behave. Everything else, `-c key=value` included,
// makes the command UNRESOLVED (§15.4).
//
// `--paginate` (and `-p`) is deliberately absent: it forces git to spawn the
// pager even when stdout is not a terminal, and the pager is whatever `$PAGER`
// or `core.pager` says — a command line git hands to `sh -c`. A read that runs
// an arbitrary program is not a read.
var gitSafeGlobals = map[string]bool{
	"--no-pager": true, "-P": true,
	"--no-replace-objects": true, "--literal-pathspecs": true,
	"--no-optional-locks": true, "--glob-pathspecs": true, "--noglob-pathspecs": true,
}

func (g gitRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := req.Command.Args()

	repoDir, subcommandAt, unknown := gitGlobalOptions(args)
	if len(unknown) > 0 {
		return Unresolved(req, action.OpUnknown, fmt.Sprintf(
			"the git global option %s changes what the command does", strings.Join(unknown, " ")))
	}
	if subcommandAt < 0 {
		return Unresolved(req, action.OpUnknown, "git was called without a subcommand")
	}

	// -C moves git to another directory before running the subcommand, which
	// also moves the paths its arguments are relative to.
	if repoDir.Text != "" {
		target, ok := req.TargetFor(repoDir)
		if !ok || target.Canonical == "" {
			return Unresolved(req, action.OpUnknown, "git -C names a directory Intenter cannot resolve")
		}
		req.Command.EffectiveCwd = target.Canonical
	}

	subcommand := args[subcommandAt].Text
	rest := args[subcommandAt+1:]
	repo := gitRepositoryFor(req)

	switch subcommand {
	case "status":
		return gitRead(req, repo, action.OpGitStatus, gitStatusGrammar, rest)
	case "diff":
		return gitDiff(req, repo, rest)
	case "log":
		return gitRead(req, repo, action.OpGitLog, gitLogGrammar, rest)
	case "show":
		return gitRead(req, repo, action.OpGitShow, gitShowGrammar, rest)
	case "rev-parse":
		return gitRead(req, repo, action.OpGitRevParse, gitRevParseGrammar, rest)
	case "branch":
		return gitBranch(req, repo, rest)
	case "add":
		return gitAdd(req, repo, rest)
	case "commit":
		return gitCommit(req, repo, rest)
	case "checkout", "switch", "restore":
		return gitCheckout(req, repo, subcommand, rest)
	case "reset":
		return gitReset(req, repo, rest)
	case "push":
		return gitPush(req, repo, rest)
	}
	return Unresolved(req, action.OpUnknown,
		fmt.Sprintf("git %s is not a subcommand Intenter models", subcommand))
}

// gitGlobalOptions splits the options that precede the subcommand. It returns
// the -C directory, the index of the subcommand (-1 when there is none) and any
// global option outside the safe set.
func gitGlobalOptions(args []parser.Word) (repoDir parser.Word, subcommandAt int, unknown []string) {
	for i := 0; i < len(args); i++ {
		text := args[i].Text
		if !isOption(text) {
			return repoDir, i, unknown
		}
		switch {
		case gitSafeGlobals[text]:
		case text == "-C":
			if i+1 >= len(args) {
				return repoDir, -1, append(unknown, "-C")
			}
			repoDir = args[i+1]
			i++
		case strings.HasPrefix(text, "-C") && len(text) > 2:
			repoDir = parser.Word{Text: text[2:], ContainsUnexpandedVar: args[i].ContainsUnexpandedVar}
		default:
			unknown = append(unknown, text)
		}
	}
	return repoDir, -1, unknown
}

// gitRepo is the repository a subcommand operates on, which is the one
// containing the effective cwd and not necessarily the workspace (§16.2).
type gitRepo struct {
	root string
	info *action.GitInfo
}

// gitRepositoryFor finds the repository for the command's effective cwd,
// reusing the request context when it is the workspace repository.
func gitRepositoryFor(req Request) gitRepo {
	cwd := canonicalize(req.Command.EffectiveCwd)
	root := nearestGitRoot(cwd)
	if root == "" {
		// Outside a repository git fails, but the paths it would touch are
		// still classified by where the shell actually is.
		return gitRepo{root: cwd}
	}
	ctx := req.Context
	if ctx == nil || ctx.Action == nil {
		return gitRepo{root: root}
	}
	if root == ctx.Action.WorkspaceRoot {
		return gitRepo{root: root, info: ctx.Action.Git}
	}
	return gitRepo{root: root, info: ReadGitInfo(root, ctx.Action.HomeDir)}
}

// gitDir is the repository's metadata directory, which may live outside the
// repository for worktrees and submodules.
func (g gitRepo) gitDir() string {
	if g.info != nil && g.info.GitDir != "" {
		return g.info.GitDir
	}
	if g.root == "" {
		return ""
	}
	return filepath.Join(g.root, ".git")
}

// hookRunning returns the first applicable hook that exists, honoring the hooks
// a --no-verify actually skips (§15.7).
func (g gitRepo) hookRunning(op action.SemanticOp, skipped ...string) (string, bool) {
	for _, name := range HooksForOperation(op) {
		if contains(skipped, name) {
			continue
		}
		if g.info.HasHook(name) {
			return name, true
		}
	}
	return "", false
}

// gitCommand starts a resolved git command.
func gitCommand(req Request, op action.SemanticOp) action.ResolvedCommand {
	return resolved(req, op)
}

// addRepoEffect records an effect on the repository working tree.
func addRepoEffect(out *action.ResolvedCommand, req Request, repo gitRepo, kind action.EffectType, flags ...action.EffectFlag) {
	if target, ok := req.PathTarget(repo.root); ok {
		addEffect(out, target, kind, flags...)
	}
}

// addGitDirEffect records an effect on the repository metadata directory.
func addGitDirEffect(out *action.ResolvedCommand, req Request, repo gitRepo, kind action.EffectType, flags ...action.EffectFlag) {
	if target, ok := req.PathTarget(repo.gitDir()); ok {
		addEffect(out, target, kind, flags...)
	}
}

// addHookEffect degrades a git command whose client-side hook runs workspace
// code Intenter never sees (§15.7).
func addHookEffect(out *action.ResolvedCommand, repo gitRepo, op action.SemanticOp, skipped ...string) {
	hook, ok := repo.hookRunning(op, skipped...)
	if !ok {
		return
	}
	out.Effects = append(out.Effects, action.Effect{
		Type:    action.EffectExecute,
		Program: &action.ProgramRef{Name: "git hook " + hook, Resolution: action.ProgramUnresolved},
	})
	degrade(out, fmt.Sprintf("the %s hook runs code Intenter cannot model", hook))
}

var gitStatusGrammar = Grammar{
	Safe: []string{
		"-s", "--short", "-b", "--branch", "-v", "--verbose", "-z", "--long",
		"--no-renames", "--renames", "--ahead-behind", "--no-ahead-behind",
		"--no-column", "-u", "-uno", "-unormal", "-uall", "--find-renames",
	},
	SafeOptionalValue: []string{"--porcelain", "--untracked-files", "--ignored", "--column", "--ignore-submodules"},
}

var gitLogGrammar = Grammar{
	Safe: []string{
		"--oneline", "--graph", "--decorate", "--no-decorate", "--stat", "-p",
		"--patch", "--no-patch", "--all", "--no-merges", "--merges",
		"--first-parent", "--reverse", "--follow", "--name-only",
		"--name-status", "--numstat", "--shortstat", "--abbrev-commit",
		"--topo-order", "--date-order", "--full-history", "--source", "-z",
	},
	SafeValue:         []string{"-n", "--max-count", "--skip"},
	SafeOptionalValue: []string{"--pretty", "--format", "--date", "--since", "--until", "--author", "--grep", "--committer", "--decorate"},
	SafeNumericShort:  true,
}

var gitShowGrammar = Grammar{
	Safe: []string{
		"--stat", "-p", "--patch", "--no-patch", "-s", "--name-only",
		"--name-status", "--numstat", "--shortstat", "--abbrev-commit", "-z",
	},
	SafeOptionalValue: []string{"--pretty", "--format", "--date"},
}

// gitRevParseGrammar accepts unlisted options: rev-parse only reports
// repository metadata and cannot be made to write.
var gitRevParseGrammar = Grammar{PermissiveUnknown: true}

var gitDiffGrammar = Grammar{
	Safe: []string{
		"--stat", "--cached", "--staged", "--name-only", "--name-status",
		"--numstat", "--shortstat", "-p", "--patch", "--no-patch", "--raw",
		"--check", "--summary", "--quiet", "--exit-code", "--no-index", "-z",
		"-w", "--ignore-all-space", "-b", "--ignore-space-change",
		"--ignore-blank-lines", "--word-diff", "--no-color", "--color-words",
		"-M", "--find-renames", "--find-copies", "--full-index", "--binary",
	},
	SafeValue:         []string{"-U", "--unified"},
	SafeOptionalValue: []string{"--color", "--diff-filter", "--word-diff", "--stat"},
	SemanticValue:     []string{"-o", "--output"},
}

// gitRead handles the subcommands that only read the repository.
func gitRead(req Request, repo gitRepo, op action.SemanticOp, grammar Grammar, args []parser.Word) action.ResolvedCommand {
	scan := grammar.Scan(args)
	out := gitCommand(req, op)
	if !scan.OK() {
		return Unresolved(req, op, scan.UnknownReason("git "+string(op)))
	}

	addRepoEffect(&out, req, repo, action.EffectRead)
	for _, pathspec := range gitPathspecs(args) {
		for _, target := range req.TargetsFor(pathspec) {
			addEffect(&out, target, action.EffectRead)
		}
	}
	return out
}

// gitDiff is a read subcommand that can also write a patch file.
func gitDiff(req Request, repo gitRepo, args []parser.Word) action.ResolvedCommand {
	scan := gitDiffGrammar.Scan(args)
	out := gitCommand(req, action.OpGitDiff)
	if !scan.OK() {
		return Unresolved(req, action.OpGitDiff, scan.UnknownReason("git diff"))
	}

	// `--no-index` compares two arbitrary paths, ignoring the repository
	// boundary git otherwise enforces, and prints their contents. Its operands
	// are the files actually read — `git diff --no-index ~/.ssh/id_rsa README.md`
	// is a read of a private key, not of the workspace — so they are modeled as
	// targets and classified like any other read.
	if scan.Has("--no-index") {
		if len(scan.Operands) != 2 {
			return Unresolved(req, action.OpGitDiff, "git diff --no-index needs exactly two paths")
		}
		for _, operand := range scan.Operands {
			if operand.Text == "-" {
				continue
			}
			for _, target := range req.TargetsFor(operand) {
				addEffect(&out, target, action.EffectRead)
			}
		}
	} else {
		addRepoEffect(&out, req, repo, action.EffectRead)
	}
	for _, pathspec := range gitPathspecs(args) {
		for _, target := range req.TargetsFor(pathspec) {
			addEffect(&out, target, action.EffectRead)
		}
	}
	for _, option := range []string{"-o", "--output"} {
		if !scan.Has(option) {
			continue
		}
		if target, ok := req.TargetFor(scan.Value(option)); ok {
			addEffect(&out, target, action.EffectCreate)
			addEffect(&out, target, action.EffectWrite)
		}
	}
	return out
}

var gitBranchGrammar = Grammar{
	Safe: []string{
		"-a", "--all", "-r", "--remotes", "-v", "-vv", "--verbose", "--list",
		"--show-current", "-q", "--quiet", "--no-color", "-i", "--ignore-case",
	},
	SafeValue:         []string{"--contains", "--no-contains", "--merged", "--no-merged", "--sort", "--points-at"},
	SafeOptionalValue: []string{"--format", "--color", "--column"},
}

// gitBranchWriteOptions turn `branch` from a listing into a change Intenter
// does not model (§15.4).
var gitBranchWriteOptions = []string{
	"-d", "-D", "--delete", "-m", "-M", "--move", "-c", "-C", "--copy",
	"--set-upstream-to", "-u", "--unset-upstream", "--edit-description",
	"-f", "--force", "--create-reflog",
}

func gitBranch(req Request, repo gitRepo, args []parser.Word) action.ResolvedCommand {
	for _, word := range args {
		name, _, _ := strings.Cut(word.Text, "=")
		if contains(gitBranchWriteOptions, name) {
			return Unresolved(req, action.OpGitBranch, fmt.Sprintf(
				"git branch %s changes refs, which Intenter models only as a listing", name))
		}
	}

	scan := gitBranchGrammar.Scan(args)
	out := gitCommand(req, action.OpGitBranch)
	if !scan.OK() {
		return Unresolved(req, action.OpGitBranch, scan.UnknownReason("git branch"))
	}
	addRepoEffect(&out, req, repo, action.EffectRead)
	return out
}

var gitAddGrammar = Grammar{
	Safe: []string{
		"-A", "--all", "-u", "--update", "-v", "--verbose", "-f", "--force",
		"-n", "--dry-run", "--ignore-removal", "--no-ignore-removal",
		"--renormalize", "-N", "--intent-to-add", "--refresh", "--ignore-errors",
	},
	SemanticValue: []string{"--pathspec-from-file"},
}

func gitAdd(req Request, repo gitRepo, args []parser.Word) action.ResolvedCommand {
	scan := gitAddGrammar.Scan(args)
	out := gitCommand(req, action.OpGitAdd)
	if !scan.OK() {
		return Unresolved(req, action.OpGitAdd, scan.UnknownReason("git add"))
	}

	pathspecs := scan.Operands
	if len(pathspecs) == 0 {
		// `git add -A` stages the whole tree.
		addRepoEffect(&out, req, repo, action.EffectRead)
	}
	for _, pathspec := range pathspecs {
		for _, target := range req.TargetsFor(pathspec) {
			addEffect(&out, target, action.EffectRead)
		}
	}
	if scan.Has("--pathspec-from-file") {
		for _, target := range req.TargetsFor(scan.Value("--pathspec-from-file")) {
			addEffect(&out, target, action.EffectRead)
		}
	}
	addGitDirEffect(&out, req, repo, action.EffectWrite)
	return out
}

var gitCommitGrammar = Grammar{
	Safe: []string{
		"--no-edit", "-q", "--quiet", "-v", "--verbose", "-s", "--signoff",
		"--allow-empty", "--allow-empty-message", "--no-verify", "-n",
		"--verify", "--amend", "--no-post-rewrite", "--short", "--porcelain",
		"--dry-run", "--status", "--no-status", "-a", "--all",
	},
	SafeValue:         []string{"-m", "--message", "-am", "-c", "-C", "--reuse-message", "--reedit-message", "--fixup", "--squash"},
	SafeOptionalValue: []string{"--author", "--date", "--cleanup", "--untracked-files"},
	SemanticValue:     []string{"-F", "--file", "-t", "--template"},
}

func gitCommit(req Request, repo gitRepo, args []parser.Word) action.ResolvedCommand {
	scan := gitCommitGrammar.Scan(args)
	out := gitCommand(req, action.OpGitCommit)
	if !scan.OK() {
		return Unresolved(req, action.OpGitCommit, scan.UnknownReason("git commit"))
	}

	// -am is the combined stage-and-message form, so it stages like -a.
	if scan.HasAny("-a", "--all", "-am") {
		addRepoEffect(&out, req, repo, action.EffectRead)
	}
	for _, operand := range scan.Operands {
		for _, target := range req.TargetsFor(operand) {
			addEffect(&out, target, action.EffectRead)
		}
	}
	for _, option := range []string{"-F", "--file", "-t", "--template"} {
		if !scan.Has(option) {
			continue
		}
		for _, target := range req.TargetsFor(scan.Value(option)) {
			addEffect(&out, target, action.EffectRead)
		}
	}
	addGitDirEffect(&out, req, repo, action.EffectWrite)

	// --no-verify skips only pre-commit and commit-msg; the other hooks run.
	var skipped []string
	if scan.HasAny("--no-verify", "-n") {
		skipped = []string{"pre-commit", "commit-msg"}
	}
	addHookEffect(&out, repo, action.OpGitCommit, skipped...)
	return out
}

var gitCheckoutGrammar = Grammar{
	Safe: []string{
		"-q", "--quiet", "--progress", "--no-progress", "--track", "--no-track",
		"--detach", "-f", "--force", "--discard-changes", "--ours", "--theirs",
		"-m", "--merge", "--overwrite-ignore", "--no-overwrite-ignore",
		"--recurse-submodules", "--no-recurse-submodules", "-p", "--patch",
		"-S", "--staged", "-W", "--worktree", "--overlay", "--no-overlay",
	},
	SafeValue:     []string{"-b", "-B", "-c", "-C", "--orphan", "--conflict"},
	SemanticValue: []string{"--source", "--pathspec-from-file"},
}

func gitCheckout(req Request, repo gitRepo, subcommand string, args []parser.Word) action.ResolvedCommand {
	scan := gitCheckoutGrammar.Scan(args)
	out := gitCommand(req, action.OpGitCheckout)
	if !scan.OK() {
		return Unresolved(req, action.OpGitCheckout, scan.UnknownReason("git "+subcommand))
	}

	// A forced checkout, a pathspec checkout and `restore` all overwrite
	// uncommitted work (§15.4).
	var flags []action.EffectFlag
	if scan.HasAny("-f", "--force", "--discard-changes") ||
		subcommand == "restore" || hasPathspecSeparator(args) {
		flags = append(flags, action.EffectFlagDiscardsChanges)
	}

	addRepoEffect(&out, req, repo, action.EffectWrite, flags...)
	for _, pathspec := range gitPathspecs(args) {
		for _, target := range req.TargetsFor(pathspec) {
			addEffect(&out, target, action.EffectWrite, flags...)
		}
	}
	addGitDirEffect(&out, req, repo, action.EffectWrite)
	addHookEffect(&out, repo, action.OpGitCheckout)
	return out
}

var gitResetGrammar = Grammar{
	Safe:          []string{"--soft", "--mixed", "-q", "--quiet", "--no-refresh", "--pathspec-file-nul"},
	Semantic:      []string{"--hard", "--merge", "--keep"},
	SemanticValue: []string{"--pathspec-from-file"},
}

func gitReset(req Request, repo gitRepo, args []parser.Word) action.ResolvedCommand {
	scan := gitResetGrammar.Scan(args)
	out := gitCommand(req, action.OpGitReset)
	if !scan.OK() {
		return Unresolved(req, action.OpGitReset, scan.UnknownReason("git reset"))
	}

	addGitDirEffect(&out, req, repo, action.EffectWrite)
	if scan.HasAny("--hard", "--merge", "--keep") {
		addRepoEffect(&out, req, repo, action.EffectWrite, action.EffectFlagDiscardsChanges)
	}
	return out
}

var gitPushGrammar = Grammar{
	Safe: []string{
		"-u", "--set-upstream", "-q", "--quiet", "-v", "--verbose",
		"--porcelain", "-n", "--dry-run", "--no-verify", "--verify",
		"--follow-tags", "--atomic", "--no-atomic", "--progress",
		"--no-progress", "--thin", "--no-thin", "--ipv4", "--ipv6",
	},
	SafeValue:             []string{"--repo", "--exec", "--receive-pack"},
	SafeOptionalValue:     []string{"--recurse-submodules", "--push-option", "-o"},
	Semantic:              []string{"-f", "--force", "--delete", "-d", "--all", "--mirror", "--tags", "--prune"},
	SemanticOptionalValue: []string{"--force-with-lease", "--force-if-includes"},
}

func gitPush(req Request, repo gitRepo, args []parser.Word) action.ResolvedCommand {
	scan := gitPushGrammar.Scan(args)
	out := gitCommand(req, action.OpGitPush)
	if !scan.OK() {
		return Unresolved(req, action.OpGitPush, scan.UnknownReason("git push"))
	}

	remote := "origin"
	if len(scan.Operands) > 0 {
		remote = scan.Operands[0].Text
	}
	host, known := gitRemoteHost(repo, remote)
	if !known {
		return Unresolved(req, action.OpGitPush, fmt.Sprintf(
			"the git remote %q is not configured in this repository", remote))
	}

	refspecs := scan.Operands
	if len(refspecs) > 0 {
		refspecs = refspecs[1:]
	}
	branch, branchKnown := gitPushBranch(repo, refspecs)

	var flags []action.EffectFlag
	if scan.HasAny("-f", "--force", "--force-with-lease", "--force-if-includes") || hasForcedRefspec(refspecs) {
		flags = append(flags, action.EffectFlagForce)
	}
	if scan.HasAny("--delete", "-d") || hasDeleteRefspec(refspecs) {
		flags = append(flags, action.EffectFlagDelete)
	}
	if scan.HasAny("--all", "--mirror", "--tags") {
		flags = append(flags, action.EffectFlagBroad)
	}

	effect := action.Effect{
		Type:    action.EffectNetwork,
		Network: &action.NetworkTarget{Host: host, Scheme: "git"},
	}
	effect.AddFlags(flags...)
	out.Effects = append(out.Effects, effect)
	out.Git = &action.GitDetail{Remote: remote, Branch: branch, BranchKnown: branchKnown}

	addGitDirEffect(&out, req, repo, action.EffectRead)

	var skipped []string
	if scan.Has("--no-verify") {
		skipped = []string{"pre-push"}
	}
	addHookEffect(&out, repo, action.OpGitPush, skipped...)
	return out
}

// gitRemoteHost resolves a remote name to its host. A remote written as a URL
// is resolved directly; an unconfigured name is not guessed at (§15.4).
func gitRemoteHost(repo gitRepo, remote string) (string, bool) {
	if repo.info != nil {
		if host, ok := repo.info.Remotes[remote]; ok && host != "" {
			return host, true
		}
	}
	if strings.Contains(remote, ":") || strings.Contains(remote, "://") {
		if host := RemoteHost(remote); host != "" {
			return host, true
		}
	}
	return "", false
}

// gitPushBranch determines which ref a push updates: the destination side of
// the first refspec, else the current branch (§15.4).
func gitPushBranch(repo gitRepo, refspecs []parser.Word) (string, bool) {
	for _, refspec := range refspecs {
		text := strings.TrimPrefix(refspec.Text, "+")
		if _, destination, ok := strings.Cut(text, ":"); ok {
			destination = strings.TrimSpace(destination)
			if destination == "" {
				// `src:` is not a form git accepts; treat the ref as unknown.
				return "", false
			}
			return gitShortRef(destination), true
		}
		if text != "" {
			return gitShortRef(text), true
		}
	}
	if repo.info != nil && repo.info.CurrentBranch != "" {
		return repo.info.CurrentBranch, true
	}
	return "", false
}

// gitShortRef trims the refs/heads/ prefix so a refspec and HEAD compare equal.
func gitShortRef(ref string) string {
	return strings.TrimPrefix(strings.TrimPrefix(ref, "refs/heads/"), "heads/")
}

// hasForcedRefspec reports the `+refspec` force form.
func hasForcedRefspec(refspecs []parser.Word) bool {
	for _, refspec := range refspecs {
		if strings.HasPrefix(refspec.Text, "+") {
			return true
		}
	}
	return false
}

// hasDeleteRefspec reports the `:branch` delete form.
func hasDeleteRefspec(refspecs []parser.Word) bool {
	for _, refspec := range refspecs {
		if strings.HasPrefix(strings.TrimPrefix(refspec.Text, "+"), ":") {
			return true
		}
	}
	return false
}

// gitPathspecs returns the words after `--`, which are the only operands git
// guarantees are paths. Words before it are revisions, and normalizing those
// would invent targets that do not exist.
func gitPathspecs(args []parser.Word) []parser.Word {
	for i, word := range args {
		if word.Text == "--" {
			return args[i+1:]
		}
	}
	return nil
}

// hasPathspecSeparator reports the `checkout -- <paths>` form, which overwrites
// the named files from the index (§15.4).
func hasPathspecSeparator(args []parser.Word) bool {
	for _, word := range args {
		if word.Text == "--" {
			return true
		}
	}
	return false
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
