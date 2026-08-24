package cmd

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

func TestParseSimpleCommands(t *testing.T) {
	tests := []struct {
		name    string
		command string
		argv    [][]string
	}{
		{"delete", `del /q dist\bundle.js`, [][]string{{"del", "/q", `dist\bundle.js`}}},
		{"remove directory", `rd /s /q dist`, [][]string{{"rd", "/s", "/q", "dist"}}},
		{"ampersand sequences", `echo one & echo two`,
			[][]string{{"echo", "one"}, {"echo", "two"}}},
		{"conditional", `npm run build && npm test`,
			[][]string{{"npm", "run", "build"}, {"npm", "test"}}},
		{"or", `npm test || echo failed`,
			[][]string{{"npm", "test"}, {"echo", "failed"}}},
		{"pipeline", `type log.txt | findstr error`,
			[][]string{{"type", "log.txt"}, {"findstr", "error"}}},
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

func TestParseVariableExpansion(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		wantArg    string
		unexpanded bool
	}{
		{"user profile", `rd /s /q %USERPROFILE%\Documents`, testHome + `\Documents`, false},
		{"home drive and path", `rd /s /q %HOMEDRIVE%%HOMEPATH%\Documents`, testHome + `\Documents`, false},
		{"temp", `del %TEMP%\build.log`, testTemp + `\build.log`, false},
		{"tmp", `del %TMP%\build.log`, testTemp + `\build.log`, false},
		{"current directory", `del %CD%\out.txt`, testCwd + `\out.txt`, false},
		{"unknown variable", `del %TARGET%\x`, `%TARGET%\x`, true},
		{"expansion inside quotes", `del "%TEMP%\my file.log"`, testTemp + `\my file.log`, false},
		{"case insensitive", `rd /s /q %userprofile%\Documents`, testHome + `\Documents`, false},
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
			if word.ContainsUnexpandedVar != tt.unexpanded {
				t.Errorf("contains_unexpanded_var = %v, want %v", word.ContainsUnexpandedVar, tt.unexpanded)
			}
		})
	}
}

func TestParseQuotingAndEscapes(t *testing.T) {
	tests := []struct {
		name    string
		command string
		wantArg string
		quoted  bool
	}{
		{"quoted path", `del "my dir\file.txt"`, `my dir\file.txt`, true},
		{"caret escape", `echo a^&b`, "a&b", false},
		{"caret escapes a pipe", `echo a^|b`, "a|b", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := mustParse(t, tt.command)
			if !out.OK() {
				t.Fatalf("unexpected unsupported constructs: %v", out.UnsupportedSummary())
			}
			if len(out.Commands) != 1 {
				t.Fatalf("got %d commands, want 1 (the escape must not split the line)", len(out.Commands))
			}
			args := out.Commands[0].Args()
			word := args[len(args)-1]
			if word.Text != tt.wantArg {
				t.Errorf("text = %q, want %q", word.Text, tt.wantArg)
			}
			if word.Quoted != tt.quoted {
				t.Errorf("quoted = %v, want %v", word.Quoted, tt.quoted)
			}
		})
	}
}

// TestParseCaretHasNoEffectInsideQuotes is the AG-120 regression: cmd.exe does
// not treat `^` as an escape character inside a double-quoted string — a
// literal caret there does nothing special, and the following `"` still
// closes the string. Escaping it unconditionally (as the parser used to)
// makes the parser believe the string is still open past that point, so a
// `&`/`|` that cmd.exe treats as a real command separator gets folded into the
// first command's argument instead — hiding whatever runs after it.
func TestParseCaretHasNoEffectInsideQuotes(t *testing.T) {
	tests := []struct {
		name    string
		command string
		argv    [][]string
	}{
		{
			"caret before the closing quote does not keep the string open",
			`echo "a^"&calc.exe`,
			[][]string{{"echo", `a^`}, {"calc.exe"}},
		},
		{
			"caret before the closing quote does not hide a pipe",
			`echo "a^"|findstr x`,
			[][]string{{"echo", `a^`}, {"findstr", "x"}},
		},
		{
			"a caret still escapes outside quotes even right after a quoted argument",
			`echo "a"^&b`,
			[][]string{{"echo", "a&b"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := mustParse(t, tt.command)
			if !out.OK() {
				t.Fatalf("unexpected unsupported constructs: %v", out.UnsupportedSummary())
			}
			if len(out.Commands) != len(tt.argv) {
				t.Fatalf("got %d commands (%v), want %d — the second command must not be swallowed into the first",
					len(out.Commands), out.Commands, len(tt.argv))
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

func TestParseRedirections(t *testing.T) {
	out := mustParse(t, `npm test > out.txt`)
	redirections := out.Commands[0].Redirections
	if len(redirections) != 1 || redirections[0].Op != ">" || redirections[0].Target.Text != "out.txt" {
		t.Fatalf("redirections = %+v", redirections)
	}

	nul := mustParse(t, `npm test > NUL`)
	if got := nul.Commands[0].Redirections; len(got) != 0 {
		t.Errorf("NUL is an ignored device, got %+v", got)
	}
	duplication := mustParse(t, `npm test 2>&1`)
	if len(duplication.Commands) != 1 || duplication.Commands[0].Name() != "npm" {
		t.Fatalf("2>&1 must not split off or drop the command, got %d commands: %v", len(duplication.Commands), duplication.Commands)
	}
	if got := duplication.Commands[0].Redirections; len(got) != 0 {
		t.Errorf("a descriptor duplication has no file target, got %+v", got)
	}
}

// TestParseRedirectionWithoutWhitespaceIsStillModeled is the AG-122
// regression: cmd.exe treats `<`/`>` as metacharacters regardless of
// surrounding whitespace, so `echo hi>out.txt` redirects exactly like
// `echo hi > out.txt`. The tokenizer used to split only on blanks, so any
// unspaced redirection vanished into the previous word and applyRedirections
// never saw the write/read — an unmodeled effect that a NOOP command like
// `echo` would sail straight through the read-only baseline with (zero
// prompts, matching the AG-01 bypass class).
func TestParseRedirectionWithoutWhitespaceIsStillModeled(t *testing.T) {
	tests := []struct {
		name    string
		command string
		wantOp  string
		wantArg string
		wantTgt string
	}{
		{"no space either side", `echo hi>out.txt`, "echo", "hi", "out.txt"},
		{"space only before", `echo hi >out.txt`, "echo", "hi", "out.txt"},
		{"space only after", `echo hi> out.txt`, "echo", "hi", "out.txt"},
		{"append, no space", `echo hi>>out.txt`, "echo", "hi", "out.txt"},
		{"append into a sensitive-looking target", `echo hi>>C:\Users\Public\.ssh\authorized_keys`, "echo", "hi", `C:\Users\Public\.ssh\authorized_keys`},
		{"input redirection, no space", `type<in.txt`, "type", "", "in.txt"},
		{"handle-numbered write, no space", `dir 2>C:\err.log`, "dir", "", `C:\err.log`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := mustParse(t, tt.command)
			if len(out.Commands) != 1 {
				t.Fatalf("got %d commands, want 1: %v", len(out.Commands), out.Commands)
			}
			cmd := out.Commands[0]
			if got := strings.ToLower(cmd.Name()); got != strings.ToLower(tt.wantOp) {
				t.Errorf("command name = %q, want %q", cmd.Name(), tt.wantOp)
			}
			gotArgs := strings.Join(cmd.ArgTexts(), " ")
			if gotArgs != tt.wantArg {
				t.Errorf("args = %q, want %q — the redirection target must not stay glued to the previous argument", gotArgs, tt.wantArg)
			}
			redirections := cmd.Redirections
			if len(redirections) != 1 {
				t.Fatalf("redirections = %+v, want exactly one — the write/read must not be swallowed into an argument", redirections)
			}
			if got := redirections[0].Target.Text; got != tt.wantTgt {
				t.Errorf("redirection target = %q, want %q", got, tt.wantTgt)
			}
		})
	}
}

// TestParseRedirectionDuplicationDoesNotSequenceANewCommand is the other
// half of AG-122: `2>&1` duplicates a stream handle, it does not sequence a
// new command the way a bare `&` does. Misreading the `&` in `2>&1` as the
// sequence operator dropped the preceding command from the parsed output
// entirely (its dangling "2>" failed as an unterminated redirect) and
// resurfaced "1" as an unrelated bare command name — so a delete padded with
// `2>&1`, a nearly universal batch-script idiom for merging stderr into
// stdout, was never seen by the hard rules at all.
func TestParseRedirectionDuplicationDoesNotSequenceANewCommand(t *testing.T) {
	out := mustParse(t, `rd /s /q dist 2>&1`)
	if len(out.Commands) != 1 {
		t.Fatalf("got %d commands, want 1 (the delete must not vanish): %v", len(out.Commands), out.Commands)
	}
	if got := strings.ToLower(out.Commands[0].Name()); got != "rd" {
		t.Errorf("command name = %q, want %q — the delete must still be the command Intenter sees", out.Commands[0].Name(), "rd")
	}

	chained := mustParse(t, `dir 2>&1&calc.exe`)
	if len(chained.Commands) != 2 {
		t.Fatalf("got %d commands, want 2 (dir, then calc.exe): %v", len(chained.Commands), chained.Commands)
	}
	if got := strings.ToLower(chained.Commands[0].Name()); got != "dir" {
		t.Errorf("command 0 = %q, want %q", chained.Commands[0].Name(), "dir")
	}
	if got := strings.ToLower(chained.Commands[1].Name()); got != "calc.exe" {
		t.Errorf("command 1 = %q, want %q", chained.Commands[1].Name(), "calc.exe")
	}
}

func TestParseDirectoryTracking(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		wantCwds []string
		final    string
	}{
		{"relative", `cd sub & del x`, []string{testCwd, testCwd + `\sub`}, testCwd + `\sub`},
		{"absolute", `cd C:\other & del x`, []string{testCwd, `C:\other`}, `C:\other`},
		{"drive switch flag", `cd /d C:\other & del x`, []string{testCwd, `C:\other`}, `C:\other`},
		{"bare cd does not move", `cd & del x`, []string{testCwd, testCwd}, testCwd},
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

func TestParseCommentsAreStripped(t *testing.T) {
	for _, command := range []string{"REM this is a comment", ":: also a comment", "rem lowercase"} {
		out := mustParse(t, command)
		if len(out.Commands) != 0 {
			t.Errorf("%q: got %d commands, want none", command, len(out.Commands))
		}
	}
}

func TestParseRefusesWhatItWillNotInterpret(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    parser.UnsupportedKind
	}{
		{"shell wrapper", `cmd /c "rd /s /q %USERPROFILE%"`, parser.UnsupportedShellWrapper},
		{"call", `call other.bat`, parser.UnsupportedShellWrapper},
		{"start", `start notepad`, parser.UnsupportedStartProcess},
		{"set", `set PATH=C:\evil`, parser.UnsupportedExport},
		{"setx", `setx PATH C:\evil`, parser.UnsupportedExport},
		{"for loop", `for %%f in (*) do del %%f`, parser.UnsupportedControlFlow},
		{"if", `if exist x del x`, parser.UnsupportedControlFlow},
		{"pushd", `pushd C:\other`, parser.UnsupportedDirStack},
		{"unterminated quote", `del "unterminated`, parser.UnsupportedSyntaxError},
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
	out := mustParse(t, `type install.ps1 | powershell`)
	if len(out.Commands) != 2 {
		t.Fatalf("got %d commands, want 2", len(out.Commands))
	}
	if !out.Commands[0].PipedInto {
		t.Error("the first stage feeds the second")
	}
	if !out.Commands[1].PipedFrom {
		t.Error("the second stage reads the first")
	}
}

func TestParseDialectName(t *testing.T) {
	if got := New().Name(); got != action.DialectCmd {
		t.Errorf("Name() = %q, want %q", got, action.DialectCmd)
	}
	registry := parser.NewRegistry()
	registry.Register(New())
	if _, err := registry.Get(action.DialectCmd); err != nil {
		t.Errorf("the dialect must be registrable: %v", err)
	}
}

func TestParseEmptyCommand(t *testing.T) {
	for _, command := range []string{"", "   ", "REM comment"} {
		out := mustParse(t, command)
		if len(out.Commands) != 0 {
			t.Errorf("%q: got %d commands, want 0", command, len(out.Commands))
		}
	}
}

func TestParseTooManyCommands(t *testing.T) {
	// Refused as too long to approve, but every command is still emitted so the
	// hard rules see a delete padded past the cap (§15.1).
	out := mustParse(t, strings.Repeat("echo x & ", parser.MaxSimpleCommands+8)+`rd /s /q %USERPROFILE%\Documents`)
	assertUnsupported(t, out, parser.UnsupportedTooManyCommands)
	if len(out.Commands) != parser.MaxSimpleCommands+9 {
		t.Errorf("got %d commands, want all %d emitted", len(out.Commands), parser.MaxSimpleCommands+9)
	}
	if last := out.Commands[len(out.Commands)-1]; last.Name() != "rd" {
		t.Errorf("the command past the cap must still be emitted, got %q", last.Name())
	}
}

func FuzzParse(f *testing.F) {
	seeds := []string{
		`rd /s /q %USERPROFILE%\Documents`,
		`del /q dist\bundle.js`,
		`type log.txt | findstr error`,
		`cd sub & del x`,
		`cmd /c "rd /s /q x"`,
		`echo a^&b`,
		`del "unterminated`,
		`for %%f in (*) do del %%f`,
		`REM comment`,
		`""`,
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
		for i, cmd := range out.Commands {
			if cmd.Name() == "" {
				t.Errorf("command %d has no executable name", i)
			}
		}
	})
}
