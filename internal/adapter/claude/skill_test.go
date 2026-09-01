package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The `/intenter` command is a file Intenter puts inside the user's own Claude
// configuration directory. The same guest rules apply as to settings.json: a
// file that is not ours is backed up before it is replaced, uninstall takes
// away everything it put there, and nothing else in that directory is touched.

// testSkillActions is a menu shaped like the real one, without depending on the
// CLI package that owns the real content.
func testSkillActions() []SkillAction {
	return []SkillAction{
		{Name: "allowed", Summary: "What runs without asking.", Command: "intenter approvals"},
		{Name: "remove", Argument: "<id>", Summary: "Stop trusting one permission.",
			Command: "intenter approval revoke %s", Changes: true},
	}
}

// skillHome builds an empty Claude configuration directory.
func skillHome(t *testing.T) (configDir, dataDir string) {
	t.Helper()
	root := t.TempDir()
	return filepath.Join(root, ".claude"), filepath.Join(root, "data")
}

func TestInstallSkillWritesTheCommand(t *testing.T) {
	configDir, dataDir := skillHome(t)

	install, err := InstallSkill(configDir, dataDir, testSkillActions(), time.Now())
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if install.Path != filepath.Join(configDir, "skills", "intenter", "SKILL.md") {
		t.Errorf("path = %s", install.Path)
	}
	if !install.CreatedSkillsDir {
		t.Error("the skills directory was created but not reported, so setup cannot tell the user to restart")
	}

	content, err := os.ReadFile(install.Path)
	if err != nil {
		t.Fatalf("read the skill: %v", err)
	}
	body := string(content)
	for _, want := range []string{
		"name: intenter",
		"disable-model-invocation: true",
		"allowed-tools: Bash(intenter *)",
		"!`intenter menu`",
		"| `remove <id>` | `intenter approval revoke <id>` |",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the skill is missing %q:\n%s", want, body)
		}
	}
	// `shell: bash` fails outright on Windows without Git Bash; left unset,
	// Claude Code uses whichever shell tool the machine has.
	if strings.Contains(body, "shell:") {
		t.Error("the skill pins a shell, which breaks on Windows without Git Bash")
	}

	// An agent session has no terminal, so a changing command cannot ask there.
	// It prints its plan and stops; the skill has to route that back through
	// the user rather than silently retrying with --yes.
	if !strings.Contains(body, "Never add `--yes` on the first attempt") {
		t.Errorf("the body must forbid confirming a permission change on the user's behalf:\n%s", body)
	}
}

// A dispatched action must name a command that exists, with the argument in the
// right place — `/intenter remove 3` has to become `intenter approval revoke 3`
// and nothing else.
func TestSkillDispatchPassesTheArgumentThrough(t *testing.T) {
	body := RenderSkill([]SkillAction{
		{Name: "remove", Argument: "<id>", Summary: "Stop trusting one permission.",
			Command: "intenter approval revoke %s", Changes: true},
		{Name: "allowed", Summary: "What runs without asking.", Command: "intenter approvals"},
	})

	if !strings.Contains(body, "| `remove <id>` | `intenter approval revoke <id>` |") {
		t.Errorf("the argument is not passed to the command:\n%s", body)
	}
	// An action with no argument must not grow a stray placeholder.
	if !strings.Contains(body, "| `allowed` | `intenter approvals` |") {
		t.Errorf("an argument-less action rendered wrong:\n%s", body)
	}
	if strings.Contains(body, "%s") {
		t.Error("a placeholder survived into the skill file")
	}
}

// An argument that matches nothing is the one case where guessing does real
// damage: guessing "remove" from "rm 3" would take a permission away that
// nobody asked about. The body has to say so rather than leave it to judgement.
func TestSkillBodyRefusesToGuessAnUnknownArgument(t *testing.T) {
	body := RenderSkill(testSkillActions())

	for _, want := range []string{
		"not a guess to make",
		"say the argument was not recognized",
		"Never invent an identifier",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the body is missing %q:\n%s", want, body)
		}
	}
}

func TestRenderSkillIsByteIdenticalAcrossRuns(t *testing.T) {
	actions := testSkillActions()
	first := RenderSkill(actions)
	for i := 0; i < 20; i++ {
		if got := RenderSkill(actions); got != first {
			t.Fatalf("run %d differs from the first; `intenter update` rewrites this file "+
				"on every upgrade, so a body that reorders itself would churn the user's "+
				"configuration for no reason", i)
		}
	}
}

func TestInstallSkillIsIdempotent(t *testing.T) {
	configDir, dataDir := skillHome(t)
	actions := testSkillActions()

	if _, err := InstallSkill(configDir, dataDir, actions, time.Now()); err != nil {
		t.Fatalf("first install: %v", err)
	}
	before, err := os.ReadFile(SkillPath(configDir))
	if err != nil {
		t.Fatalf("read after the first install: %v", err)
	}

	second, err := InstallSkill(configDir, dataDir, actions, time.Now())
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if !second.Unchanged {
		t.Error("a re-run rewrote an identical file instead of reporting it unchanged")
	}
	if second.BackupPath != "" {
		t.Errorf("a re-run backed up our own file: %s", second.BackupPath)
	}
	after, err := os.ReadFile(SkillPath(configDir))
	if err != nil {
		t.Fatalf("read after the second install: %v", err)
	}
	if string(before) != string(after) {
		t.Error("the second install changed the file")
	}
}

func TestInstallSkillBacksUpAFileItDidNotWrite(t *testing.T) {
	configDir, dataDir := skillHome(t)
	path := SkillPath(configDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	const theirs = "---\nname: intenter\n---\n\nMy own command, which I wrote.\n"
	if err := os.WriteFile(path, []byte(theirs), 0o600); err != nil {
		t.Fatalf("prepare: %v", err)
	}

	install, err := InstallSkill(configDir, dataDir, testSkillActions(), time.Now())
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if install.BackupPath == "" {
		t.Fatal("a file Intenter did not write was replaced without a backup")
	}
	saved, err := os.ReadFile(install.BackupPath)
	if err != nil {
		t.Fatalf("read the backup: %v", err)
	}
	if string(saved) != theirs {
		t.Errorf("the backup does not hold what was there:\n%s", saved)
	}
	if install.CreatedSkillsDir {
		t.Error("the skills directory already existed but was reported as created")
	}
}

func TestSkillUpToDateNoticesAStaleFile(t *testing.T) {
	configDir, dataDir := skillHome(t)
	actions := testSkillActions()

	current, err := SkillUpToDate(configDir, actions)
	if err != nil {
		t.Fatalf("before install: %v", err)
	}
	if current {
		t.Error("a missing skill reported as up to date")
	}

	if _, err := InstallSkill(configDir, dataDir, actions, time.Now()); err != nil {
		t.Fatalf("install: %v", err)
	}
	if current, err = SkillUpToDate(configDir, actions); err != nil || !current {
		t.Errorf("after install: up to date = %v, err = %v", current, err)
	}

	// An older build wrote a different menu.
	older := append(actions, SkillAction{Name: "extra", Summary: "Gone in this build.", Command: "intenter status"})
	if current, err = SkillUpToDate(configDir, older); err != nil || current {
		t.Errorf("a file from another build reported as current: %v, err = %v", current, err)
	}
}

func TestRemoveSkillTakesOnlyItsOwnDirectory(t *testing.T) {
	configDir, dataDir := skillHome(t)
	if _, err := InstallSkill(configDir, dataDir, testSkillActions(), time.Now()); err != nil {
		t.Fatalf("install: %v", err)
	}

	// A skill of the user's own, in the same directory.
	theirs := filepath.Join(SkillsDir(configDir), "deploy")
	if err := os.MkdirAll(theirs, 0o755); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := os.WriteFile(filepath.Join(theirs, "SKILL.md"), []byte("mine\n"), 0o600); err != nil {
		t.Fatalf("prepare: %v", err)
	}

	removed, err := RemoveSkill(configDir)
	if err != nil || !removed {
		t.Fatalf("remove: %v, %v", removed, err)
	}
	if _, err := os.Stat(SkillDir(configDir)); !os.IsNotExist(err) {
		t.Error("the intenter skill directory is still there")
	}
	if _, err := os.Stat(filepath.Join(theirs, "SKILL.md")); err != nil {
		t.Errorf("uninstall removed a skill of the user's own: %v", err)
	}

	// Re-running an uninstall must not fail.
	removed, err = RemoveSkill(configDir)
	if err != nil {
		t.Fatalf("second remove: %v", err)
	}
	if removed {
		t.Error("the second remove reported removing something that was gone")
	}
}
