package powershell

import (
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/parser"
)

const (
	testHome = `C:\Users\u`
	testCwd  = `C:\w\demo`
	testTemp = `C:\Temp`
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

func assertUnsupported(t *testing.T, out *parser.ParsedCommand, want parser.UnsupportedKind) {
	t.Helper()
	for _, item := range out.Unsupported {
		if item.Kind == want {
			return
		}
	}
	t.Errorf("unsupported = %v, want a %q entry", out.UnsupportedSummary(), want)
}

func TestParseSimpleCmdlets(t *testing.T) {
	tests := []struct {
		name    string
		command string
		argv    [][]string
	}{
		{"remove", "Remove-Item -Recurse -Force ./dist",
			[][]string{{"Remove-Item", "-Recurse", "-Force", "./dist"}}},
		{"alias resolves to the cmdlet", "rm -Recurse ./dist",
			[][]string{{"Remove-Item", "-Recurse", "./dist"}}},
		{"del alias", "del ./dist", [][]string{{"Remove-Item", "./dist"}}},
		{"sequence", "Get-ChildItem; Get-Content README.md",
			[][]string{{"Get-ChildItem"}, {"Get-Content", "README.md"}}},
		{"conditional", "npm run build && npm test",
			[][]string{{"npm", "run", "build"}, {"npm", "test"}}},
		{"pipeline", "Get-ChildItem | Select-String demo",
			[][]string{{"Get-ChildItem"}, {"Select-String", "demo"}}},
		{"comment stripped", "Remove-Item ./dist # clean up",
			[][]string{{"Remove-Item", "./dist"}}},
		{"call operator", `& "C:\tools\thing.exe" --flag`,
			[][]string{{`C:\tools\thing.exe`, "--flag"}}},
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

func TestParseQuotingAndExpansion(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		wantArg    string
		quoted     bool
		unexpanded bool
	}{
		{"double quotes expand", `Remove-Item "$env:USERPROFILE\Documents"`,
			testHome + `\Documents`, true, false},
		{"single quotes do not expand", `Remove-Item '$env:USERPROFILE\Documents'`,
			`$env:USERPROFILE\Documents`, true, false},
		{"home variable", `Remove-Item $HOME\Documents`, testHome + `\Documents`, false, false},
		{"tilde", "Remove-Item ~/Documents", testHome + "/Documents", false, false},
		{"temp variable", `Remove-Item $env:TEMP\build`, testTemp + `\build`, false, false},
		{"pwd", `Remove-Item $PWD\dist`, testCwd + `\dist`, false, false},
		{"unknown variable", `Remove-Item $env:TARGET\x`, `$env:TARGET\x`, false, true},
		{"backtick escape", "Remove-Item my`$file", "my$file", false, false},
		{"quoted path with spaces", `Remove-Item "my dir"`, "my dir", true, false},
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
			if word.ContainsUnexpandedVar != tt.unexpanded {
				t.Errorf("contains_unexpanded_var = %v, want %v", word.ContainsUnexpandedVar, tt.unexpanded)
			}
		})
	}
}

func TestParseRedirections(t *testing.T) {
	tests := []struct {
		name    string
		command string
		op      string
		target  string
	}{
		{"truncate", "Get-ChildItem > out.txt", ">", "out.txt"},
		{"append", "Get-ChildItem >> log.txt", ">>", "log.txt"},
		{"stderr", "npm test 2> errors.txt", "2>", "errors.txt"},
		{"all streams", "npm test *> all.txt", "*>", "all.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := mustParse(t, tt.command)
			redirections := out.Commands[0].Redirections
			if len(redirections) != 1 {
				t.Fatalf("redirections = %+v, want one", redirections)
			}
			if redirections[0].Op != tt.op || redirections[0].Target.Text != tt.target {
				t.Errorf("redirection = %+v, want %s %s", redirections[0], tt.op, tt.target)
			}
		})
	}

	dropped := mustParse(t, "npm test 2>&1")
	if got := dropped.Commands[0].Redirections; len(got) != 0 {
		t.Errorf("a stream duplication has no file target, got %+v", got)
	}
	ignored := mustParse(t, "npm test > $null")
	if got := ignored.Commands[0].Redirections; len(got) != 0 {
		t.Errorf("$null is an ignored device, got %+v", got)
	}
}

// TestParseRedirectionWithoutWhitespaceIsStillModeled is the AG-122
// regression (same root cause as the cmd.exe dialect): PowerShell treats
// `<`/`>` as redirection metacharacters regardless of surrounding
// whitespace, so `Write-Output hi>out.txt` redirects exactly like
// `Write-Output hi > out.txt`. The tokenizer used to split only on blanks,
// so an unspaced redirection vanished into the previous argument and
// applyRedirections never saw the write — an unmodeled effect a resolved
// command would sail straight through the read-only baseline with (zero
// prompts, matching the AG-01 bypass class).
func TestParseRedirectionWithoutWhitespaceIsStillModeled(t *testing.T) {
	tests := []struct {
		name    string
		command string
		wantArg string
		wantOp  string
		wantTgt string
	}{
		{"no space either side", `Write-Output hi>out.txt`, "hi", ">", "out.txt"},
		{"space only before", `Write-Output hi >out.txt`, "hi", ">", "out.txt"},
		{"space only after", `Write-Output hi> out.txt`, "hi", ">", "out.txt"},
		{"append, no space", `Write-Output hi>>out.txt`, "hi", ">>", "out.txt"},
		{"append into a sensitive-looking target", `Write-Output hi>>C:\Users\u\.ssh\authorized_keys`, "hi", ">>", `C:\Users\u\.ssh\authorized_keys`},
		{"input redirection, no space", `Get-Content<in.txt`, "", "<", "in.txt"},
		{"handle-numbered write, no space", `Get-Content 5>C:\err.log`, "", "5>", `C:\err.log`},
		{"all-streams write, no space", `Get-Content *>C:\all.log`, "", "*>", `C:\all.log`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := mustParse(t, tt.command)
			if len(out.Commands) != 1 {
				t.Fatalf("got %d commands, want 1: %v", len(out.Commands), out.Commands)
			}
			cmd := out.Commands[0]
			gotArgs := strings.Join(cmd.ArgTexts(), " ")
			if gotArgs != tt.wantArg {
				t.Errorf("args = %q, want %q — the redirection target must not stay glued to the previous argument", gotArgs, tt.wantArg)
			}
			redirections := cmd.Redirections
			if len(redirections) != 1 {
				t.Fatalf("redirections = %+v, want exactly one — the write/read must not be swallowed into an argument", redirections)
			}
			if redirections[0].Op != tt.wantOp || redirections[0].Target.Text != tt.wantTgt {
				t.Errorf("redirection = %+v, want %s %s", redirections[0], tt.wantOp, tt.wantTgt)
			}
		})
	}
}

// TestParseRedirectionInsideQuotesStaysLiteral guards the fix above: a
// quoted `>` is an ordinary character, not a redirection, whether the quotes
// are single or double.
func TestParseRedirectionInsideQuotesStaysLiteral(t *testing.T) {
	for _, command := range []string{`Write-Output "a>b"`, `Write-Output 'a>b'`} {
		out := mustParse(t, command)
		if len(out.Commands[0].Redirections) != 0 {
			t.Errorf("%q: redirections = %+v, want none — the quoted `>` is literal", command, out.Commands[0].Redirections)
		}
		if got := strings.Join(out.Commands[0].ArgTexts(), " "); got != "a>b" {
			t.Errorf("%q: args = %q, want %q", command, got, "a>b")
		}
	}
}

func TestParseLocationTracking(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		wantCwds []string
		final    string
	}{
		{"relative", `Set-Location sub; Remove-Item ./dist`,
			[]string{testCwd, testCwd + `\sub`}, testCwd + `\sub`},
		{"alias", `cd sub; Remove-Item ./dist`,
			[]string{testCwd, testCwd + `\sub`}, testCwd + `\sub`},
		{"absolute", `Set-Location C:\other; Remove-Item x`,
			[]string{testCwd, `C:\other`}, `C:\other`},
		{"home", `Set-Location; Get-ChildItem`,
			[]string{testCwd, testHome}, testHome},
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
					t.Errorf("command %d cwd = %q, want %q", i, got, want)
				}
			}
			if out.FinalCwd != tt.final {
				t.Errorf("final cwd = %q, want %q", out.FinalCwd, tt.final)
			}
		})
	}
}

func TestParseRefusesWhatItWillNotInterpret(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    parser.UnsupportedKind
	}{
		{"script block", `Get-ChildItem | ForEach-Object { Remove-Item $_ }`, parser.UnsupportedScriptBlock},
		{"subexpression", `Remove-Item $(Get-Content target.txt)`, parser.UnsupportedCommandSubstitution},
		{"array subexpression", `Remove-Item @(Get-Content list.txt)`, parser.UnsupportedCommandSubstitution},
		{"invoke expression", `Invoke-Expression "Remove-Item ~"`, parser.UnsupportedInvokeExpression},
		{"iex alias", `iex $payload`, parser.UnsupportedInvokeExpression},
		{"start process", `Start-Process powershell`, parser.UnsupportedStartProcess},
		{"command flag", `powershell -Command "Remove-Item ~"`, parser.UnsupportedShellWrapper},
		{"pwsh command flag", `pwsh -c "Remove-Item ~"`, parser.UnsupportedShellWrapper},
		{"alias definition", `Set-Alias rm Remove-Item`, parser.UnsupportedAlias},
		{"variable assignment", `Set-Variable -Name x -Value 1`, parser.UnsupportedExport},
		{"directory stack", `Push-Location C:\other`, parser.UnsupportedDirStack},
		{"background job", `npm run dev &`, parser.UnsupportedBackground},
		{"elevation", `runas /user:admin cmd`, parser.UnsupportedElevation},
		{"unterminated quote", `Remove-Item "unterminated`, parser.UnsupportedSyntaxError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := mustParse(t, tt.command)
			assertUnsupported(t, out, tt.want)
			if out.OK() {
				t.Error("a refused construct makes the action PARSE_FAILED (I-2)")
			}
		})
	}
}

func TestParsePipelineMarking(t *testing.T) {
	out := mustParse(t, "Get-Content install.ps1 | powershell")
	if len(out.Commands) != 2 {
		t.Fatalf("got %d commands, want 2", len(out.Commands))
	}
	if !out.Commands[0].PipedInto {
		t.Error("the first stage feeds the second")
	}
	if !out.Commands[1].PipedFrom {
		t.Error("the second stage reads the first")
	}
	if !parser.IsStreamInterpreter(out.Commands[1].Name()) {
		t.Error("powershell must be recognized as a stream interpreter")
	}
}

func TestParseDialectName(t *testing.T) {
	if got := New().Name(); got != action.DialectPowerShell {
		t.Errorf("Name() = %q, want %q", got, action.DialectPowerShell)
	}
	registry := parser.NewRegistry()
	registry.Register(New())
	if _, err := registry.Get(action.DialectPowerShell); err != nil {
		t.Errorf("the dialect must be registrable: %v", err)
	}
}

func TestParseEmptyCommand(t *testing.T) {
	for _, command := range []string{"", "   ", "# only a comment"} {
		out := mustParse(t, command)
		if !out.OK() {
			t.Errorf("%q: unexpected unsupported constructs: %v", command, out.UnsupportedSummary())
		}
		if len(out.Commands) != 0 {
			t.Errorf("%q: got %d commands, want 0", command, len(out.Commands))
		}
	}
}

func TestParseCommandTooLong(t *testing.T) {
	out := mustParse(t, "Get-ChildItem "+strings.Repeat("a", parser.MaxCommandBytes))
	assertUnsupported(t, out, parser.UnsupportedCommandTooLong)
}

func TestParseTooManyCommands(t *testing.T) {
	// Refused as too long to approve, but every command is still emitted so the
	// hard rules see a delete padded past the cap (§15.1).
	out := mustParse(t, strings.Repeat("Get-ChildItem; ", parser.MaxSimpleCommands+8)+"Remove-Item -Recurse ~/Documents")
	assertUnsupported(t, out, parser.UnsupportedTooManyCommands)
	if len(out.Commands) != parser.MaxSimpleCommands+9 {
		t.Errorf("got %d commands, want all %d emitted", len(out.Commands), parser.MaxSimpleCommands+9)
	}
	if last := out.Commands[len(out.Commands)-1]; last.Name() != "Remove-Item" {
		t.Errorf("the command past the cap must still be emitted, got %q", last.Name())
	}
}

func FuzzParse(f *testing.F) {
	seeds := []string{
		`Remove-Item -Recurse -Force ./dist`,
		`rm "$env:USERPROFILE\Documents"`,
		`Get-ChildItem | Select-String demo`,
		`Set-Location sub; Remove-Item ./dist`,
		`Invoke-Expression "x"`,
		`Get-ChildItem | ForEach-Object { Remove-Item $_ }`,
		`Remove-Item "unterminated`,
		"npm run dev &",
		`& "C:\t.exe" --flag`,
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
		if len(out.Commands) > parser.MaxParsedCommands {
			t.Errorf("got %d commands, want at most %d", len(out.Commands), parser.MaxParsedCommands)
		}
		if len(out.Commands) > parser.MaxSimpleCommands && out.OK() {
			t.Errorf("%d commands exceed the approvable cap and must be refused", len(out.Commands))
		}
		if want := len(out.Commands) - 1; want > 0 && len(out.Operators) != want {
			t.Errorf("operators = %d, want %d for %d commands",
				len(out.Operators), want, len(out.Commands))
		}
		for i, cmd := range out.Commands {
			if cmd.Name() == "" {
				t.Errorf("command %d has no executable name", i)
			}
		}
	})
}
