package posix

import (
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/parser"
)

const (
	testHome = "/Users/u"
	testCwd  = "/w/demo"
	testTemp = "/tmp/user"
)

func testInput(command string) parser.Input {
	return parser.Input{Command: command, Cwd: testCwd, Home: testHome, TempDir: testTemp}
}

func mustParse(t *testing.T, command string) *parser.ParsedCommand {
	t.Helper()
	out, err := New().Parse(testInput(command))
	if err != nil {
		t.Fatalf("Parse(%q) returned an internal error: %v", command, err)
	}
	if out == nil {
		t.Fatalf("Parse(%q) returned no result", command)
	}
	return out
}

func TestParseSimpleCommands(t *testing.T) {
	tests := []struct {
		name    string
		command string
		argv    [][]string
	}{
		{"single", "rm -rf ./dist", [][]string{{"rm", "-rf", "./dist"}}},
		{"sequence", "git status; git diff", [][]string{{"git", "status"}, {"git", "diff"}}},
		{"and", "npm run build && npm test", [][]string{{"npm", "run", "build"}, {"npm", "test"}}},
		{"or", "make || echo failed", [][]string{{"make"}, {"echo", "failed"}}},
		{"pipeline", "cat a.txt | grep x", [][]string{{"cat", "a.txt"}, {"grep", "x"}}},
		{"comment stripped", "rm -rf ./dist # clean up", [][]string{{"rm", "-rf", "./dist"}}},
		{"grouping", "{ git status; git diff; }", [][]string{{"git", "status"}, {"git", "diff"}}},
		{"subshell", "(git status)", [][]string{{"git", "status"}}},
		{"negated", "! git diff --quiet", [][]string{{"git", "diff", "--quiet"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := mustParse(t, tt.command)
			if !out.OK() {
				t.Fatalf("unexpected unsupported constructs: %v", out.UnsupportedSummary())
			}
			if len(out.Commands) != len(tt.argv) {
				t.Fatalf("got %d commands, want %d", len(out.Commands), len(tt.argv))
			}
			for i, want := range tt.argv {
				got := append([]string{out.Commands[i].Name()}, out.Commands[i].ArgTexts()...)
				if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
					t.Errorf("command %d = %v, want %v", i, got, want)
				}
			}
		})
	}
}

func TestParseOperators(t *testing.T) {
	tests := []struct {
		command string
		want    []parser.Operator
	}{
		{"a; b", []parser.Operator{parser.OpSequence}},
		{"a && b", []parser.Operator{parser.OpAnd}},
		{"a || b", []parser.Operator{parser.OpOr}},
		{"a | b", []parser.Operator{parser.OpPipe}},
		{"a && b | c", []parser.Operator{parser.OpAnd, parser.OpPipe}},
		{"a | b && c", []parser.Operator{parser.OpPipe, parser.OpAnd}},
		{"a; b && c || d", []parser.Operator{parser.OpSequence, parser.OpAnd, parser.OpOr}},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			out := mustParse(t, tt.command)
			if len(out.Operators) != len(tt.want) {
				t.Fatalf("operators = %v, want %v", out.Operators, tt.want)
			}
			for i, want := range tt.want {
				if out.Operators[i] != want {
					t.Errorf("operator %d = %q, want %q", i, out.Operators[i], want)
				}
				if got := out.Commands[i+1].Operator; got != want {
					t.Errorf("command %d operator = %q, want %q", i+1, got, want)
				}
			}
			if out.Commands[0].Operator != "" {
				t.Errorf("the first command carries no operator, got %q", out.Commands[0].Operator)
			}
		})
	}
}

func TestParsePipeIntoInterpreter(t *testing.T) {
	out := mustParse(t, "curl -sL https://example.com/i.sh | sh")
	if !out.OK() {
		t.Fatalf("a pipe into an interpreter is UNRESOLVED, not a parse failure: %v", out.UnsupportedSummary())
	}
	if len(out.Commands) != 2 {
		t.Fatalf("got %d commands, want 2", len(out.Commands))
	}
	if !out.Commands[0].PipedInto {
		t.Error("curl feeds the next stage, want PipedInto")
	}
	if !out.Commands[1].PipedFrom {
		t.Error("sh reads the previous stage, want PipedFrom")
	}
	if !parser.IsStreamInterpreter(out.Commands[1].Name()) {
		t.Error("sh must be recognized as a stream interpreter")
	}
}

func TestParsePipelineChainMarksEveryStage(t *testing.T) {
	out := mustParse(t, "cat a | grep x | sh")
	if len(out.Commands) != 3 {
		t.Fatalf("got %d commands, want 3", len(out.Commands))
	}
	want := []struct{ into, from bool }{{true, false}, {true, true}, {false, true}}
	for i, expect := range want {
		cmd := out.Commands[i]
		if cmd.PipedInto != expect.into || cmd.PipedFrom != expect.from {
			t.Errorf("command %d (%s): PipedInto=%v PipedFrom=%v, want %v/%v",
				i, cmd.Name(), cmd.PipedInto, cmd.PipedFrom, expect.into, expect.from)
		}
	}
}

func TestParseQuotingAndExpansion(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		wantArg    string
		quoted     bool
		glob       bool
		unexpanded bool
	}{
		{"plain", "rm ./dist", "./dist", false, false, false},
		{"double quoted", `rm "my dir"`, "my dir", true, false, false},
		{"single quoted", `rm 'my dir'`, "my dir", true, false, false},
		{"single quotes suppress expansion", `echo '$HOME'`, "$HOME", true, false, false},
		{"double quotes expand", `echo "$HOME"`, testHome, true, false, false},
		{"tilde", "rm -rf ~", testHome, false, false, false},
		{"tilde path", "rm -rf ~/Documents", testHome + "/Documents", false, false, false},
		{"quoted tilde stays literal", `rm -rf "~"`, "~", true, false, false},
		{"home variable", "rm -rf $HOME/Documents", testHome + "/Documents", false, false, false},
		{"braced home", "rm -rf ${HOME}/Documents", testHome + "/Documents", false, false, false},
		{"pwd", "rm -rf $PWD/dist", testCwd + "/dist", false, false, false},
		{"tmpdir", "rm -rf $TMPDIR/build", testTemp + "/build", false, false, false},
		{"unknown variable", "rm -rf $TARGET/x", "$TARGET/x", false, false, true},
		{"parameter operator", "rm -rf ${TARGET:-/tmp}/x", "${TARGET:-/tmp}/x", false, false, true},
		{"positional parameter", "rm -rf $1", "$1", false, false, true},
		{"glob", "rm -rf build/*", "build/*", false, true, false},
		{"glob question mark", "rm -rf build/?.log", "build/?.log", false, true, false},
		{"glob class", "rm -rf build/[ab].log", "build/[ab].log", false, true, false},
		{"quoted glob is literal", `rm -rf "build/*"`, "build/*", true, false, false},
		{"escaped glob is literal", `rm -rf build/\*`, "build/*", false, false, false},
		{"escaped space", `rm -rf my\ dir`, "my dir", false, false, false},
		{"tilde glob", "rm -rf ~/*", testHome + "/*", false, true, false},
		{"backslash literal in double quotes", `echo "a\b"`, `a\b`, true, false, false},
		{"escaped dollar in double quotes", `echo "\$HOME"`, "$HOME", true, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := mustParse(t, tt.command)
			if !out.OK() {
				t.Fatalf("unexpected unsupported constructs: %v", out.UnsupportedSummary())
			}
			args := out.Commands[0].Args()
			word := args[len(args)-1]
			if word.Text != tt.wantArg {
				t.Errorf("text = %q, want %q", word.Text, tt.wantArg)
			}
			if word.Quoted != tt.quoted {
				t.Errorf("quoted = %v, want %v", word.Quoted, tt.quoted)
			}
			if word.ContainsGlob != tt.glob {
				t.Errorf("contains_glob = %v, want %v", word.ContainsGlob, tt.glob)
			}
			if word.ContainsUnexpandedVar != tt.unexpanded {
				t.Errorf("contains_unexpanded_var = %v, want %v", word.ContainsUnexpandedVar, tt.unexpanded)
			}
		})
	}
}

func TestParseWithoutHomeLeavesTildeLiteral(t *testing.T) {
	out, err := New().Parse(parser.Input{Command: "rm -rf ~/Documents", Cwd: testCwd})
	if err != nil {
		t.Fatalf("Parse returned an internal error: %v", err)
	}
	if got := out.Commands[0].ArgTexts()[1]; got != "~/Documents" {
		t.Errorf("without a home directory the word stays literal, got %q", got)
	}
}

func TestParseEnvAssignments(t *testing.T) {
	out := mustParse(t, "NODE_ENV=production npm run build")
	if !out.OK() {
		t.Fatalf("an env prefix is supported syntax: %v", out.UnsupportedSummary())
	}
	assigns := out.Commands[0].EnvAssignments
	if len(assigns) != 1 || assigns[0].Name != "NODE_ENV" || assigns[0].Value != "production" {
		t.Fatalf("env assignments = %+v, want NODE_ENV=production", assigns)
	}
	if parser.IsDangerousEnvAssignment(assigns[0].Name) {
		t.Error("NODE_ENV does not change what the command resolves to")
	}
}

func TestParseDangerousEnvAssignmentIsRecordedForTheResolver(t *testing.T) {
	// The parser records the prefix; mapping it onto UNRESOLVED is the
	// resolver's job, so this stays supported syntax (§14.2).
	out := mustParse(t, "PATH=/tmp/evil rm -rf ./dist")
	if !out.OK() {
		t.Fatalf("unexpected unsupported constructs: %v", out.UnsupportedSummary())
	}
	assigns := out.Commands[0].EnvAssignments
	if len(assigns) != 1 || assigns[0].Name != "PATH" {
		t.Fatalf("env assignments = %+v, want PATH=…", assigns)
	}
	if !parser.IsDangerousEnvAssignment(assigns[0].Name) {
		t.Error("a PATH override must be detectable as dangerous")
	}
}

func TestParseBareAssignmentIsRefused(t *testing.T) {
	out := mustParse(t, "FOO=bar")
	assertUnsupported(t, out, parser.UnsupportedExport)
}

func TestParseRedirections(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    []parser.Redirection
	}{
		{"truncate", "echo hi > out.txt", []parser.Redirection{{Op: ">", Target: parser.Word{Text: "out.txt"}}}},
		{"append", "echo hi >> log.txt", []parser.Redirection{{Op: ">>", Target: parser.Word{Text: "log.txt"}}}},
		{"read", "wc -l < in.txt", []parser.Redirection{{Op: "<", Target: parser.Word{Text: "in.txt"}}}},
		{"stderr", "make 2> errors.txt", []parser.Redirection{{Op: "2>", Target: parser.Word{Text: "errors.txt"}}}},
		{"all", "make &> out.txt", []parser.Redirection{{Op: "&>", Target: parser.Word{Text: "out.txt"}}}},
		{"clobber", "echo hi >| out.txt", []parser.Redirection{{Op: ">|", Target: parser.Word{Text: "out.txt"}}}},
		{"descriptor duplication dropped", "make 2>&1", nil},
		{"dev null dropped", "make > /dev/null", nil},
		{"dev null with stderr dropped", "make > /dev/null 2>&1", nil},
		{"expanded target", "echo hi > $HOME/out.txt", []parser.Redirection{{Op: ">", Target: parser.Word{Text: testHome + "/out.txt"}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := mustParse(t, tt.command)
			if !out.OK() {
				t.Fatalf("unexpected unsupported constructs: %v", out.UnsupportedSummary())
			}
			got := out.Commands[0].Redirections
			if len(got) != len(tt.want) {
				t.Fatalf("redirections = %+v, want %+v", got, tt.want)
			}
			for i := range tt.want {
				if got[i].Op != tt.want[i].Op || got[i].Target.Text != tt.want[i].Target.Text {
					t.Errorf("redirection %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseHeredocIsNotAFileTarget(t *testing.T) {
	out := mustParse(t, "cat <<EOF\nhello\nEOF\n")
	if !out.OK() {
		t.Fatalf("a here-document is literal input: %v", out.UnsupportedSummary())
	}
	if got := out.Commands[0].Redirections; len(got) != 0 {
		t.Errorf("a here-document has no file target, got %+v", got)
	}
}

func TestParseGroupingRedirectionIsRefused(t *testing.T) {
	out := mustParse(t, "{ git status; git diff; } > out.txt")
	assertUnsupported(t, out, parser.UnsupportedRedirection)
}

func TestParseCdTracking(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		wantCwds []string
		wantFina string
	}{
		{"relative", "cd sub && rm -rf ./dist", []string{testCwd, testCwd + "/sub"}, testCwd + "/sub"},
		{"absolute", "cd /srv/app; rm x", []string{testCwd, "/srv/app"}, "/srv/app"},
		{"home", "cd && git status", []string{testCwd, testHome}, testHome},
		{"tilde", "cd ~ && git status", []string{testCwd, testHome}, testHome},
		{"parent", "cd ..; rm x", []string{testCwd, testCwd + "/.."}, testCwd + "/.."},
		{"double dash", "cd -- sub; rm x", []string{testCwd, testCwd + "/sub"}, testCwd + "/sub"},
		{"chained", "cd a; cd b; rm x", []string{testCwd, testCwd + "/a", testCwd + "/a/b"}, testCwd + "/a/b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := mustParse(t, tt.command)
			if !out.OK() {
				t.Fatalf("unexpected unsupported constructs: %v", out.UnsupportedSummary())
			}
			if len(out.Commands) != len(tt.wantCwds) {
				t.Fatalf("got %d commands, want %d", len(out.Commands), len(tt.wantCwds))
			}
			for i, want := range tt.wantCwds {
				if got := out.Commands[i].EffectiveCwd; got != want {
					t.Errorf("command %d effective cwd = %q, want %q", i, got, want)
				}
			}
			if out.FinalCwd != tt.wantFina {
				t.Errorf("final cwd = %q, want %q", out.FinalCwd, tt.wantFina)
			}
			if !out.Commands[0].Builtin {
				t.Error("cd is a parser builtin")
			}
		})
	}
}

func TestParseCdDashIsRefused(t *testing.T) {
	out := mustParse(t, "cd - && rm -rf ./dist")
	assertUnsupported(t, out, parser.UnsupportedCdDash)
	if out.FinalCwd != testCwd {
		t.Errorf("an unsupported cd leaves the cwd untouched, got %q", out.FinalCwd)
	}
}

func TestParseUnsupportedConstructs(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    parser.UnsupportedKind
	}{
		{"command substitution", "rm -rf $(cat target.txt)", parser.UnsupportedCommandSubstitution},
		{"backticks", "rm -rf `cat target.txt`", parser.UnsupportedCommandSubstitution},
		{"process substitution", "diff <(ls a) <(ls b)", parser.UnsupportedProcessSubstitution},
		{"arithmetic expansion", "echo $((1 + 1))", parser.UnsupportedArithmetic},
		{"arithmetic command", "((x = 1))", parser.UnsupportedArithmetic},
		{"let", "let x=1", parser.UnsupportedArithmetic},
		{"for loop", "for f in *; do rm $f; done", parser.UnsupportedControlFlow},
		{"while loop", "while true; do rm x; done", parser.UnsupportedControlFlow},
		{"if", "if true; then rm x; fi", parser.UnsupportedControlFlow},
		{"case", "case $x in a) rm y;; esac", parser.UnsupportedControlFlow},
		{"function", "cleanup() { rm -rf ./dist; }", parser.UnsupportedFunction},
		{"eval", `eval "rm -rf ~"`, parser.UnsupportedEval},
		{"source", "source ./env.sh", parser.UnsupportedSource},
		{"dot source", ". ./env.sh", parser.UnsupportedSource},
		{"exec", "exec rm -rf ./dist", parser.UnsupportedExec},
		{"xargs", "ls | xargs rm", parser.UnsupportedXargs},
		{"alias", "alias rm='rm -rf'", parser.UnsupportedAlias},
		{"export", "export PATH=/tmp", parser.UnsupportedExport},
		{"unset", "unset PATH", parser.UnsupportedExport},
		{"declare", "declare -x FOO=bar", parser.UnsupportedExport},
		{"pushd", "pushd /tmp", parser.UnsupportedDirStack},
		{"popd", "popd", parser.UnsupportedDirStack},
		{"trap", "trap cleanup EXIT", parser.UnsupportedTrap},
		{"background", "npm run dev &", parser.UnsupportedBackground},
		{"sh -c", `sh -c "rm -rf ~"`, parser.UnsupportedShellWrapper},
		{"bash -c", `bash -c "rm -rf ~"`, parser.UnsupportedShellWrapper},
		{"node -e", `node -e "require('fs').rmSync('/', {recursive:true})"`, parser.UnsupportedShellWrapper},
		{"sudo", "sudo rm -rf /", parser.UnsupportedElevation},
		{"doas", "doas rm -rf /", parser.UnsupportedElevation},
		{"su", "su -c 'rm -rf /'", parser.UnsupportedElevation},
		{"syntax error", "echo )", parser.UnsupportedSyntaxError},
		{"other shell variant", "((# 1))", parser.UnsupportedSyntaxError},
		{"extended test", "[[ -f x ]]", parser.UnsupportedNode},
		{"time", "time npm test", parser.UnsupportedNode},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := mustParse(t, tt.command)
			assertUnsupported(t, out, tt.want)
			if out.OK() {
				t.Error("an unsupported construct makes the action PARSE_FAILED (I-2)")
			}
		})
	}
}

func TestParseElevationKeepsTheInnerCommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		wantCmd []string
	}{
		{"plain", "sudo rm -rf /", []string{"rm", "-rf", "/"}},
		{"boolean options", "sudo -n -E rm -rf /", []string{"rm", "-rf", "/"}},
		{"user option", "sudo -u root rm -rf /", []string{"rm", "-rf", "/"}},
		{"long option with value", "sudo --user=root rm -rf /", []string{"rm", "-rf", "/"}},
		{"end of options", "sudo -- rm -rf /", []string{"rm", "-rf", "/"}},
		{"doas", "doas rm -rf /etc", []string{"rm", "-rf", "/etc"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := mustParse(t, tt.command)
			if len(out.Commands) != 1 {
				t.Fatalf("got %d commands, want 1", len(out.Commands))
			}
			cmd := out.Commands[0]
			if !cmd.Elevated {
				t.Error("an elevated command must carry the elevated flag")
			}
			got := append([]string{cmd.Name()}, cmd.ArgTexts()...)
			if strings.Join(got, "\x00") != strings.Join(tt.wantCmd, "\x00") {
				t.Errorf("inner command = %v, want %v", got, tt.wantCmd)
			}
		})
	}
}

func TestParseElevationWithoutInnerCommand(t *testing.T) {
	out := mustParse(t, "sudo -l")
	assertUnsupported(t, out, parser.UnsupportedElevation)
	if len(out.Commands) != 1 || out.Commands[0].Name() != "sudo" {
		t.Fatalf("commands = %+v, want the sudo invocation itself", out.Commands)
	}
	if !out.Commands[0].Elevated {
		t.Error("want the elevated flag")
	}
}

func TestParseBareInterpreterIsNotAShellWrapper(t *testing.T) {
	// `node --test` and `bash script.sh` do not hide a payload in an argument;
	// they resolve (or fail to) like any other executable.
	for _, command := range []string{"node --test", "bash ./script.sh"} {
		out := mustParse(t, command)
		if !out.OK() {
			t.Errorf("%q: unexpected unsupported constructs: %v", command, out.UnsupportedSummary())
		}
	}
}

func TestParseHardRulesStillSeeParsedCommands(t *testing.T) {
	// I-2: refused syntax caps the decision at ASK, but whatever parsed is
	// still handed to the policy engine.
	out := mustParse(t, "sudo rm -rf ~")
	if out.OK() {
		t.Fatal("elevation is refused syntax")
	}
	if len(out.Commands) != 1 {
		t.Fatalf("got %d commands, want the inner rm", len(out.Commands))
	}
	if got := out.Commands[0].ArgTexts(); got[len(got)-1] != testHome {
		t.Errorf("inner target = %q, want %q", got[len(got)-1], testHome)
	}
}

func TestParseCommandTooLong(t *testing.T) {
	out := mustParse(t, "echo "+strings.Repeat("a", parser.MaxCommandBytes))
	assertUnsupported(t, out, parser.UnsupportedCommandTooLong)
	if len(out.Commands) != 0 {
		t.Errorf("an over-long command is not parsed, got %d commands", len(out.Commands))
	}
}

func TestParseTooManyCommands(t *testing.T) {
	// The line is refused as too long to approve, but every command is still
	// emitted: a delete padded past the cap must reach the hard rules (§15.1).
	out := mustParse(t, strings.Repeat("echo x; ", parser.MaxSimpleCommands+8)+"rm -rf ~/Documents")
	assertUnsupported(t, out, parser.UnsupportedTooManyCommands)
	if len(out.Commands) != parser.MaxSimpleCommands+9 {
		t.Errorf("got %d commands, want all %d emitted", len(out.Commands), parser.MaxSimpleCommands+9)
	}
	if last := out.Commands[len(out.Commands)-1]; last.Name() != "rm" {
		t.Errorf("the command past the cap must still be emitted, got %q", last.Name())
	}
	if len(out.Operators) != len(out.Commands)-1 {
		t.Errorf("operators = %d, want %d", len(out.Operators), len(out.Commands)-1)
	}
}

func TestParseSurvivesShellGrammarPanic(t *testing.T) {
	// mvdan.cc/sh v3.8.0 panics on this input; the gate must still answer.
	out := mustParse(t, "`$\\\x00")
	assertUnsupported(t, out, parser.UnsupportedSyntaxError)
	if len(out.Commands) != 0 {
		t.Errorf("a rejected command line yields no commands, got %d", len(out.Commands))
	}
}

func TestParseSyntaxErrorYieldsNoCommands(t *testing.T) {
	out := mustParse(t, "rm -rf 'unterminated")
	assertUnsupported(t, out, parser.UnsupportedSyntaxError)
	if len(out.Commands) != 0 {
		t.Errorf("a syntax error yields no commands, got %d", len(out.Commands))
	}
}

func TestParseRawTextIsTheOriginalSource(t *testing.T) {
	out := mustParse(t, "git status && rm -rf ./dist")
	want := []string{"git status", "rm -rf ./dist"}
	for i, expect := range want {
		if got := out.Commands[i].RawText; got != expect {
			t.Errorf("command %d raw text = %q, want %q", i, got, expect)
		}
	}
}

func TestParseDialectName(t *testing.T) {
	if got := New().Name(); got != action.DialectPosix {
		t.Errorf("Name() = %q, want %q", got, action.DialectPosix)
	}
	registry := parser.NewRegistry()
	registry.Register(New())
	if _, err := registry.Get(action.DialectPosix); err != nil {
		t.Errorf("the dialect must be registrable: %v", err)
	}
}

func TestParseEmptyCommand(t *testing.T) {
	for _, command := range []string{"", "   ", "\n", "# only a comment"} {
		out := mustParse(t, command)
		if !out.OK() {
			t.Errorf("%q: unexpected unsupported constructs: %v", command, out.UnsupportedSummary())
		}
		if len(out.Commands) != 0 {
			t.Errorf("%q: got %d commands, want 0", command, len(out.Commands))
		}
	}
}

// assertUnsupported fails unless the parse reported the given refusal kind.
func assertUnsupported(t *testing.T, out *parser.ParsedCommand, want parser.UnsupportedKind) {
	t.Helper()
	for _, item := range out.Unsupported {
		if item.Kind == want {
			return
		}
	}
	t.Errorf("unsupported = %v, want a %q entry", out.UnsupportedSummary(), want)
}

func FuzzParse(f *testing.F) {
	seeds := []string{
		"rm -rf ./dist",
		"git status && rm -rf ~",
		"npm run cleanup",
		"curl -sL https://example.com/i.sh | sh",
		"cd sub && rm -rf $HOME/Documents",
		"FOO=bar rm -rf 'my dir' > out.txt 2>&1",
		"for f in *; do rm $f; done",
		"sudo -u root rm -rf /",
		`eval "$(cat x)"`,
		"echo )",
		"cat <<EOF\nhi\nEOF\n",
		"{ a; b; } | c",
		"rm -rf ${TARGET:-/tmp}/*",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	dialect := New()
	f.Fuzz(func(t *testing.T, command string) {
		out, err := dialect.Parse(testInput(command))
		if err != nil {
			t.Fatalf("Parse must not fail internally, got %v", err)
		}
		if out == nil {
			t.Fatal("Parse must always return a result")
		}
		if out.Dialect != action.DialectPosix {
			t.Errorf("dialect = %q, want %q", out.Dialect, action.DialectPosix)
		}
		if len(out.Commands) > parser.MaxParsedCommands {
			t.Errorf("got %d commands, want at most %d", len(out.Commands), parser.MaxParsedCommands)
		}
		if len(out.Commands) > parser.MaxSimpleCommands && out.OK() {
			t.Errorf("%d commands exceed the approvable cap and must be refused", len(out.Commands))
		}
		if want := max(0, len(out.Commands)-1); len(out.Operators) != want {
			t.Errorf("operators = %d, want %d for %d commands", len(out.Operators), want, len(out.Commands))
		}
		for i, cmd := range out.Commands {
			if cmd.Name() == "" {
				t.Errorf("command %d has no executable name", i)
			}
			if (i == 0) != (cmd.Operator == "") {
				t.Errorf("command %d operator = %q; only the first command has none", i, cmd.Operator)
			}
		}
	})
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
