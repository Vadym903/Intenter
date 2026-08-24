package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// §25 requires `--json` on every list and show command, "for scripting and
// tests". A script is a promise: whatever the state of the machine, the shape
// coming out is the same one the script was written against. These tests hold
// the CLI to that.

func TestEveryListCommandAnswersWithAnArray(t *testing.T) {
	// An empty result is `[]`, never `null`. `jq 'length'` should say 0 on a
	// fresh install rather than fail, and a script should not have to know
	// which command returns which.
	f := startFixture(t)

	commands := map[string][]string{
		"approvals":         {"approvals", "--json"},
		"approvals --all":   {"approvals", "--all", "--json"},
		"history":           {"history", "--json"},
		"history --blocked": {"history", "--blocked", "--json"},
	}

	for name, args := range commands {
		t.Run("empty/"+name, func(t *testing.T) {
			out, _, code := f.inWorkspace(t, args...)
			if code != ExitOK {
				t.Fatalf("exit code = %d\n%s", code, out)
			}
			var decoded []json.RawMessage
			if err := json.Unmarshal([]byte(out), &decoded); err != nil {
				t.Fatalf("%s --json is not an array: %v\n%s", name, err, out)
			}
			if decoded == nil {
				t.Errorf("%s --json returned null; an empty list is [] (§25)", name)
			}
		})
	}

	// And the same commands with content, so the shape does not change with it.
	evaluated := f.evaluate(t, "npm run cleanup", "toolu_1")
	f.inWorkspace(t, "approve", itoa(*evaluated.AuditEventID))

	for name, args := range commands {
		t.Run("populated/"+name, func(t *testing.T) {
			out, _, code := f.inWorkspace(t, args...)
			if code != ExitOK {
				t.Fatalf("exit code = %d\n%s", code, out)
			}
			var decoded []json.RawMessage
			if err := json.Unmarshal([]byte(out), &decoded); err != nil {
				t.Fatalf("%s --json is not an array: %v\n%s", name, err, out)
			}
		})
	}
}

func TestEveryShowCommandAnswersWithAnObject(t *testing.T) {
	f := startFixture(t)
	evaluated := f.evaluate(t, "npm run cleanup", "toolu_1")
	f.inWorkspace(t, "approve", itoa(*evaluated.AuditEventID))

	commands := map[string][]string{
		"approval show": {"approval", "show", "1", "--json"},
		"history show":  {"history", "show", itoa(*evaluated.AuditEventID), "--json"},
		"status":        {"status", "--json"},
		"doctor":        {"doctor", "--json"},
		"version":       {"version", "--json"},
	}

	for name, args := range commands {
		t.Run(name, func(t *testing.T) {
			out, _, _ := f.inWorkspace(t, args...)
			var decoded map[string]json.RawMessage
			if err := json.Unmarshal([]byte(out), &decoded); err != nil {
				t.Fatalf("%s --json is not an object: %v\n%s", name, err, out)
			}
			if len(decoded) == 0 {
				t.Errorf("%s --json is empty", name)
			}
		})
	}
}

func TestJSONOutputIsTheOnlyThingOnStdout(t *testing.T) {
	// A stray human-readable line before or after the JSON breaks every
	// consumer, and is the easiest thing in the world to add by accident.
	f := startFixture(t)
	evaluated := f.evaluate(t, "npm run cleanup", "toolu_1")
	f.inWorkspace(t, "approve", itoa(*evaluated.AuditEventID))

	commands := map[string][]string{
		"approvals":     {"approvals", "--json"},
		"approval show": {"approval", "show", "1", "--json"},
		"history":       {"history", "--json"},
		"history show":  {"history", "show", itoa(*evaluated.AuditEventID), "--json"},
		"status":        {"status", "--json"},
		"doctor":        {"doctor", "--json"},
	}

	for name, args := range commands {
		t.Run(name, func(t *testing.T) {
			out, _, _ := f.inWorkspace(t, args...)

			trimmed := strings.TrimSpace(out)
			if trimmed == "" {
				t.Fatalf("%s --json produced nothing", name)
			}
			if first := trimmed[0]; first != '{' && first != '[' {
				t.Errorf("%s --json starts with %q, not with JSON:\n%s", name, first, out)
			}

			var decoded any
			decoder := json.NewDecoder(strings.NewReader(out))
			if err := decoder.Decode(&decoded); err != nil {
				t.Fatalf("%s --json: %v\n%s", name, err, out)
			}
			// Anything after the first value is a second document, which no
			// ordinary JSON consumer will read.
			var extra any
			if err := decoder.Decode(&extra); err == nil {
				t.Errorf("%s --json wrote more than one document:\n%s", name, out)
			}
		})
	}
}

func TestOutputCarriesNoTerminalEscapes(t *testing.T) {
	// Intenter writes no color at all, which is how it stays readable when
	// piped into a file, a pager, or an agent's own transcript. This holds it
	// there: color would have to be added deliberately, behind a TTY check.
	f := startFixture(t)
	evaluated := f.evaluate(t, "npm run cleanup", "toolu_1")
	f.inWorkspace(t, "approve", itoa(*evaluated.AuditEventID))
	f.evaluate(t, "rm -rf ~/Documents", "toolu_2")

	commands := map[string][]string{
		"approvals":     {"approvals"},
		"approval show": {"approval", "show", "1"},
		"history":       {"history"},
		"history show":  {"history", "show", itoa(*evaluated.AuditEventID)},
		"status":        {"status"},
		"doctor":        {"doctor"},
		"version":       {"version"},
	}

	for name, args := range commands {
		t.Run(name, func(t *testing.T) {
			out, errOut, _ := f.inWorkspace(t, args...)
			for stream, text := range map[string]string{"stdout": out, "stderr": errOut} {
				if strings.ContainsRune(text, 0x1b) {
					t.Errorf("%s wrote an escape sequence to %s:\n%q", name, stream, text)
				}
			}
		})
	}
}

func TestNoLineEndsInWhitespace(t *testing.T) {
	// Trailing whitespace survives copy-paste into an issue and shows up in a
	// diff as a change that is not one.
	f := startFixture(t)
	evaluated := f.evaluate(t, "npm run cleanup", "toolu_1")
	f.inWorkspace(t, "approve", itoa(*evaluated.AuditEventID))

	commands := map[string][]string{
		"approvals":       {"approvals"},
		"approvals --all": {"approvals", "--all"},
		"approval show":   {"approval", "show", "1"},
		"history":         {"history"},
		"history show":    {"history", "show", itoa(*evaluated.AuditEventID)},
		"status":          {"status"},
		"doctor":          {"doctor"},
	}

	for name, args := range commands {
		t.Run(name, func(t *testing.T) {
			out, _, _ := f.inWorkspace(t, args...)
			for i, line := range strings.Split(out, "\n") {
				if line != strings.TrimRight(line, " \t") {
					t.Errorf("%s line %d ends in whitespace: %q", name, i+1, line)
				}
			}
		})
	}
}

func TestHumanOutputIsStableAcrossRuns(t *testing.T) {
	// Go randomizes map iteration, so anything rendered from a map has to be
	// sorted first. Running each command twice is the cheapest way to notice.
	f := startFixture(t)
	evaluated := f.evaluate(t, "npm run cleanup", "toolu_1")
	f.inWorkspace(t, "approve", itoa(*evaluated.AuditEventID))

	commands := map[string][]string{
		"approvals":     {"approvals"},
		"approval show": {"approval", "show", "1"},
		"history":       {"history"},
		"history show":  {"history", "show", itoa(*evaluated.AuditEventID)},
		"status":        {"status"},
		"doctor":        {"doctor"},
	}

	for name, args := range commands {
		t.Run(name, func(t *testing.T) {
			first, _, _ := f.inWorkspace(t, args...)
			for i := 0; i < 4; i++ {
				again, _, _ := f.inWorkspace(t, args...)
				if again != first {
					t.Fatalf("%s changed between runs:\n--- first ---\n%s\n--- again ---\n%s",
						name, first, again)
				}
			}
		})
	}
}
