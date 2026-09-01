package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
)

// settingsWith builds a single settings file with the given allow rules.
func settingsWith(scope Scope, allow ...string) []SettingsFile {
	return []SettingsFile{{
		Scope:       scope,
		Path:        string(scope) + "/settings.json",
		Exists:      true,
		Permissions: Permissions{Allow: allow},
	}}
}

func TestParseRule(t *testing.T) {
	tests := []struct {
		raw     string
		tool    string
		content string
		ok      bool
	}{
		{"Bash(npm run cleanup)", "Bash", "npm run cleanup", true},
		{"Bash(npm run:*)", "Bash", "npm run:*", true},
		{"Bash(*)", "Bash", "*", true},
		{"Bash", "Bash", "*", true},
		{"PowerShell(Get-ChildItem)", "PowerShell", "Get-ChildItem", true},
		{"Bash(nested (parens) here)", "Bash", "nested (parens) here", true},
		{"", "", "", false},
		{"Bash(unterminated", "", "", false},
		{"(no tool)", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			rule, ok := ParseRule(ScopeUser, tt.raw)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if !ok {
				return
			}
			if rule.Tool != tt.tool || rule.Content != tt.content {
				t.Errorf("rule = %+v, want tool %q content %q", rule, tt.tool, tt.content)
			}
			if rule.Key() != string(ScopeUser)+":"+tt.raw {
				t.Errorf("key = %q", rule.Key())
			}
		})
	}
}

func TestRuleGrammarMatching(t *testing.T) {
	// The table from Claude's documented Bash rule grammar (§11.6).
	tests := []struct {
		name       string
		rule       string
		command    string
		wantMatch  bool
		wantExactP bool
	}{
		{"exact", "Bash(npm run cleanup)", "npm run cleanup", true, true},
		{"exact does not match a longer command", "Bash(npm run cleanup)", "npm run cleanup --force", false, false},
		{"exact does not match a prefix", "Bash(npm run cleanup)", "npm run", false, false},

		{"colon star matches a subcommand", "Bash(npm run:*)", "npm run build", true, false},
		{"colon star matches the bare prefix", "Bash(npm run:*)", "npm run", true, false},
		{"colon star enforces a word boundary", "Bash(npm run:*)", "npm runx", false, false},
		{"trailing space star behaves the same", "Bash(npm run *)", "npm run build", true, false},
		{"trailing space star word boundary", "Bash(npm run *)", "npm runx", false, false},

		{"star matches anything", "Bash(*)", "rm -rf /", true, false},
		{"bare tool matches anything", "Bash", "rm -rf /", true, false},

		{"star in the middle", "Bash(git * --dry-run)", "git push origin --dry-run", true, false},
		{"star spans spaces", "Bash(npm *)", "npm run build --watch", true, false},
		{"leading star", "Bash(* --version)", "some tool --version", true, false},
		{"star does not match a different suffix", "Bash(git * --dry-run)", "git push origin", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			consent := Consent(ToolBash, tt.command, settingsWith(ScopeUser, tt.rule))
			if (consent != nil) != tt.wantMatch {
				t.Fatalf("consent = %+v, want match = %v", consent, tt.wantMatch)
			}
			if consent == nil {
				return
			}
			if consent.Exact != tt.wantExactP {
				t.Errorf("exact = %v, want %v", consent.Exact, tt.wantExactP)
			}
			if consent.Kind != action.ConsentKindPersistentRule {
				t.Errorf("kind = %q", consent.Kind)
			}
			if len(consent.RuleKeys) != 1 {
				t.Errorf("rule keys = %v, want one", consent.RuleKeys)
			}
		})
	}
}

func TestConsentRequiresEverySubcommandToBeCovered(t *testing.T) {
	files := settingsWith(ScopeUser, "Bash(git status)", "Bash(npm run:*)")

	tests := []struct {
		command string
		want    bool
	}{
		{"git status", true},
		{"git status && npm run build", true},
		{"git status; npm run build", true},
		{"git status | npm run build", true},
		{"git status || npm run build", true},
		{"git status\nnpm run build", true},
		{"git status && rm -rf /", false},
		{"rm -rf / && git status", false},
		{"git diff", false},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			consent := Consent(ToolBash, tt.command, files)
			if (consent != nil) != tt.want {
				t.Errorf("consent = %+v, want covered = %v", consent, tt.want)
			}
		})
	}
}

func TestConsentStripsClaudesWrappers(t *testing.T) {
	files := settingsWith(ScopeUser, "Bash(npm test)")

	for _, command := range []string{
		"npm test",
		"timeout npm test",
		"time npm test",
		"nice npm test",
		"nohup npm test",
		"stdbuf npm test",
		"command npm test",
		"builtin npm test",
		"noglob npm test",
		"timeout nice npm test",
	} {
		if Consent(ToolBash, command, files) == nil {
			t.Errorf("%q: want consent after stripping the wrapper", command)
		}
	}
}

func TestConsentRefusesWhenTheMatchIsUncertain(t *testing.T) {
	// Every uncertainty yields no consent at all (§11.6). A false positive here
	// would import an approval the user never granted.
	files := settingsWith(ScopeUser, "Bash(npm test)", "Bash(*)")

	tests := []struct {
		name    string
		command string
	}{
		{"leading environment assignment", "NODE_ENV=production npm test"},
		{"assignment in a later subcommand", "npm test && FOO=bar npm test"},
		{"separator inside double quotes", `npm test --name "a && b"`},
		{"separator inside single quotes", `npm test --name 'a | b'`},
		{"pipe inside quotes", `echo "x;y" && npm test`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if consent := Consent(ToolBash, tt.command, files); consent != nil {
				t.Errorf("consent = %+v, want none for an uncertain match", consent)
			}
		})
	}
}

func TestOnlyBareXargsIsStripped(t *testing.T) {
	// Claude strips a bare `xargs`; with options it changes what runs, so the
	// whole invocation has to be covered by a rule of its own.
	files := settingsWith(ScopeUser, "Bash(npm test)")

	if Consent(ToolBash, "xargs -0 npm test", files) != nil {
		t.Error("a rule for `npm test` must not cover `xargs -0 npm test`")
	}
	if Consent(ToolBash, "xargs", files) != nil {
		t.Error("a bare xargs with nothing to run is not covered")
	}

	withRule := settingsWith(ScopeUser, "Bash(xargs -0 npm test)")
	if Consent(ToolBash, "xargs -0 npm test", withRule) == nil {
		t.Error("an exact rule for the full invocation does cover it")
	}
}

func TestConsentIsScopedToTheTool(t *testing.T) {
	files := settingsWith(ScopeUser, "Bash(npm test)")

	if Consent(ToolPowerShell, "npm test", files) != nil {
		t.Error("a Bash rule must not grant consent for the PowerShell tool")
	}
	if Consent(ToolBash, "npm test", nil) != nil {
		t.Error("no settings means no consent")
	}
	if Consent(ToolBash, "", files) != nil {
		t.Error("an empty command has nothing to consent to")
	}
}

func TestConsentCollectsEveryContributingRuleKey(t *testing.T) {
	files := []SettingsFile{
		{Scope: ScopeUser, Exists: true, Permissions: Permissions{Allow: []string{"Bash(git status)"}}},
		{Scope: ScopeLocal, Exists: true, Permissions: Permissions{Allow: []string{"Bash(npm run:*)"}}},
	}

	consent := Consent(ToolBash, "git status && npm run build", files)
	if consent == nil {
		t.Fatal("want consent")
	}
	if len(consent.RuleKeys) != 2 {
		t.Fatalf("rule keys = %v, want one per contributing rule", consent.RuleKeys)
	}
	joined := strings.Join(consent.RuleKeys, " ")
	for _, want := range []string{"user:Bash(git status)", "local:Bash(npm run:*)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("rule keys must include %q, got %v", want, consent.RuleKeys)
		}
	}
	if consent.Exact {
		t.Error("a pattern rule contributed, so the consent is not exact")
	}
}

func TestConsentRuleKeysAreStable(t *testing.T) {
	// The key is what `agent_rule_imports` deduplicates on, so its order must
	// not depend on the order rules happened to be read in.
	files := []SettingsFile{
		{Scope: ScopeLocal, Exists: true, Permissions: Permissions{Allow: []string{"Bash(npm run:*)"}}},
		{Scope: ScopeUser, Exists: true, Permissions: Permissions{Allow: []string{"Bash(git status)"}}},
	}
	reversed := []SettingsFile{files[1], files[0]}

	first := Consent(ToolBash, "git status && npm run build", files)
	second := Consent(ToolBash, "git status && npm run build", reversed)
	if first == nil || second == nil {
		t.Fatal("want consent from both orderings")
	}
	if strings.Join(first.RuleKeys, ",") != strings.Join(second.RuleKeys, ",") {
		t.Errorf("rule keys differ by file order:\n%v\n%v", first.RuleKeys, second.RuleKeys)
	}
}

func TestDenyRulesAreVisibleForExplanations(t *testing.T) {
	files := []SettingsFile{{
		Scope:  ScopeUser,
		Exists: true,
		Permissions: Permissions{
			Deny: []string{"Bash(rm -rf:*)"},
			Ask:  []string{"Bash(git push:*)"},
		},
	}}

	if key, ok := DenyMatch(ToolBash, "rm -rf ./dist", files); !ok || !strings.Contains(key, "rm -rf") {
		t.Errorf("DenyMatch = %q, %v, want the deny rule", key, ok)
	}
	if key, ok := DenyMatch(ToolBash, "git push origin main", files); !ok || !strings.Contains(key, "git push") {
		t.Errorf("DenyMatch = %q, %v, want the ask rule", key, ok)
	}
	if _, ok := DenyMatch(ToolBash, "git status", files); ok {
		t.Error("an unrelated command must not match")
	}
}

func TestSettingsDiscoveryOrderAndCaching(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	project := filepath.Join(base, "project")

	for _, dir := range []string{
		filepath.Join(home, ".claude"),
		filepath.Join(project, ".git"),
		filepath.Join(project, ".claude"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	writeSettings := func(path string, allow ...string) {
		content, err := json.Marshal(Settings{Permissions: Permissions{Allow: allow}})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	userSettings := filepath.Join(home, ".claude", "settings.json")
	localSettings := filepath.Join(project, ".claude", "settings.local.json")
	writeSettings(userSettings, "Bash(git status)")
	writeSettings(localSettings, "Bash(npm run:*)")

	reader := NewSettingsReader(fakePlatform{home: home, os: "darwin"}, "")
	files := reader.Discover(filepath.Join(project, "src"))

	var scopes []string
	for _, file := range files {
		scopes = append(scopes, string(file.Scope))
	}
	if strings.Join(scopes, ",") != "managed,user,project,local" {
		t.Fatalf("discovery order = %v, want managed,user,project,local (§11.6)", scopes)
	}

	consent := Consent(ToolBash, "git status && npm run build", files)
	if consent == nil {
		t.Fatal("rules from both files must apply")
	}

	// An edit is picked up without a restart: the cache is keyed on mtime/size.
	writeSettings(localSettings, "Bash(npm run test)")
	files = reader.Discover(filepath.Join(project, "src"))
	if Consent(ToolBash, "git status && npm run build", files) != nil {
		t.Error("the edited settings file must be re-read (§11.6)")
	}
	if Consent(ToolBash, "git status && npm run test", files) == nil {
		t.Error("the new rule must apply")
	}
}

func TestSettingsIgnoresUnparsableFiles(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	reader := NewSettingsReader(fakePlatform{home: home, os: "darwin"}, "")
	files := reader.Discover("")

	for _, file := range files {
		if len(file.Permissions.Allow) != 0 {
			t.Errorf("an unparsable settings file must yield no rules, got %+v", file.Permissions)
		}
	}
	if Consent(ToolBash, "git status", files) != nil {
		t.Error("no rules means no consent")
	}
}

func TestSettingsUserOverride(t *testing.T) {
	base := t.TempDir()
	override := filepath.Join(base, "custom-settings.json")

	reader := NewSettingsReader(fakePlatform{home: filepath.Join(base, "home"), os: "linux"}, override)
	if got := reader.UserSettingsPath(); got != override {
		t.Errorf("user settings path = %q, want the override %q", got, override)
	}
}

// projectRulesFixture builds a home and a project with settings files.
func projectRulesFixture(t *testing.T) (home, project string, write func(path string, allow ...string)) {
	t.Helper()
	base := t.TempDir()
	home = filepath.Join(base, "home")
	project = filepath.Join(home, "src", "app")
	for _, dir := range []string{
		filepath.Join(home, ".claude"),
		filepath.Join(project, ".git"),
		filepath.Join(project, ".claude"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	write = func(path string, allow ...string) {
		t.Helper()
		content, err := json.Marshal(Settings{Permissions: Permissions{Allow: allow}})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return home, project, write
}

func TestProjectRulesCollectsEveryScope(t *testing.T) {
	home, project, write := projectRulesFixture(t)
	write(filepath.Join(home, ".claude", "settings.json"), "Bash(git status)", "Read(//tmp/**)")
	write(filepath.Join(project, ".claude", "settings.json"), "Bash(npm run build)")
	write(filepath.Join(project, ".claude", "settings.local.json"), "Bash(npm run cleanup)")

	reader := NewSettingsReader(fakePlatform{home: home, os: "darwin"}, "")
	rules, unreadable := ProjectRules(reader, project, ToolBash)
	if len(unreadable) != 0 {
		t.Fatalf("unreadable = %v", unreadable)
	}
	if len(rules) != 3 {
		t.Fatalf("rules = %d, want 3 (the Read rule is another tool's)", len(rules))
	}

	// Precedence order, and each rule carries the identity removal acts on.
	wantScopes := []Scope{ScopeUser, ScopeProject, ScopeLocal}
	for i, rule := range rules {
		if rule.Scope != wantScopes[i] {
			t.Errorf("rule %d scope = %s, want %s", i, rule.Scope, wantScopes[i])
		}
		if rule.Path == "" {
			t.Errorf("rule %d has no file, so nothing could remove it", i)
		}
		if rule.Key() != string(rule.Scope)+":"+rule.Raw {
			t.Errorf("rule %d key = %q", i, rule.Key())
		}
	}
}

// A rule in a file this user cannot change here must be reported as such, not
// quietly listed as removable. Telling someone a permission is gone when it is
// not is worse than telling them it cannot be removed.
func TestProjectRulesMarksWhatCannotBeChangedHere(t *testing.T) {
	home, project, write := projectRulesFixture(t)
	write(filepath.Join(home, ".claude", "settings.json"), "Bash(git status)")
	write(filepath.Join(project, ".claude", "settings.json"), "Bash(npm run build)")
	write(filepath.Join(project, ".claude", "settings.local.json"), "Bash(npm run cleanup)")

	reader := NewSettingsReader(fakePlatform{home: home, os: "darwin"}, "")
	rules, _ := ProjectRules(reader, project, ToolBash)

	byScope := map[Scope]ProjectRule{}
	for _, rule := range rules {
		byScope[rule.Scope] = rule
	}
	if rule := byScope[ScopeProject]; rule.Changeable {
		t.Error("a rule shared through the repository must not be changed by default")
	} else if rule.Reason == "" {
		t.Error("a refusal must say why")
	}
	for _, scope := range []Scope{ScopeUser, ScopeLocal} {
		if !byScope[scope].Changeable {
			t.Errorf("a %s rule is the user's own and must be changeable", scope)
		}
	}
}

func TestProjectRulesReportsAFileItCannotRead(t *testing.T) {
	home, project, write := projectRulesFixture(t)
	write(filepath.Join(home, ".claude", "settings.json"), "Bash(git status)")
	broken := filepath.Join(project, ".claude", "settings.local.json")
	if err := os.WriteFile(broken, []byte("{ not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	reader := NewSettingsReader(fakePlatform{home: home, os: "darwin"}, "")
	rules, unreadable := ProjectRules(reader, project, ToolBash)

	if len(rules) != 1 {
		t.Errorf("rules = %d, want the one readable rule", len(rules))
	}
	if len(unreadable) != 1 || unreadable[0] != broken {
		t.Fatalf("unreadable = %v, want %q — a file whose rules cannot be read makes the "+
			"list incomplete, and saying nothing would present it as complete", unreadable, broken)
	}
}
