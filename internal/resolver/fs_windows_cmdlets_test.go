package resolver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/parser"
	cmdshell "github.com/Vadym903/Intenter/internal/parser/cmd"
	"github.com/Vadym903/Intenter/internal/parser/powershell"
)

// These recognizers are compiled and tested on every OS: the dialects parse
// text, and only the path rules are platform-specific (§14.4). That is what
// lets a Windows npm script be understood from a macOS test run.

// resolveDialect parses and recognizes one command under a chosen dialect.
func (r *repo) resolveDialect(t *testing.T, dialect action.Dialect, command string) action.ResolvedCommand {
	t.Helper()

	var parsed *parser.ParsedCommand
	var err error
	switch dialect {
	case action.DialectPowerShell:
		parsed, err = powershell.New().Parse(parser.Input{
			Command: command, Cwd: r.root, Home: r.home, TempDir: os.TempDir(),
		})
	case action.DialectCmd:
		parsed, err = cmdshell.New().Parse(parser.Input{
			Command: command, Cwd: r.root, Home: r.home, TempDir: os.TempDir(),
		})
	default:
		t.Fatalf("unsupported dialect %s", dialect)
	}
	if err != nil {
		t.Fatalf("parse %q: %v", command, err)
	}
	if len(parsed.Commands) != 1 {
		t.Fatalf("parse %q produced %d commands (%v)", command, len(parsed.Commands), parsed.UnsupportedSummary())
	}

	ctx := r.builder.Build(r.root, "")
	return NewRecognizerRegistry().Recognize(Request{
		Command: parsed.Commands[0], Context: ctx, Dialect: dialect,
	})
}

func TestWindowsDeleteMatchesThePosixDelete(t *testing.T) {
	// The point of these recognizers: the same intent produces the same
	// effects, so trust is portable across the dialects.
	r := newRepo(t)
	r.write(t, "package.json", `{"name":"demo"}`)
	if err := os.MkdirAll(filepath.Join(r.root, "dist"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	posix := r.recognize(t, "rm -rf ./dist")
	powershellForm := r.resolveDialect(t, action.DialectPowerShell, `Remove-Item -Recurse -Force ./dist`)
	cmdForm := r.resolveDialect(t, action.DialectCmd, `rd /s /q dist`)

	want := strings.Join(effectSummary(posix), " | ")
	for name, out := range map[string]action.ResolvedCommand{
		"powershell": powershellForm,
		"cmd":        cmdForm,
	} {
		if out.SemanticOp != action.OpFSDelete {
			t.Errorf("%s: semantic op = %s, want FS_DELETE", name, out.SemanticOp)
		}
		if got := strings.Join(effectSummary(out), " | "); got != want {
			t.Errorf("%s effects = %s, want the same as POSIX %s", name, got, want)
		}
	}
}

func TestPowerShellCmdlets(t *testing.T) {
	r := newRepo(t)

	tests := []struct {
		name    string
		command string
		op      action.SemanticOp
		effects []string
	}{
		{"remove", `Remove-Item -Recurse -Force ./dist`, action.OpFSDelete,
			[]string{"DELETE(force,recursive) ./dist"}},
		{"remove by parameter", `Remove-Item -Path ./dist -Recurse`, action.OpFSDelete,
			[]string{"DELETE(recursive) ./dist"}},
		{"remove literal path", `Remove-Item -LiteralPath ./dist`, action.OpFSDelete,
			[]string{"DELETE ./dist"}},
		{"alias", `rm -Recurse -Force ./dist`, action.OpFSDelete,
			[]string{"DELETE(force,recursive) ./dist"}},
		{"copy", `Copy-Item ./a.txt ./b.txt`, action.OpFSCopy,
			[]string{"READ ./a.txt", "CREATE ./b.txt", "WRITE ./b.txt"}},
		{"copy by parameter", `Copy-Item -Path ./a.txt -Destination ./b.txt`, action.OpFSCopy,
			[]string{"READ ./a.txt", "CREATE ./b.txt", "WRITE ./b.txt"}},
		{"move", `Move-Item ./a.txt ./b.txt`, action.OpFSMove,
			[]string{"DELETE ./a.txt", "CREATE ./b.txt"}},
		{"new item", `New-Item -ItemType Directory -Path ./out`, action.OpFSCreate,
			[]string{"CREATE ./out"}},
		{"get content", `Get-Content ./README.md`, action.OpFSRead,
			[]string{"READ ./README.md"}},
		{"get content alias", `cat ./README.md`, action.OpFSRead,
			[]string{"READ ./README.md"}},
		{"select string", `Select-String TODO ./src`, action.OpFSRead,
			[]string{"READ ./src"}},
		{"get child item", `Get-ChildItem -Recurse ./src`, action.OpFSRead,
			[]string{"READ(recursive) ./src"}},
		{"get child item defaults to the cwd", `Get-ChildItem`, action.OpFSRead,
			[]string{"READ ."}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := r.resolveDialect(t, action.DialectPowerShell, tt.command)
			if out.Status != action.StatusResolved {
				t.Fatalf("status = %s (%s), want RESOLVED", out.Status, out.StatusReason)
			}
			if out.SemanticOp != tt.op {
				t.Errorf("semantic op = %s, want %s", out.SemanticOp, tt.op)
			}
			assertEffects(t, out, tt.effects...)
		})
	}
}

func TestPowerShellParameterCaseIsIgnored(t *testing.T) {
	// PowerShell parameter names are case-insensitive, so `-recurse` must be
	// the same option as `-Recurse`.
	r := newRepo(t)

	for _, command := range []string{
		`Remove-Item -recurse -force ./dist`,
		`Remove-Item -RECURSE -FORCE ./dist`,
		`Remove-Item -Recurse -Force ./dist`,
	} {
		out := r.resolveDialect(t, action.DialectPowerShell, command)
		if out.Status != action.StatusResolved {
			t.Fatalf("%q: status = %s (%s)", command, out.Status, out.StatusReason)
		}
		assertEffects(t, out, "DELETE(force,recursive) ./dist")
	}
}

// TestNewItemLinkTypesAreUnresolved is the AG-121 regression: creating a
// symbolic link, junction or hard link is aliasing a path to somewhere else —
// a fundamentally different, riskier operation than creating an ordinary file
// — and New-Item modeled it identically to -ItemType File/Directory, with the
// -Value (the link's real target) silently dropped. POSIX never resolves `ln`
// at all, so it is always asked; New-Item must be at least as cautious.
func TestNewItemLinkTypesAreUnresolved(t *testing.T) {
	r := newRepo(t)

	for _, command := range []string{
		`New-Item -Path ./dist/evil -ItemType SymbolicLink -Value ~/Documents`,
		`New-Item -Path ./dist/evil -ItemType Junction -Value ~/Documents`,
		`New-Item -Path ./dist/evil -ItemType HardLink -Value ~/Documents`,
		`New-Item -Path ./dist/evil -ItemType symboliclink -Value ~/Documents`,
		`ni -Path ./dist/evil -ItemType SymbolicLink -Value ~/Documents`,
	} {
		out := r.resolveDialect(t, action.DialectPowerShell, command)
		if out.Status != action.StatusUnresolved {
			t.Errorf("%q: status = %s (%s), want UNRESOLVED — a link target must not be modeled as a plain CREATE",
				command, out.Status, out.StatusReason)
		}
	}

	// An ordinary file or directory is unaffected.
	for _, command := range []string{
		`New-Item -Path ./dist/out -ItemType File`,
		`New-Item -Path ./dist/out -ItemType Directory`,
		`New-Item -Path ./dist/out`,
	} {
		out := r.resolveDialect(t, action.DialectPowerShell, command)
		if out.Status != action.StatusResolved {
			t.Errorf("%q: status = %s (%s), want RESOLVED", command, out.Status, out.StatusReason)
		}
	}
}

func TestCmdBuiltins(t *testing.T) {
	r := newRepo(t)

	tests := []struct {
		name    string
		command string
		op      action.SemanticOp
		effects []string
	}{
		{"delete", `del /q notes.txt`, action.OpFSDelete,
			[]string{"DELETE(force) ./notes.txt"}},
		{"remove directory", `rd /s /q dist`, action.OpFSDelete,
			[]string{"DELETE(force,recursive) ./dist"}},
		{"rmdir alias", `rmdir /s /q dist`, action.OpFSDelete,
			[]string{"DELETE(force,recursive) ./dist"}},
		{"switch case is ignored", `rd /S /Q dist`, action.OpFSDelete,
			[]string{"DELETE(force,recursive) ./dist"}},
		{"copy", `copy a.txt b.txt`, action.OpFSCopy,
			[]string{"READ ./a.txt", "CREATE ./b.txt", "WRITE ./b.txt"}},
		{"move", `move a.txt b.txt`, action.OpFSMove,
			[]string{"DELETE ./a.txt", "CREATE ./b.txt"}},
		{"make directory", `md out`, action.OpFSCreate,
			[]string{"CREATE ./out"}},
		{"type", `type README.md`, action.OpFSRead,
			[]string{"READ ./README.md"}},
		{"dir", `dir /s src`, action.OpFSRead,
			[]string{"READ(recursive) ./src"}},
		{"findstr", `findstr TODO src\main.go`, action.OpFSRead,
			[]string{"READ ./src/main.go"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := r.resolveDialect(t, action.DialectCmd, tt.command)
			if out.Status != action.StatusResolved {
				t.Fatalf("status = %s (%s), want RESOLVED", out.Status, out.StatusReason)
			}
			if out.SemanticOp != tt.op {
				t.Errorf("semantic op = %s, want %s", out.SemanticOp, tt.op)
			}
			assertEffects(t, out, tt.effects...)
		})
	}
}

func TestWindowsRecognizersRefuseUnknownOptions(t *testing.T) {
	r := newRepo(t)

	for _, tt := range []struct {
		dialect action.Dialect
		command string
	}{
		{action.DialectPowerShell, `Remove-Item -Zap ./dist`},
		{action.DialectPowerShell, `Copy-Item -Zap ./a ./b`},
		{action.DialectCmd, `rd /zap dist`},
		{action.DialectCmd, `del /zap x`},
	} {
		out := r.resolveDialect(t, tt.dialect, tt.command)
		if out.Status != action.StatusUnresolved {
			t.Errorf("%q: status = %s, want UNRESOLVED (§15.3)", tt.command, out.Status)
		}
	}
}

func TestWindowsDeletesInHomeKeepTheirScope(t *testing.T) {
	// The whole point of a shared effect model: the safety floor sees the same
	// HOME delete whichever dialect expressed it (S7).
	r := newRepo(t)

	powershellForm := r.resolveDialect(t, action.DialectPowerShell,
		`Remove-Item -Recurse -Force $env:USERPROFILE\Documents`)
	cmdForm := r.resolveDialect(t, action.DialectCmd, `rd /s /q %USERPROFILE%\Documents`)

	for name, out := range map[string]action.ResolvedCommand{
		"powershell": powershellForm,
		"cmd":        cmdForm,
	} {
		if len(out.Targets) != 1 {
			t.Fatalf("%s: targets = %+v, want one", name, out.Targets)
		}
		if out.Targets[0].Scope != action.ScopeHome {
			t.Errorf("%s: scope = %s, want HOME", name, out.Targets[0].Scope)
		}
		if !out.Targets[0].HasFlag(action.FlagBroad) {
			t.Errorf("%s: ~/Documents is a standard home directory and must be broad", name)
		}
		if !out.Effects[0].HasFlag(action.EffectFlagRecursive) {
			t.Errorf("%s: the delete is recursive", name)
		}
	}
}

// TestWindowsRedirectionWithoutWhitespaceProducesTheSameEffect is the AG-122
// regression: cmd.exe treats `<`/`>` as metacharacters whether or not
// whitespace surrounds them, so `echo hi>>%USERPROFILE%\...` must model the
// same write effect as the fully-spaced form — before the parser fix, the
// unspaced write vanished into the previous argument and the recognizer saw
// no effect at all.
func TestWindowsRedirectionWithoutWhitespaceProducesTheSameEffect(t *testing.T) {
	r := newRepo(t)

	spaced := r.resolveDialect(t, action.DialectCmd, `echo hi >> %USERPROFILE%\.ssh\authorized_keys`)
	unspaced := r.resolveDialect(t, action.DialectCmd, `echo hi>>%USERPROFILE%\.ssh\authorized_keys`)

	for name, out := range map[string]action.ResolvedCommand{"spaced": spaced, "unspaced": unspaced} {
		if len(out.Targets) != 1 {
			t.Fatalf("%s: targets = %+v, want one — the redirection target must be modeled", name, out.Targets)
		}
		if got := strings.Join(effectSummary(out), " | "); !strings.Contains(got, "WRITE") {
			t.Errorf("%s: effects = %s, want a WRITE effect", name, got)
		}
	}
	if got := strings.Join(effectSummary(unspaced), " | "); got != strings.Join(effectSummary(spaced), " | ") {
		t.Errorf("unspaced effects = %s, want the same as the spaced form %s", got, strings.Join(effectSummary(spaced), " | "))
	}
}

// TestWindowsStreamDuplicationDoesNotDropTheCommand is the other half of
// AG-122: `2>&1` duplicates a stream handle, it does not sequence a new
// command. Misreading its `&` as cmd's sequence operator used to drop the
// whole preceding command from the parsed output, so a HOME delete suffixed
// with this near-universal batch idiom never reached the hard rules at all.
func TestWindowsStreamDuplicationDoesNotDropTheCommand(t *testing.T) {
	r := newRepo(t)

	plain := r.resolveDialect(t, action.DialectCmd, `rd /s /q %USERPROFILE%\Documents`)
	withDup := r.resolveDialect(t, action.DialectCmd, `rd /s /q %USERPROFILE%\Documents 2>&1`)

	if withDup.SemanticOp != action.OpFSDelete {
		t.Fatalf("semantic op = %s, want FS_DELETE — the delete must not disappear behind 2>&1", withDup.SemanticOp)
	}
	if len(withDup.Targets) != 1 || withDup.Targets[0].Scope != action.ScopeHome {
		t.Fatalf("targets = %+v, want one HOME-scoped target, same as without 2>&1", withDup.Targets)
	}
	if got, want := strings.Join(effectSummary(withDup), " | "), strings.Join(effectSummary(plain), " | "); got != want {
		t.Errorf("effects with 2>&1 = %s, want the same as without it: %s", got, want)
	}
}

func TestWindowsPathSeparatorsAreUnderstood(t *testing.T) {
	// Backslash separators resolve to the same target as forward slashes.
	r := newRepo(t)

	backslash := r.resolveDialect(t, action.DialectCmd, `del src\main.go`)
	forward := r.resolveDialect(t, action.DialectCmd, `del src/main.go`)

	if len(backslash.Targets) != 1 || len(forward.Targets) != 1 {
		t.Fatalf("targets = %+v / %+v, want one each", backslash.Targets, forward.Targets)
	}
	if backslash.Targets[0].Display != forward.Targets[0].Display {
		t.Errorf("display = %q and %q, want the same target",
			backslash.Targets[0].Display, forward.Targets[0].Display)
	}
}
