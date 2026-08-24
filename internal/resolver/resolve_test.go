package resolver

import (
	"strings"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/parser"
	"github.com/Vadym903/Intenter/internal/version"
)

func TestResolveAggregatesEveryCommandOfTheLine(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	out := r.resolve(t, "git status && rm -rf ./dist")
	if out.Status != action.StatusResolved {
		t.Fatalf("status = %s (%s)", out.Status, out.StatusReason)
	}
	if len(out.Commands) != 2 {
		t.Fatalf("commands = %d, want 2", len(out.Commands))
	}
	want := []action.SemanticOp{action.OpGitStatus, action.OpFSDelete}
	for i, op := range want {
		if out.SemanticOps[i] != op {
			t.Errorf("semantic op %d = %s, want %s", i, out.SemanticOps[i], op)
		}
	}
	assertActionEffects(t, out, "READ .", "DELETE(force,recursive) ./dist")
}

func TestResolveTakesTheWeakestStatus(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	out := r.resolve(t, "git status && some-unknown-tool")
	if out.Status != action.StatusUnresolved {
		t.Errorf("status = %s, want the weakest status of the line (§13.6)", out.Status)
	}
	if !strings.Contains(out.StatusReason, "some-unknown-tool") {
		t.Errorf("reason = %q, want it to name the unresolved command", out.StatusReason)
	}
	// Both branches of a conditional are evaluated (§14.2).
	if len(out.Commands) != 2 {
		t.Errorf("commands = %d, want both branches", len(out.Commands))
	}
}

func TestResolveEffectsAreTheUnionOverBranches(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	out := r.resolve(t, "rm -rf ./dist || rm -rf ./build")
	assertActionEffects(t, out,
		"DELETE(force,recursive) ./dist",
		"DELETE(force,recursive) ./build")
}

func TestResolveParseFailureIsNotApprovable(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	tests := []string{
		"for f in *; do rm $f; done",
		"rm -rf $(cat target.txt)",
		`eval "rm -rf ~"`,
		"echo )",
	}
	for _, command := range tests {
		out := r.resolve(t, command)
		if out.Status.Approvable() {
			t.Errorf("%q: status = %s, want a non-approvable status (I-2)", command, out.Status)
		}
		if out.ActionKey != "" {
			t.Errorf("%q: an unapprovable action must have no action key (I-11)", command)
		}
		if len(out.Unsupported) == 0 {
			t.Errorf("%q: the refused constructs must be recorded", command)
		}
	}
}

func TestResolveActionKeyIsDeterministicAndSpecific(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{"cleanup":"rm -rf ./dist"}}`)

	first := r.resolve(t, "npm run cleanup")
	second := r.resolve(t, "npm run cleanup")
	if first.ActionKey == "" {
		t.Fatal("a resolved action must have an action key")
	}
	if first.ActionKey != second.ActionKey {
		t.Error("the same action must produce the same key")
	}

	other := r.resolve(t, "rm -rf ./dist")
	if other.ActionKey == first.ActionKey {
		t.Error("a different semantic op must produce a different key")
	}
}

func TestResolveActionKeyChangesWithTheTarget(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	dist := r.resolve(t, "rm -rf ./dist")
	src := r.resolve(t, "rm -rf ./src")
	if dist.ActionKey == src.ActionKey {
		t.Error("a different target must produce a different action key")
	}
}

func TestResolveUnknownDialectIsRefused(t *testing.T) {
	// A dialect with no parser must fail closed rather than be guessed at.
	r := nodeRepo(t, `{"scripts":{}}`)

	resolver := New(r.builder, version.EngineVersion)
	resolver.parsers = parser.NewRegistry()

	out := resolver.ResolveInContext(action.ActionRequest{
		Dialect:    action.DialectPosix,
		RawCommand: "rm -rf ./dist",
		Cwd:        r.root,
	}, r.builder.Build(r.root, ""))

	if out.Status != action.StatusParseFailed {
		t.Errorf("status = %s, want PARSE_FAILED when no parser is registered", out.Status)
	}
	if !strings.Contains(out.StatusReason, "posix") {
		t.Errorf("reason = %q, want it to name the dialect", out.StatusReason)
	}
}

func TestResolveRegistersEveryDialect(t *testing.T) {
	// §14.4: the Windows dialects parse text, so they are available on every
	// OS — a script has to be understood under both before its effects can be
	// combined (I-13).
	parsers := NewParserRegistry()

	for _, dialect := range []action.Dialect{
		action.DialectPosix, action.DialectPowerShell, action.DialectCmd,
	} {
		if _, err := parsers.Get(dialect); err != nil {
			t.Errorf("dialect %s is not registered: %v", dialect, err)
		}
	}
}

func TestResolveDefaultsToPosixWhenNoDialectIsGiven(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	out, _ := New(r.builder, version.EngineVersion).Resolve(action.ActionRequest{
		RawCommand: "rm -rf ./dist",
		Cwd:        r.root,
	})
	if out.Dialect != action.DialectPosix {
		t.Errorf("dialect = %s, want posix", out.Dialect)
	}
	if out.Status != action.StatusResolved {
		t.Errorf("status = %s (%s)", out.Status, out.StatusReason)
	}
}

func TestResolveWithoutAUsableWorkspaceFailsTheContext(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	// HOME is not a valid workspace root (§16.2).
	out, ctx := New(r.builder, version.EngineVersion).Resolve(action.ActionRequest{
		Dialect:    action.DialectPosix,
		RawCommand: "rm -rf ./dist",
		Cwd:        r.home,
	})
	if ctx.Action.Status != action.ContextWorkspaceUndefined {
		t.Fatalf("context status = %s, want WORKSPACE_UNDEFINED", ctx.Action.Status)
	}
	if out.Status != action.StatusContextFailed {
		t.Errorf("status = %s, want CONTEXT_FAILED", out.Status)
	}
	if out.Status.Approvable() {
		t.Error("a context failure must not be approvable (I-11)")
	}
}

func TestResolveDepthLimitStopsWrapperRecursion(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{
		"a":"npm run b",
		"b":"npm run c",
		"c":"npm run d",
		"d":"npm run e",
		"e":"npm run f",
		"f":"rm -rf ./dist"
	}}`)

	out := r.resolve(t, "npm run a")
	if out.Status != action.StatusUnresolved {
		t.Errorf("status = %s, want UNRESOLVED past the depth limit (§15.1)", out.Status)
	}
	if !strings.Contains(out.StatusReason, "deeper") {
		t.Errorf("reason = %q, want it to name the depth limit", out.StatusReason)
	}
}

func TestResolveShallowWrapperChainStaysResolved(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{"a":"npm run b","b":"rm -rf ./dist"}}`)

	out := r.resolve(t, "npm run a")
	if out.Status != action.StatusResolved {
		t.Fatalf("status = %s (%s), want RESOLVED within the depth limit", out.Status, out.StatusReason)
	}
	assertActionEffects(t, out, "DELETE(force,recursive) ./dist")
}

func TestResolveCommandLimit(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	// Past the command cap the action is not approvable, but every command is
	// still kept so the hard rules run over all of them — a delete padded to the
	// end of a long line must not slip past the safety floor (§15.1).
	command := strings.Repeat("git status; ", 40) + "rm -rf ~/Documents"
	out := r.resolve(t, command)
	if out.Status.Approvable() {
		t.Errorf("status = %s, want a non-approvable status past the command limit (§15.1)", out.Status)
	}
	last := out.Commands[len(out.Commands)-1]
	if last.SemanticOp != action.OpFSDelete {
		t.Errorf("the command past the cap must still be resolved for the hard rules, got %s", last.SemanticOp)
	}
}

func TestResolveTimeBudgetIsEnforced(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{"cleanup":"rm -rf ./dist"}}`)

	resolver := New(r.builder, version.EngineVersion)
	// The first reading sets the deadline; every later one is past it.
	base := time.Now()
	readings := 0
	resolver.now = func() time.Time {
		readings++
		if readings == 1 {
			return base
		}
		return base.Add(time.Hour)
	}

	out, _ := resolver.Resolve(action.ActionRequest{
		Dialect:    action.DialectPosix,
		RawCommand: "npm run cleanup",
		Cwd:        r.root,
	})
	if out.Status.Approvable() {
		t.Errorf("status = %s, want a non-approvable status past the time budget (§15.1)", out.Status)
	}
	if !strings.Contains(out.StatusReason, "budget") {
		t.Errorf("reason = %q, want it to name the budget", out.StatusReason)
	}
}

func TestResolveCdTrackingMovesLaterCommands(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	out := r.resolve(t, "cd ~ && git status")
	if len(out.Commands) != 2 {
		t.Fatalf("commands = %d, want 2", len(out.Commands))
	}
	// After `cd ~` the repository lookup starts at HOME, which is outside the
	// workspace, so the target keeps its real scope (§16.2).
	targets := out.Targets()
	if len(targets) != 1 {
		t.Fatalf("targets = %+v, want one", targets)
	}
	if targets[0].Scope != action.ScopeHome {
		t.Errorf("scope = %s, want HOME", targets[0].Scope)
	}
}

func TestResolveExplanationIsDeterministic(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{"cleanup":"rm -rf ./dist"}}`)

	first := r.resolve(t, "npm run cleanup")
	second := r.resolve(t, "npm run cleanup")
	if strings.Join(first.Explanation, "\n") != strings.Join(second.Explanation, "\n") {
		t.Errorf("explanations differ between runs:\n%v\n%v", first.Explanation, second.Explanation)
	}
	joined := strings.Join(first.Explanation, "\n")
	for _, want := range []string{"targets:", "effects:", "fingerprints:"} {
		if !strings.Contains(joined, want) {
			t.Errorf("explanation is missing %q: %v", want, first.Explanation)
		}
	}
}

func TestResolveRecordsTheProjectIdentity(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	out := r.resolve(t, "git status")
	if out.ProjectID == "" {
		t.Error("the resolved action must carry the project identity")
	}
	if out.ProjectID != action.ProjectID(r.root) {
		t.Errorf("project id = %q, want the hash of the canonical workspace root", out.ProjectID)
	}
}

func TestResolveEmptyCommand(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	out := r.resolve(t, "")
	if len(out.Commands) != 0 {
		t.Errorf("commands = %d, want none", len(out.Commands))
	}
	if out.Status != action.StatusResolved {
		t.Errorf("status = %s, want RESOLVED for an empty line", out.Status)
	}
}

func TestResolveHardRulesStillSeeTheParsedCommandsOfARefusedLine(t *testing.T) {
	// I-2: refused syntax caps the decision, but what parsed is still resolved
	// so the policy engine can block on it.
	r := nodeRepo(t, `{"scripts":{}}`)

	out := r.resolve(t, "sudo rm -rf ~")
	if out.Status.Approvable() {
		t.Fatalf("status = %s, want a non-approvable status", out.Status)
	}
	if len(out.Effects) == 0 {
		t.Fatal("the inner delete must still be modeled")
	}
	found := false
	for _, effect := range out.Effects {
		if effect.Type == action.EffectDelete && effect.Target != nil && effect.Target.Scope == action.ScopeHome {
			found = true
		}
	}
	if !found {
		t.Errorf("effects = %v, want the HOME delete of the inner command", actionEffects(out))
	}
}

func TestResolveRegistriesCoverTheRequiredRecognizers(t *testing.T) {
	registry := NewRecognizerRegistry()

	for _, name := range []string{"rm", "cp", "mv", "mkdir", "cat", "grep", "find", "git", "npm", "pnpm", "yarn", "npx"} {
		if _, ok := registry.Lookup(name); !ok {
			t.Errorf("recognizer for %q is not registered", name)
		}
	}

	parsers := NewParserRegistry()
	if _, err := parsers.Get(action.DialectPosix); err != nil {
		t.Errorf("the posix dialect must be registered: %v", err)
	}
}
