package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Vadym903/Intenter/internal/adapter/claude"
)

// `intenter menu` is injected into an agent session with `` !`intenter menu` ``,
// and a command that fails there aborts the whole `/intenter` invocation — the
// user gets a shell error instead of the menu, at exactly the moment something
// is wrong. So every one of these cases has to end in exit 0 with the problem
// stated in the output.

func TestMenuExitsZeroWithTheDaemonDown(t *testing.T) {
	f := startFixture(t)
	stopFixtureDaemon(t, f)

	out, _, code := f.inWorkspace(t, "menu")
	if code != ExitOK {
		t.Fatalf("exit code = %d, want 0 — a non-zero exit aborts the whole /intenter invocation\n%s", code, out)
	}
	if !strings.Contains(out, "daemon is not answering") {
		t.Errorf("the menu must say the daemon is down:\n%s", out)
	}
	if !strings.Contains(out, "intenter daemon start") {
		t.Errorf("the menu must say how to start it:\n%s", out)
	}
}

// A directory with no git root above it is still a project — its own path.
// What matters is that the menu names whichever one it is acting on, so the
// user is never reading someone else's permissions by accident.
func TestMenuNamesTheProjectEvenWhenNothingIsTrusted(t *testing.T) {
	f := startFixture(t)

	out, _, code := f.inWorkspace(t, "menu")
	if code != ExitOK {
		t.Fatalf("exit code = %d\n%s", code, out)
	}
	if !strings.Contains(out, f.workspace) {
		t.Errorf("the menu must name the project it is about, even with nothing trusted:\n%s", out)
	}

	plain := filepath.Join(f.home, "not-a-repository")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	out, _, code = runInDir(t, plain, "menu")
	if code != ExitOK {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "What you can do") {
		t.Errorf("the menu must still list its actions outside a repository:\n%s", out)
	}
	if !strings.Contains(out, plain) {
		t.Errorf("a directory with no git root is its own project and must be named:\n%s", out)
	}
}

// The one case the exit-0 contract exists for, and the one the other tests all
// missed because their fixture always has a valid configuration: a broken
// config.toml made `intenter menu` exit 1 before it ever ran, so `/intenter`
// aborted with a shell error at exactly the moment the user needed to be told
// what was wrong.
func TestMenuExitsZeroWithABrokenConfig(t *testing.T) {
	f := startFixture(t)
	broken := filepath.Join(f.home, "broken-config.toml")
	writeFile(t, broken, "[log\nlevel = \"debug\n")

	out, _, code := f.inWorkspace(t, "menu", "--config", broken)
	if code != ExitOK {
		t.Fatalf("exit code = %d, want 0 — a non-zero exit aborts the whole /intenter invocation\n%s", code, out)
	}
	if !strings.Contains(out, "could not start up") {
		t.Errorf("the menu must say start-up failed:\n%s", out)
	}
	if !strings.Contains(out, "intenter doctor") {
		t.Errorf("the menu must point at the thing that explains it:\n%s", out)
	}
	if !strings.Contains(out, "What you can do") {
		t.Errorf("the actions must still be listed — one of them is how the user recovers:\n%s", out)
	}
}

// Every other command treats the same failure as fatal, which is right for a
// terminal. Only `menu` degrades.
func TestOtherCommandsStillFailOnABrokenConfig(t *testing.T) {
	f := startFixture(t)
	broken := filepath.Join(f.home, "broken-config.toml")
	writeFile(t, broken, "[log\nlevel = \"debug\n")

	for _, args := range [][]string{{"approvals"}, {"status"}, {"history"}} {
		full := append(args, "--config", broken)
		_, errOut, code := f.inWorkspace(t, full...)
		if code == ExitOK {
			t.Errorf("%v: a broken config must be fatal outside the menu", args)
		}
		if !strings.Contains(errOut, "config") {
			t.Errorf("%v: stderr = %q, want the config error", args, errOut)
		}
	}
}

func TestMenuExitsZeroWithUnreadableSettings(t *testing.T) {
	f := startFixture(t)

	settings := filepath.Join(f.home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, settings, "{ this is not json")

	out, _, code := f.inWorkspace(t, "menu")
	if code != ExitOK {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "could not be read") {
		t.Errorf("an unparsable settings file must be reported — its rules are unknown, "+
			"which is not the same as absent:\n%s", out)
	}
}

func TestMenuListsEveryActionWithAnExample(t *testing.T) {
	f := startFixture(t)

	out, _, code := f.inWorkspace(t, "menu")
	if code != ExitOK {
		t.Fatalf("exit code = %d\n%s", code, out)
	}
	for _, act := range MenuActions() {
		if !strings.Contains(out, "/intenter "+menuActionLabel(act)) {
			t.Errorf("the menu does not offer %q:\n%s", act.Name, out)
		}
		if !strings.Contains(out, act.Example) {
			t.Errorf("action %q has no example in the output:\n%s", act.Name, out)
		}
	}
	if !strings.Contains(out, "Nothing is trusted in this project yet") {
		t.Errorf("an empty project must say so:\n%s", out)
	}
	if !strings.Contains(out, "intenter approve") {
		t.Errorf("the empty state must say how a permission comes to exist:\n%s", out)
	}
}

// The menu is the first thing most people will see, so it has to show the same
// two sources the full listing does — otherwise it under-reports what the
// project trusts at exactly the moment someone is checking.
func TestMenuShowsTheRulesClaudeHoldsOfItsOwn(t *testing.T) {
	f := startFixture(t)
	writeAllowRules(t, filepath.Join(f.home, ".claude", "settings.json"), "Bash(git status)")

	out, _, code := f.inWorkspace(t, "menu")
	if code != ExitOK {
		t.Fatalf("exit code = %d\n%s", code, out)
	}
	if !strings.Contains(out, "Bash(git status)") {
		t.Errorf("the menu must list the rules Claude holds:\n%s", out)
	}
	if !strings.Contains(out, "claude rule") {
		t.Errorf("the menu must say where a rule came from:\n%s", out)
	}
}

func TestMenuJSONCarriesTheProjectAndActions(t *testing.T) {
	f := startFixture(t)

	out, _, code := f.inWorkspace(t, "menu", "--json")
	if code != ExitOK {
		t.Fatalf("exit code = %d\n%s", code, out)
	}

	var result struct {
		Project     string       `json:"project"`
		Warnings    []string     `json:"warnings"`
		Permissions []any        `json:"permissions"`
		Actions     []MenuAction `json:"actions"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("parse the menu JSON: %v\n%s", err, out)
	}
	if len(result.Actions) != len(MenuActions()) {
		t.Errorf("actions = %d, want %d", len(result.Actions), len(MenuActions()))
	}
	if result.Warnings == nil || result.Permissions == nil {
		t.Error("warnings and permissions must be arrays, never null, so a consumer can range over them")
	}
}

// TestMenuAndSkillAgree keeps the two renderings of one list in step. The skill
// file dispatches `/intenter allowed` to whatever the registry says, so a menu
// that advertised something the skill did not dispatch would be a lie the user
// only discovers by typing it.
//
// The check lives here rather than beside the renderer because the registry is
// in this package and the adapter cannot import it — the dependency points one
// way on purpose.
func TestMenuAndSkillAgree(t *testing.T) {
	actions := MenuActions()
	skill := SkillActions()
	if len(skill) != len(actions) {
		t.Fatalf("skill actions = %d, menu actions = %d", len(skill), len(actions))
	}
	for i, act := range actions {
		if skill[i].Name != act.Name || skill[i].Argument != act.Argument {
			t.Errorf("entry %d: skill has %q/%q, menu has %q/%q",
				i, skill[i].Name, skill[i].Argument, act.Name, act.Argument)
		}
		if skill[i].Command != act.Command {
			t.Errorf("entry %d (%s): skill dispatches %q, menu documents %q",
				i, act.Name, skill[i].Command, act.Command)
		}
	}

	// And the file that actually ships lists them, in the same order.
	body := claude.RenderSkill(skill)
	position := 0
	for _, act := range actions {
		row := "| `" + menuActionLabel(act) + "` |"
		found := strings.Index(body[position:], row)
		if found < 0 {
			t.Errorf("the skill file does not dispatch %q, or lists it out of order:\n%s",
				menuActionLabel(act), body)
			continue
		}
		position += found + len(row)
	}
}

// Every entry has to say what it does and show something the reader can run,
// or the menu is a list of names — which is the documentation problem it exists
// to solve.
func TestMenuActionsAreComplete(t *testing.T) {
	root, _ := NewRootCommand(io.Discard, io.Discard)

	for _, act := range MenuActions() {
		if strings.TrimSpace(act.Summary) == "" {
			t.Errorf("action %q has no description", act.Name)
		}
		if strings.TrimSpace(act.Example) == "" {
			t.Errorf("action %q has no example", act.Name)
		}
		if act.Changes && strings.TrimSpace(act.Undo) == "" {
			t.Errorf("action %q changes a permission but does not say whether that can be undone", act.Name)
		}
		if !act.Changes && act.Undo != "" {
			t.Errorf("action %q does not change anything but talks about undoing it", act.Name)
		}

		// The example must name a command that exists. An example that does not
		// run is worse than no example: it is a promise the tool breaks.
		if !commandExists(root, act.Example) {
			t.Errorf("action %q gives an example that names no real command: %q", act.Name, act.Example)
		}
		if !commandExists(root, act.Command) {
			t.Errorf("action %q dispatches to no real command: %q", act.Name, act.Command)
		}
	}
}

// FR-026: the menu may only see, remove and pause. Nothing in it may create or
// widen a permission — that is what the gate's own flow is for, with its own
// checks, and a menu action would be a way around them.
func TestMenuOffersNoWayToGrantAPermission(t *testing.T) {
	forbidden := map[string]bool{"approve": true, "setup": true, "hook": true, "daemon": true}
	for _, act := range MenuActions() {
		fields := strings.Fields(act.Command)
		if len(fields) < 2 {
			t.Errorf("action %q has no command", act.Name)
			continue
		}
		if forbidden[fields[1]] {
			t.Errorf("action %q maps to `intenter %s`, which can grant or reconfigure a permission",
				act.Name, fields[1])
		}
	}
}

// commandExists reports whether a command line names a real command in the
// tree, ignoring flags and arguments.
func commandExists(root *cobra.Command, line string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 || fields[0] != "intenter" {
		return false
	}

	current := root
	for _, field := range fields[1:] {
		if strings.HasPrefix(field, "-") {
			break
		}
		next, _, err := current.Find([]string{field})
		if err != nil || next == current {
			// Not a subcommand: the rest is an argument, e.g. `revoke 3`.
			break
		}
		current = next
	}
	return current != root
}

// runInDir runs a CLI command with a chosen working directory.
func runInDir(t *testing.T, dir string, args ...string) (string, string, int) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("chdir back: %v", err)
		}
	}()
	return runCLI(t, args...)
}
