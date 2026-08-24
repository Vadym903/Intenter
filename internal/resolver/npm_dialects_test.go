package resolver

import (
	"strings"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/parser"
)

// §15.5.4 and INVARIANT I-13: on Windows, npm runs a package script through
// cmd.exe, but Git Bash may supply the utilities the script calls. Which one
// actually runs is not knowable from the script text, so both are evaluated and
// their effects are combined — never the more permissive of the two.
//
// These tests run on every OS: the dialects parse text, so the Windows
// behavior is verifiable from a macOS or Linux CI run.

func TestScriptDialectsOnWindowsCoverBothShells(t *testing.T) {
	tests := []struct {
		name     string
		info     action.PackageManagerInfo
		hostOS   string
		dialects []action.Dialect
		ok       bool
	}{
		{
			name:     "npm on windows evaluates both",
			info:     action.PackageManagerInfo{Kind: action.PMNpm},
			hostOS:   "windows",
			dialects: []action.Dialect{action.DialectCmd, action.DialectPosix},
			ok:       true,
		},
		{
			name:     "npm on macOS is posix only",
			info:     action.PackageManagerInfo{Kind: action.PMNpm},
			hostOS:   "darwin",
			dialects: []action.Dialect{action.DialectPosix},
			ok:       true,
		},
		{
			name:     "yarn berry ships its own shell everywhere",
			info:     action.PackageManagerInfo{Kind: action.PMYarnBerry},
			hostOS:   "windows",
			dialects: []action.Dialect{action.DialectPosix},
			ok:       true,
		},
		{
			name:     "a configured shell settles it",
			info:     action.PackageManagerInfo{Kind: action.PMNpm, ScriptShell: "/bin/bash"},
			hostOS:   "windows",
			dialects: []action.Dialect{action.DialectPosix},
			ok:       true,
		},
		{
			name:     "a configured cmd shell",
			info:     action.PackageManagerInfo{Kind: action.PMNpm, ScriptShell: `C:\Windows\System32\cmd.exe`},
			hostOS:   "windows",
			dialects: []action.Dialect{action.DialectCmd},
			ok:       true,
		},
		{
			name:   "an unrecognized shell is not guessed at",
			info:   action.PackageManagerInfo{Kind: action.PMNpm, ScriptShell: "/opt/weird/fish"},
			hostOS: "windows",
			ok:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dialects, ok := ScriptDialects(tt.info, tt.hostOS)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if !ok {
				return
			}
			if len(dialects) != len(tt.dialects) {
				t.Fatalf("dialects = %v, want %v", dialects, tt.dialects)
			}
			for i, want := range tt.dialects {
				if dialects[i] != want {
					t.Errorf("dialect %d = %s, want %s", i, dialects[i], want)
				}
			}
		})
	}
}

func TestWindowsScriptEffectsAreTheUnionOfBothDialects(t *testing.T) {
	// The case I-13 exists for: `rm -rf ~/Documents` in a package script is a
	// POSIX delete of the home directory, and under cmd.exe `~` is a literal
	// directory name. Evaluating only cmd would miss the dangerous reading.
	r := nodeRepo(t, `{"scripts":{"cleanup":"rm -rf ~/Documents"}}`)

	posixOnly := resolveScriptUnder(t, r, "rm -rf ~/Documents", action.DialectPosix)
	cmdOnly := resolveScriptUnder(t, r, "rm -rf ~/Documents", action.DialectCmd)
	union := resolveScriptUnder(t, r, "rm -rf ~/Documents", action.DialectCmd, action.DialectPosix)

	if !containsScope(posixOnly, action.ScopeHome) {
		t.Fatalf("the POSIX reading targets HOME, got %v", posixOnly)
	}
	if containsScope(cmdOnly, action.ScopeHome) {
		t.Fatalf("under cmd.exe `~` is a literal name, got %v", cmdOnly)
	}
	if !containsScope(union, action.ScopeHome) {
		t.Error("the union must keep the dangerous reading (I-13)")
	}
	if len(union) < len(cmdOnly) {
		t.Errorf("the union is at least the cmd reading: %v vs %v", union, cmdOnly)
	}
}

func TestWindowsScriptStatusIsTheWeakerOfBoth(t *testing.T) {
	// If either reading cannot be modeled, the action cannot be approved: a
	// script that resolves cleanly under one shell and not the other is still
	// a script whose behavior is uncertain.
	r := nodeRepo(t, `{"scripts":{}}`)

	// `$(...)` is a command substitution under POSIX and three ordinary words
	// under cmd.exe: harmless in one reading, unknowable in the other.
	const script = "rm -rf $(cat targets.txt)"

	posixReading := resolveStatusUnder(t, r, script, action.DialectPosix)
	cmdReading := resolveStatusUnder(t, r, script, action.DialectCmd)
	union := resolveStatusUnder(t, r, script, action.DialectCmd, action.DialectPosix)

	if !cmdReading.Approvable() {
		t.Fatalf("cmd reading = %s, want an approvable status", cmdReading)
	}
	if posixReading.Approvable() {
		t.Fatalf("posix reading = %s, want a non-approvable status", posixReading)
	}
	if union.Approvable() {
		t.Errorf("union status = %s, want the weaker of the two (I-13)", union)
	}
}

func TestUnknownScriptShellIsNeverGuessed(t *testing.T) {
	// A shell Intenter does not understand means the script's behavior is
	// unknown, whatever the text says.
	r := nodeRepo(t, `{"scripts":{"cleanup":"rm -rf ./dist"}}`)
	r.write(t, ".npmrc", "script-shell=/opt/weird/fish\n")

	out := r.resolveAction(t, "npm run cleanup")
	if out.Status.Approvable() {
		t.Fatalf("status = %s, want a non-approvable status", out.Status)
	}
	if !strings.Contains(out.StatusReason, "script-shell") {
		t.Errorf("reason = %q, want it to name the configured shell", out.StatusReason)
	}
}

// resolveScriptUnder resolves a script text under the given dialects and
// returns the scopes of every target it touches.
func resolveScriptUnder(t *testing.T, r *repo, script string, dialects ...action.Dialect) []action.Scope {
	t.Helper()
	commands := resolveScriptCommands(t, r, script, dialects...)

	var scopes []action.Scope
	for _, command := range commands {
		for _, target := range command.Targets {
			scopes = append(scopes, target.Scope)
		}
	}
	return scopes
}

// resolveStatusUnder returns the combined status of a script under the
// dialects: the resolver's own verdict, weakened by every command it produced.
func resolveStatusUnder(t *testing.T, r *repo, script string, dialects ...action.Dialect) action.ResolutionStatus {
	t.Helper()
	result := resolveScriptResult(t, r, script, dialects...)

	status := result.Status
	if status == "" {
		status = action.StatusResolved
	}
	for _, command := range result.Commands {
		status = action.WeakerStatus(status, command.Status)
	}
	return status
}

// resolveScriptCommands returns just the commands a script resolved to.
func resolveScriptCommands(t *testing.T, r *repo, script string, dialects ...action.Dialect) []action.ResolvedCommand {
	t.Helper()
	return resolveScriptResult(t, r, script, dialects...).Commands
}

// resolveScriptResult runs the pipeline's own script resolver, which is what
// the npm recognizer uses.
func resolveScriptResult(t *testing.T, r *repo, script string, dialects ...action.Dialect) ScriptResult {
	t.Helper()

	resolver := New(r.builder, 1)
	ctx := r.builder.Build(r.root, "")
	run := &resolveRun{resolver: resolver, ctx: ctx, deadline: farFuture()}

	return run.resolveScript(Request{
		Command: parser.SimpleCommand{EffectiveCwd: r.root},
		Context: ctx,
		Dialect: action.DialectPosix,
	}, Script{
		Text:     script,
		Cwd:      r.root,
		Dialects: dialects,
		Label:    "npm run cleanup",
		Key:      "package.json#scripts.cleanup",
	})
}

// containsScope reports whether a scope appears in the list.
func containsScope(scopes []action.Scope, want action.Scope) bool {
	for _, scope := range scopes {
		if scope == want {
			return true
		}
	}
	return false
}

// farFuture is a deadline no test will reach.
func farFuture() time.Time { return time.Now().Add(time.Hour) }
