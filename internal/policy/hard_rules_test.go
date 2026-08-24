package policy

import (
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/platform"
)

const (
	testWorkspace = "/w/demo"
	testHome      = "/Users/u"
)

func target(display, canonical string, scope action.Scope, flags ...action.TargetFlag) action.Target {
	out := action.Target{
		Raw:       display,
		Display:   display,
		Canonical: canonical,
		Scope:     scope,
		Status:    action.TargetResolved,
	}
	out.AddFlags(flags...)
	return out
}

func effect(kind action.EffectType, on action.Target, flags ...action.EffectFlag) action.Effect {
	pinned := on
	out := action.Effect{Type: kind, Target: &pinned}
	out.AddFlags(flags...)
	return out
}

func networkEffect(host string, flags ...action.EffectFlag) action.Effect {
	out := action.Effect{Type: action.EffectNetwork, Network: &action.NetworkTarget{Host: host, Scheme: "https"}}
	out.AddFlags(flags...)
	return out
}

func executeEffect(name string, streamed bool) action.Effect {
	return action.Effect{
		Type:    action.EffectExecute,
		Program: &action.ProgramRef{Name: name, Resolution: action.ProgramUnresolved, Streamed: streamed},
	}
}

// ruleInput builds an evaluation input around a set of effects.
func ruleInput(effects ...action.Effect) Input {
	return Input{
		Action: &action.ResolvedAction{
			Status:  action.StatusResolved,
			Effects: effects,
		},
		Context: &action.Context{
			WorkspaceRoot: testWorkspace,
			HomeDir:       testHome,
			Status:        action.ContextOK,
		},
		Config: config.Default(),
		Rules:  platform.PathRules{},
	}
}

// assertRule checks that a rule fired with the expected outcome, or did not.
func assertRule(t *testing.T, findings Findings, rule string, want Outcome) {
	t.Helper()
	for _, finding := range findings {
		if finding.Rule != rule {
			continue
		}
		if finding.Outcome != want {
			t.Errorf("%s outcome = %s, want %s (%s)", rule, finding.Outcome, want, finding.Reason)
		}
		if finding.Reason == "" {
			t.Errorf("%s fired without a reason", rule)
		}
		return
	}
	if want != OutcomePass {
		t.Errorf("%s did not fire; findings = %v", rule, findings.Reasons())
	}
}

func TestRuleR1SystemLocations(t *testing.T) {
	system := target("/usr/bin", "/usr/bin", action.ScopeSystem)
	workspace := target("./dist", testWorkspace+"/dist", action.ScopeWorkspaceGenerated)

	tests := []struct {
		name   string
		effect action.Effect
		want   Outcome
	}{
		{"delete", effect(action.EffectDelete, system), OutcomeBlock},
		{"write", effect(action.EffectWrite, system), OutcomeBlock},
		{"create", effect(action.EffectCreate, system), OutcomeBlock},
		{"read is allowed through R1", effect(action.EffectRead, system), OutcomePass},
		{"workspace delete", effect(action.EffectDelete, workspace), OutcomePass},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRule(t, HardRules(ruleInput(tt.effect)), "R1", tt.want)
		})
	}
}

func TestRuleR2HomeDeletes(t *testing.T) {
	documents := target("~/Documents", testHome+"/Documents", action.ScopeHome, action.FlagBroad)
	note := target("~/note.txt", testHome+"/note.txt", action.ScopeHome)
	wildcard := target("~/tmp/*", testHome+"/tmp", action.ScopeHome, action.FlagWildcard)
	credential := target("~/.ssh/id_rsa", testHome+"/.ssh/id_rsa", action.ScopeHome, action.FlagSensitive)

	tests := []struct {
		name   string
		effect action.Effect
		want   Outcome
	}{
		{"recursive", effect(action.EffectDelete, note, action.EffectFlagRecursive), OutcomeBlock},
		{"broad directory", effect(action.EffectDelete, documents), OutcomeBlock},
		{"wildcard target", effect(action.EffectDelete, wildcard), OutcomeBlock},
		{"wildcard effect", effect(action.EffectDelete, note, action.EffectFlagWildcard), OutcomeBlock},
		{"credential", effect(action.EffectDelete, credential), OutcomeBlock},
		{"single file", effect(action.EffectDelete, note), OutcomePass},
		{"read", effect(action.EffectRead, documents), OutcomePass},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRule(t, HardRules(ruleInput(tt.effect)), "R2", tt.want)
		})
	}
}

func TestRuleR3OutsideWorkspaceDeletes(t *testing.T) {
	directory := target("/srv/data", "/srv/data", action.ScopeOutsideWorkspace)
	directory.IsDir = true
	file := target("/srv/data/x.txt", "/srv/data/x.txt", action.ScopeOutsideWorkspace)
	tempFile := target("/tmp/build/x", "/tmp/build/x", action.ScopeOutsideWorkspace, action.FlagTemp)
	tempRoot := target("/tmp", "/tmp", action.ScopeOutsideWorkspace, action.FlagTemp, action.FlagBroad)

	tests := []struct {
		name   string
		effect action.Effect
		want   Outcome
	}{
		{"recursive", effect(action.EffectDelete, file, action.EffectFlagRecursive), OutcomeBlock},
		{"directory", effect(action.EffectDelete, directory), OutcomeBlock},
		{"single file", effect(action.EffectDelete, file), OutcomePass},
		{"inside temp", effect(action.EffectDelete, tempFile, action.EffectFlagRecursive), OutcomePass},
		{"the temp root itself", effect(action.EffectDelete, tempRoot, action.EffectFlagRecursive), OutcomeBlock},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRule(t, HardRules(ruleInput(tt.effect)), "R3", tt.want)
		})
	}
}

func TestRuleR4AsksAboutNarrowerOutsideDeletes(t *testing.T) {
	homeFile := target("~/note.txt", testHome+"/note.txt", action.ScopeHome)
	tempFile := target("/tmp/build/x", "/tmp/build/x", action.ScopeOutsideWorkspace, action.FlagTemp)
	workspaceFile := target("./dist/a.js", testWorkspace+"/dist/a.js", action.ScopeWorkspaceGenerated)

	assertRule(t, HardRules(ruleInput(effect(action.EffectDelete, homeFile))), "R4", OutcomeAskAlways)
	assertRule(t, HardRules(ruleInput(effect(action.EffectDelete, tempFile, action.EffectFlagRecursive))), "R4", OutcomeAskAlways)
	assertRule(t, HardRules(ruleInput(effect(action.EffectDelete, workspaceFile))), "R4", OutcomePass)

	// A delete R2 already blocked is not also reported as R4.
	blocked := HardRules(ruleInput(effect(action.EffectDelete, homeFile, action.EffectFlagRecursive)))
	assertRule(t, blocked, "R2", OutcomeBlock)
	assertRule(t, blocked, "R4", OutcomePass)
}

func TestRuleR5SensitiveTargets(t *testing.T) {
	key := target("~/.ssh/id_rsa", testHome+"/.ssh/id_rsa", action.ScopeHome, action.FlagSensitive)
	env := target("./.env", testWorkspace+"/.env", action.ScopeWorkspace, action.FlagSensitive)
	plain := target("./README.md", testWorkspace+"/README.md", action.ScopeWorkspace)

	tests := []struct {
		name   string
		effect action.Effect
		want   Outcome
	}{
		{"read a key", effect(action.EffectRead, key), OutcomeAskAlways},
		{"read a workspace env file", effect(action.EffectRead, env), OutcomeAskAlways},
		{"write", effect(action.EffectWrite, env), OutcomeBlock},
		{"create", effect(action.EffectCreate, env), OutcomeBlock},
		{"delete", effect(action.EffectDelete, env), OutcomeBlock},
		{"ordinary file", effect(action.EffectRead, plain), OutcomePass},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRule(t, HardRules(ruleInput(tt.effect)), "R5", tt.want)
		})
	}
}

func TestRuleR6EscapingPaths(t *testing.T) {
	traversal := target("../../etc/passwd", "/etc/passwd", action.ScopeSystem, action.FlagTraversal)
	symlink := target("./build/link", testHome+"/Documents", action.ScopeHome, action.FlagSymlinkEscape)
	plain := target("./dist", testWorkspace+"/dist", action.ScopeWorkspaceGenerated)

	assertRule(t, HardRules(ruleInput(effect(action.EffectRead, traversal))), "R6", OutcomeAskAlways)
	assertRule(t, HardRules(ruleInput(effect(action.EffectRead, symlink))), "R6", OutcomeAskAlways)
	assertRule(t, HardRules(ruleInput(effect(action.EffectRead, plain))), "R6", OutcomePass)

	// R6 is a floor, not a ceiling: a stronger rule still decides.
	findings := HardRules(ruleInput(effect(action.EffectDelete, traversal, action.EffectFlagRecursive)))
	if findings.Outcome() != OutcomeBlock {
		t.Errorf("outcome = %s, want BLOCK from R1 despite R6 (§18.2)", findings.Outcome())
	}
}

func TestRuleR7GitPush(t *testing.T) {
	push := func(git *action.GitDetail, flags ...action.EffectFlag) Input {
		network := networkEffect("github.com", flags...)
		in := ruleInput(network)
		in.Action.Commands = []action.ResolvedCommand{{
			SemanticOp: action.OpGitPush,
			Effects:    []action.Effect{network},
			Git:        git,
		}}
		in.Context.Git = &action.GitInfo{DefaultBranch: "main"}
		return in
	}

	feature := &action.GitDetail{Remote: "origin", Branch: "feature/login", BranchKnown: true}
	main := &action.GitDetail{Remote: "origin", Branch: "main", BranchKnown: true}
	release := &action.GitDetail{Remote: "origin", Branch: "release", BranchKnown: true}
	unknown := &action.GitDetail{Remote: "origin", BranchKnown: false}

	tests := []struct {
		name string
		in   Input
		want Outcome
	}{
		{"plain push", push(feature), OutcomePass},
		{"force to a feature branch", push(feature, action.EffectFlagForce), OutcomePass},
		{"force to the default branch", push(main, action.EffectFlagForce), OutcomeAskAlways},
		{"force to an unknown branch", push(unknown, action.EffectFlagForce), OutcomeAskAlways},
		{"delete", push(feature, action.EffectFlagDelete), OutcomeAskAlways},
		{"broad", push(feature, action.EffectFlagBroad), OutcomeAskAlways},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRule(t, HardRules(tt.in), "R7", tt.want)
		})
	}

	t.Run("configured protected branch", func(t *testing.T) {
		in := push(release, action.EffectFlagForce)
		in.Config.Policy.ProtectedBranches = []string{"release"}
		assertRule(t, HardRules(in), "R7", OutcomeAskAlways)
	})
}

func TestRuleR8DiscardingChanges(t *testing.T) {
	workspace := target(".", testWorkspace, action.ScopeWorkspace, action.FlagBroad)

	discarding := func(op action.SemanticOp, flags ...action.EffectFlag) Input {
		write := effect(action.EffectWrite, workspace, flags...)
		in := ruleInput(write)
		in.Action.Commands = []action.ResolvedCommand{{SemanticOp: op, Effects: []action.Effect{write}}}
		return in
	}

	assertRule(t, HardRules(discarding(action.OpGitCheckout, action.EffectFlagDiscardsChanges)), "R8", OutcomeAskAlways)
	assertRule(t, HardRules(discarding(action.OpGitReset, action.EffectFlagDiscardsChanges)), "R8", OutcomeAskAlways)
	assertRule(t, HardRules(discarding(action.OpGitCheckout)), "R8", OutcomePass)
	assertRule(t, HardRules(discarding(action.OpGitAdd, action.EffectFlagDiscardsChanges)), "R8", OutcomePass)
}

func TestRuleR9WorkspaceRoot(t *testing.T) {
	root := target(".", testWorkspace, action.ScopeWorkspace, action.FlagBroad)
	gitDir := target("./.git", testWorkspace+"/.git", action.ScopeWorkspace)
	inside := target("./dist", testWorkspace+"/dist", action.ScopeWorkspaceGenerated)

	assertRule(t, HardRules(ruleInput(effect(action.EffectDelete, root, action.EffectFlagRecursive))), "R9", OutcomeAskAlways)
	assertRule(t, HardRules(ruleInput(effect(action.EffectDelete, gitDir, action.EffectFlagRecursive))), "R9", OutcomeAskAlways)
	assertRule(t, HardRules(ruleInput(effect(action.EffectDelete, inside, action.EffectFlagRecursive))), "R9", OutcomePass)
	assertRule(t, HardRules(ruleInput(effect(action.EffectRead, root))), "R9", OutcomePass)
}

func TestRuleR10ElevationAndCredentials(t *testing.T) {
	dist := target("./dist", testWorkspace+"/dist", action.ScopeWorkspaceGenerated)

	assertRule(t, HardRules(ruleInput(effect(action.EffectDelete, dist, action.EffectFlagElevated))), "R10", OutcomeAskAlways)
	assertRule(t, HardRules(ruleInput(networkEffect("api.example.com", action.EffectFlagInlineCredential))), "R10", OutcomeAskAlways)
	assertRule(t, HardRules(ruleInput(effect(action.EffectDelete, dist))), "R10", OutcomePass)
}

func TestRuleR11InsecureTLS(t *testing.T) {
	assertRule(t, HardRules(ruleInput(networkEffect("api.example.com", action.EffectFlagInsecureTLS))), "R11", OutcomeAskAlways)
	assertRule(t, HardRules(ruleInput(networkEffect("api.example.com"))), "R11", OutcomePass)

	// Every parseable loopback form counts as local, not just 127.0.0.1:
	// 127.0.0.2 and the bracketed IPv6 forms are loopback too. A shorthand like
	// 127.1 that Go cannot parse falls through to ASK, which is the safe way to
	// be wrong for a rule that only decides whether to prompt.
	for _, host := range []string{
		"localhost", "127.0.0.1", "127.0.0.2", "::1", "[::1]",
		"0.0.0.0", "app.localhost",
	} {
		findings := HardRules(ruleInput(networkEffect(host, action.EffectFlagInsecureTLS)))
		assertRule(t, findings, "R11", OutcomePass)
	}

	// A genuine remote is still asked about, including a public IP address.
	for _, host := range []string{"93.184.216.34", "8.8.8.8", "example.com"} {
		findings := HardRules(ruleInput(networkEffect(host, action.EffectFlagInsecureTLS)))
		assertRule(t, findings, "R11", OutcomeAskAlways)
	}
}

func TestRuleR13IncompleteEvaluationAsks(t *testing.T) {
	// A command Intenter could not examine in full — too long, or resolution
	// timed out before the last command — is forced to a prompt rather than
	// deferred, so an unseen tail cannot be run by a string rule or in bypass.
	in := ruleInput()
	in.Action.MarkIncomplete("the command line was too long to examine in full")
	assertRule(t, HardRules(in), "R13", OutcomeAskAlways)

	// A fully examined action does not trip R13.
	assertRule(t, HardRules(ruleInput()), "R13", OutcomePass)
}

func TestRuleR12StreamedExecution(t *testing.T) {
	assertRule(t, HardRules(ruleInput(executeEffect("sh", true))), "R12", OutcomeAskAlways)

	// An ordinary unknown program is not R12; it is handled by the status.
	assertRule(t, HardRules(ruleInput(executeEffect("some-tool", false))), "R12", OutcomePass)

	declared := action.Effect{
		Type:    action.EffectExecute,
		Program: &action.ProgramRef{Name: "gradle", Resolution: action.ProgramDeclared, Streamed: true},
	}
	assertRule(t, HardRules(ruleInput(declared)), "R12", OutcomePass)
}

func TestHardRuleOutcomeOrdering(t *testing.T) {
	if got := Stronger(OutcomePass, OutcomeAskAlways); got != OutcomeAskAlways {
		t.Errorf("Stronger(PASS, ASK_ALWAYS) = %s", got)
	}
	if got := Stronger(OutcomeBlock, OutcomeAskAlways); got != OutcomeBlock {
		t.Errorf("Stronger(BLOCK, ASK_ALWAYS) = %s", got)
	}
	if got := Findings(nil).Outcome(); got != OutcomePass {
		t.Errorf("no findings = %s, want PASS", got)
	}
}

func TestHardRuleStrongestIsDeterministic(t *testing.T) {
	// Two rules of equal strength resolve to the lower-numbered one, so the
	// same action always explains itself the same way (§18.4).
	key := target("~/.ssh/id_rsa", testHome+"/.ssh/id_rsa", action.ScopeHome, action.FlagSensitive, action.FlagTraversal)

	first := HardRules(ruleInput(effect(action.EffectRead, key)))
	second := HardRules(ruleInput(effect(action.EffectRead, key)))
	if strings.Join(first.Reasons(), "|") != strings.Join(second.Reasons(), "|") {
		t.Fatalf("findings differ between runs:\n%v\n%v", first.Reasons(), second.Reasons())
	}

	strongest, ok := first.Strongest()
	if !ok {
		t.Fatal("want a finding")
	}
	if strongest.Rule != "R5" {
		t.Errorf("strongest rule = %s, want the lower-numbered R5 over R6", strongest.Rule)
	}
}

func TestHardRulesRunOverUnresolvedActions(t *testing.T) {
	// §18.1 step 1: the rules run over whatever was parsed, even when the
	// action as a whole is not understood.
	home := target("~", testHome, action.ScopeHome, action.FlagBroad)
	in := ruleInput(effect(action.EffectDelete, home, action.EffectFlagRecursive, action.EffectFlagElevated))
	in.Action.Status = action.StatusParseFailed

	findings := HardRules(in)
	if findings.Outcome() != OutcomeBlock {
		t.Errorf("outcome = %s, want BLOCK even on a PARSE_FAILED action", findings.Outcome())
	}
	assertRule(t, findings, "R2", OutcomeBlock)
	assertRule(t, findings, "R10", OutcomeAskAlways)
}

func TestHardRulesOnAnEmptyAction(t *testing.T) {
	if findings := HardRules(Input{}); len(findings) != 0 {
		t.Errorf("findings = %v, want none for an empty input", findings)
	}
	if findings := HardRules(ruleInput()); len(findings) != 0 {
		t.Errorf("findings = %v, want none for an action with no effects", findings)
	}
}

func TestHardRulesFindNestedGitCommands(t *testing.T) {
	// A push reached through `npm run deploy` is still a push.
	network := networkEffect("github.com", action.EffectFlagBroad)
	in := ruleInput(network)
	in.Action.Commands = []action.ResolvedCommand{{
		SemanticOp: action.OpRunScript,
		Children: []action.ResolvedCommand{{
			SemanticOp: action.OpGitPush,
			Effects:    []action.Effect{network},
			Git:        &action.GitDetail{Remote: "origin", Branch: "main", BranchKnown: true},
		}},
	}}
	assertRule(t, HardRules(in), "R7", OutcomeAskAlways)
}
