package parser

import (
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
)

func testContext() VarContext {
	return VarContext{Home: "/Users/u", Cwd: "/w/demo", TempDir: "/tmp/user"}
}

func TestExpandTilde(t *testing.T) {
	tests := []struct{ in, want string }{
		{"~", "/Users/u"},
		{"~/Documents", "/Users/u/Documents"},
		{"~/", "/Users/u/"},
		{"~other/x", "~other/x"},
		{"./dist", "./dist"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := ExpandTilde(tt.in, "/Users/u"); got != tt.want {
			t.Errorf("ExpandTilde(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if got := ExpandTilde("~/x", ""); got != "~/x" {
		t.Errorf("without a home directory the word stays literal, got %q", got)
	}
}

func TestExpandPosixString(t *testing.T) {
	ctx := testContext()
	tests := []struct {
		in         string
		want       string
		unexpanded bool
	}{
		{"$HOME/Documents", "/Users/u/Documents", false},
		{"${HOME}/Documents", "/Users/u/Documents", false},
		{"$PWD/dist", "/w/demo/dist", false},
		{"$TMPDIR/build", "/tmp/user/build", false},
		{"~/dist", "/Users/u/dist", false},
		{"./dist", "./dist", false},
		{"$UNKNOWN/x", "$UNKNOWN/x", true},
		{"${UNKNOWN}/x", "${UNKNOWN}/x", true},
		{"${HOME:-/fallback}", "${HOME:-/fallback}", true},
		{"cost is 100$", "cost is 100$", false},
	}
	for _, tt := range tests {
		got, unexpanded := ExpandPosixString(tt.in, ctx)
		if got != tt.want || unexpanded != tt.unexpanded {
			t.Errorf("ExpandPosixString(%q) = (%q, %v), want (%q, %v)", tt.in, got, unexpanded, tt.want, tt.unexpanded)
		}
	}
}

func TestExpandPowerShell(t *testing.T) {
	ctx := testContext()
	tests := []struct {
		in         string
		want       string
		unexpanded bool
	}{
		{"$HOME\\Documents", "/Users/u\\Documents", false},
		{"$env:USERPROFILE\\Documents", "/Users/u\\Documents", false},
		{"$env:TEMP\\build", "/tmp/user\\build", false},
		{"$PWD\\dist", "/w/demo\\dist", false},
		{"~\\dist", "/Users/u\\dist", false},
		{".\\dist", ".\\dist", false},
		{"$env:SECRET_PATH", "$env:SECRET_PATH", true},
		{"$unknown", "$unknown", true},
	}
	for _, tt := range tests {
		got, unexpanded := ExpandPowerShell(tt.in, ctx)
		if got != tt.want || unexpanded != tt.unexpanded {
			t.Errorf("ExpandPowerShell(%q) = (%q, %v), want (%q, %v)", tt.in, got, unexpanded, tt.want, tt.unexpanded)
		}
	}
}

func TestExpandCmd(t *testing.T) {
	ctx := testContext()
	tests := []struct {
		in         string
		want       string
		unexpanded bool
	}{
		{`%USERPROFILE%\Documents`, `/Users/u\Documents`, false},
		{`%HOMEDRIVE%%HOMEPATH%\Documents`, `/Users/u\Documents`, false},
		{`%TEMP%\build`, `/tmp/user\build`, false},
		{`%CD%\dist`, `/w/demo\dist`, false},
		{`.\dist`, `.\dist`, false},
		{`%SECRET%\x`, `%SECRET%\x`, true},
		{`100%`, `100%`, false},
	}
	for _, tt := range tests {
		got, unexpanded := ExpandCmd(tt.in, ctx)
		if got != tt.want || unexpanded != tt.unexpanded {
			t.Errorf("ExpandCmd(%q) = (%q, %v), want (%q, %v)", tt.in, got, unexpanded, tt.want, tt.unexpanded)
		}
	}
}

func TestLookupIsDialectSpecific(t *testing.T) {
	ctx := testContext()

	if _, ok := Lookup(action.DialectPosix, "USERPROFILE", ctx); ok {
		t.Error("POSIX must not know Windows variables")
	}
	if _, ok := Lookup(action.DialectCmd, "HOME", ctx); ok {
		t.Error("cmd must not know POSIX variables")
	}
	if value, ok := Lookup(action.DialectCmd, "userprofile", ctx); !ok || value != "/Users/u" {
		t.Errorf("cmd variables are case-insensitive: %q, %v", value, ok)
	}
	if value, ok := Lookup(action.DialectPowerShell, "env:USERPROFILE", ctx); !ok || value != "/Users/u" {
		t.Errorf("PowerShell env:USERPROFILE = %q, %v", value, ok)
	}
}

func TestContainsGlob(t *testing.T) {
	globs := []string{"*", "dist/*", "*.go", "file?.txt", "[abc].txt", "**/*.js"}
	for _, text := range globs {
		if !ContainsGlob(text) {
			t.Errorf("ContainsGlob(%q) = false, want true", text)
		}
	}
	plain := []string{"./dist", "package.json", "a[b", "name"}
	for _, text := range plain {
		if ContainsGlob(text) {
			t.Errorf("ContainsGlob(%q) = true, want false", text)
		}
	}
}

func TestIgnoredRedirectionTargets(t *testing.T) {
	for _, target := range []string{"/dev/null", "/dev/stderr", "NUL", "nul", "$null"} {
		if !IsIgnoredRedirectionTarget(target) {
			t.Errorf("%q must be ignored as a redirection target (§14.2)", target)
		}
	}
	if IsIgnoredRedirectionTarget("out.log") {
		t.Error("a real file must not be ignored")
	}
}

func TestDangerousEnvAssignments(t *testing.T) {
	dangerous := []string{
		"PATH", "LD_PRELOAD", "LD_LIBRARY_PATH", "DYLD_INSERT_LIBRARIES", "NODE_OPTIONS", "GIT_DIR", "git_dir",
		// The variables that make a modeled program execute something else; PAGER
		// is the one the original denylist missed.
		"PAGER", "EDITOR", "VISUAL", "BASH_ENV", "ENV", "PERL5OPT", "PYTHONSTARTUP",
		"_JAVA_OPTIONS", "JAVA_TOOL_OPTIONS", "MAVEN_OPTS", "GRADLE_OPTS", "LESSOPEN",
		"HOME", "SHELL", "IFS", "TMPDIR", "HTTPS_PROXY", "npm_config_script_shell",
		// Anything unknown is dangerous: the list is an allowlist (§14.2).
		"FOO", "MY_TOOL_PLUGIN",
	}
	for _, name := range dangerous {
		if !IsDangerousEnvAssignment(name) {
			t.Errorf("%s must make the command UNRESOLVED (§14.2)", name)
		}
	}
	for _, name := range []string{"NODE_ENV", "CI", "NO_COLOR", "FORCE_COLOR", "TZ", "LANG", "node_env"} {
		if IsDangerousEnvAssignment(name) {
			t.Errorf("%s is harmless and must not be flagged", name)
		}
	}
}

func TestStreamInterpretersAndElevation(t *testing.T) {
	for _, name := range []string{"sh", "bash", "/bin/bash", "python3", "node", "powershell.exe", "iex"} {
		if !IsStreamInterpreter(name) {
			t.Errorf("%s executes streamed content and must be flagged (R12)", name)
		}
	}
	if IsStreamInterpreter("grep") {
		t.Error("grep does not execute its input")
	}

	for _, name := range []string{"sudo", "doas", "su", "runas", "/usr/bin/sudo"} {
		if !IsElevationWrapper(name) {
			t.Errorf("%s must be recognized as elevation (R10)", name)
		}
	}
	if IsElevationWrapper("rm") {
		t.Error("rm is not an elevation wrapper")
	}
}

func TestSimpleCommandAccessors(t *testing.T) {
	cmd := SimpleCommand{Argv: []Word{
		{Text: "rm"},
		{Text: "-rf"},
		{Text: "$UNKNOWN/x", ContainsUnexpandedVar: true},
	}}
	if cmd.Name() != "rm" {
		t.Errorf("Name = %q", cmd.Name())
	}
	if got := cmd.ArgTexts(); len(got) != 2 || got[0] != "-rf" {
		t.Errorf("ArgTexts = %v", got)
	}
	if !cmd.HasUnexpandedVar() {
		t.Error("an unexpanded variable must be reported")
	}

	empty := SimpleCommand{}
	if empty.Name() != "" || empty.Args() != nil || empty.HasUnexpandedVar() {
		t.Error("an empty command must be safe to inspect")
	}
}

func TestParsedCommandUnsupportedTracking(t *testing.T) {
	parsed := &ParsedCommand{Dialect: action.DialectPosix}
	if !parsed.OK() {
		t.Error("a clean parse must be OK")
	}

	parsed.AddUnsupported(UnsupportedCommandSubstitution, 7, "$(cat list.txt)")
	if parsed.OK() {
		t.Error("any unsupported construct must make the parse not OK (I-2)")
	}
	summary := parsed.UnsupportedSummary()
	if len(summary) != 1 || !strings.Contains(summary[0], "command_substitution") {
		t.Errorf("summary = %v", summary)
	}
	if !strings.Contains(summary[0], "$(cat list.txt)") {
		t.Errorf("summary must name the construct: %v", summary)
	}
}

// stubDialect is a minimal Dialect used to exercise the registry.
type stubDialect struct{ name action.Dialect }

func (s stubDialect) Name() action.Dialect { return s.name }
func (s stubDialect) Parse(Input) (*ParsedCommand, error) {
	return &ParsedCommand{Dialect: s.name}, nil
}

func TestRegistry(t *testing.T) {
	registry := NewRegistry()
	registry.Register(stubDialect{name: action.DialectPosix})
	registry.Register(stubDialect{name: action.DialectCmd})

	dialect, err := registry.Get(action.DialectPosix)
	if err != nil || dialect.Name() != action.DialectPosix {
		t.Fatalf("Get(posix) = %v, %v", dialect, err)
	}

	_, err = registry.Get(action.DialectPowerShell)
	if err == nil {
		t.Fatal("an unregistered dialect must be an error")
	}
	if !strings.Contains(err.Error(), "cmd") || !strings.Contains(err.Error(), "posix") {
		t.Errorf("the error must list known dialects, got %v", err)
	}

	names := registry.Names()
	if len(names) != 2 || names[0] != "cmd" || names[1] != "posix" {
		t.Errorf("Names = %v, want sorted [cmd posix]", names)
	}
}

func TestUnsupportedConstructString(t *testing.T) {
	if got := (UnsupportedConstruct{Kind: UnsupportedEval}).String(); got != "eval" {
		t.Errorf("String = %q", got)
	}
	if got := (UnsupportedConstruct{Kind: UnsupportedEval, Text: "eval x"}).String(); got != "eval (eval x)" {
		t.Errorf("String = %q", got)
	}
}
