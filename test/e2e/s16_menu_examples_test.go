package e2e

import (
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/cli"
)

// S16 (007 SC-002): every example the menu prints actually runs.
//
// The menu's promise is that a person who has never read the documentation can
// copy a line out of it and have it work. A unit test can check that the
// example names a command that exists in the command tree; only running the
// real binary checks that the line as written is accepted — the flags spelled
// right, the arguments in the right places, no usage error.

// exampleArgs turns an example line into argv, dropping the binary name.
func exampleArgs(example string) []string {
	fields := strings.Fields(example)
	if len(fields) < 2 {
		return nil
	}
	return fields[1:]
}

func TestS16EveryMenuExampleRuns(t *testing.T) {
	env := NewEnv(t)
	env.SetScripts(`{"cleanup":"rm -rf ./dist","sweep":"rm -rf ./build","tidy":"rm -rf ./out"}`)

	// The examples name approval 3, the way the documentation does, so there
	// have to be three. Setting up less and relaxing the assertion instead
	// would leave the interesting half of each example untested.
	for i, script := range []string{"cleanup", "sweep", "tidy"} {
		command := "npm run " + script
		env.PreToolUse("session-1", "toolu_"+itoa(int64(i+1)), command)
		env.MustCLI("approve", itoa(env.EventIDFor(command)))
	}

	for _, action := range cli.MenuActions() {
		t.Run(action.Name, func(t *testing.T) {
			args := exampleArgs(action.Example)
			if len(args) == 0 {
				t.Fatalf("action %q has no runnable example: %q", action.Name, action.Example)
			}

			stdout, stderr, code := env.CLI(args...)
			output := stdout + stderr

			// A malformed example is the failure this test exists for: the line
			// was wrong, not the machine.
			for _, usage := range []string{"unknown flag", "unknown shorthand", "unknown command", "Usage:"} {
				if strings.Contains(output, usage) {
					t.Fatalf("the example `%s` is not a command this binary accepts (%s):\n%s",
						action.Example, usage, output)
				}
			}

			if action.Changes && !action.Reversible {
				// An irreversible example stops for a confirmation it cannot ask
				// for here. That is the documented behavior, not a broken
				// example — what matters is that it reached its own plan.
				if code == 0 {
					t.Errorf("`%s` changed something irreversibly without being asked", action.Example)
				}
				if !strings.Contains(output, "stop being trusted") {
					t.Errorf("`%s` did not reach its own behavior:\n%s", action.Example, output)
				}
				return
			}
			if code != 0 {
				t.Errorf("`%s` exited %d:\n%s", action.Example, code, output)
			}
		})
	}
}
