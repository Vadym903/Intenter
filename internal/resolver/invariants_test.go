package resolver

import (
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
)

// The invariant index for resolution: I-2 (the resolver's half) and I-13.
// See internal/approval/invariants_test.go for what this index is for.

func TestInvariant_I2_ResolutionNeverReportsMoreThanItKnows(t *testing.T) {
	// I-2's other half. The policy engine turns uncertainty into a prompt, but
	// only if resolution admits to it: a resolver that reports RESOLVED for a
	// command it guessed at would defeat the rule before it ran.
	r := nodeRepo(t, `{"scripts":{"cleanup":"rm -rf ./dist"}}`)

	commands := map[string]string{
		"a command nothing models":     "some-unknown-tool --wipe",
		"a substitution":               "rm -rf $(cat targets.txt)",
		"an unexpanded variable":       "rm -rf $TARGET",
		"an unknown flag on a delete":  "rm --obliterate ./dist",
		"a script that does not exist": "npm run nonexistent",
	}

	for name, command := range commands {
		t.Run(name, func(t *testing.T) {
			out := r.resolveAction(t, command)
			if out.Status == action.StatusResolved && !out.HasAmbiguousTarget() {
				t.Errorf("%q resolved cleanly to %+v; resolution must admit what it could not read",
					command, out.Effects)
			}
			if out.Status != action.StatusResolved && out.StatusReason == "" {
				t.Error("a non-resolved status must say why, so the prompt can explain itself")
			}
		})
	}
}

func TestInvariant_I13_UncertainScriptsAreEvaluatedUnderEveryReading(t *testing.T) {
	// I-13: uncertain script interpretation MUST be evaluated under all
	// plausible interpretations, with the union of effects.
	//
	// On Windows npm hands a package script to cmd.exe, but Git Bash may supply
	// the utilities it calls, and the script text does not say which one runs.
	// Picking a reading would mean picking, half the time, the one that misses
	// the dangerous effect.
	//
	// See also TestWindowsScriptEffectsAreTheUnionOfBothDialects.
	r := nodeRepo(t, `{"scripts":{}}`)

	// Each script means something different under the two shells.
	tests := map[string]struct {
		script       string
		onlyInPosix  action.Scope
		presentInCmd bool
	}{
		"a home path only POSIX understands": {
			script: "rm -rf ~/Documents", onlyInPosix: action.ScopeHome,
		},
		"a home path with a subdirectory": {
			script: "rm -rf ~/Documents/notes", onlyInPosix: action.ScopeHome,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			posix := resolveScriptUnder(t, r, tc.script, action.DialectPosix)
			cmd := resolveScriptUnder(t, r, tc.script, action.DialectCmd)
			union := resolveScriptUnder(t, r, tc.script, action.DialectCmd, action.DialectPosix)

			if !containsScope(posix, tc.onlyInPosix) {
				t.Fatalf("the POSIX reading of %q must reach %s, got %v", tc.script, tc.onlyInPosix, posix)
			}
			if containsScope(cmd, tc.onlyInPosix) {
				t.Fatalf("the cmd reading of %q must not reach %s, got %v", tc.script, tc.onlyInPosix, cmd)
			}
			if !containsScope(union, tc.onlyInPosix) {
				t.Error("the union dropped the dangerous reading")
			}
			// The union is a union, not a choice: nothing either reading found
			// may be missing from it.
			for _, scope := range append(append([]action.Scope{}, posix...), cmd...) {
				if !containsScope(union, scope) {
					t.Errorf("the union is missing %s, which one reading found", scope)
				}
			}
		})
	}
}

func TestInvariant_I13_TheWeakerStatusWins(t *testing.T) {
	// The status half of I-13: a script that resolves cleanly under one shell
	// and not the other is a script whose behavior is uncertain, and taking the
	// cleaner reading would make it approvable on the strength of the reading
	// that happens to be simpler.
	//
	// See also TestWindowsScriptStatusIsTheWeakerOfBoth.
	r := nodeRepo(t, `{"scripts":{}}`)

	// A command substitution: unknowable under POSIX, three plain words under
	// cmd.exe.
	const script = "rm -rf $(cat targets.txt)"

	if union := resolveStatusUnder(t, r, script, action.DialectCmd, action.DialectPosix); union.Approvable() {
		t.Errorf("union status = %s, want the weaker of the two readings", union)
	}
}
