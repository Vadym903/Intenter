package policy_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/platform"
	"github.com/Vadym903/Intenter/internal/policy"
	"github.com/Vadym903/Intenter/internal/resolver"
)

// The B1 unit tests next door prove the rule against hand-built effects. These
// prove the thing a user actually experiences: that the commands an agent runs
// dozens of times an hour reach the baseline through the real resolver, and
// that the ones that only look read-only do not.
//
// The chain is resolver → engine, exactly as the daemon wires it, so a
// recognizer that stops modeling `ls` shows up here as a prompt regression
// rather than as a passing unit test.

const commandEngineVersion = 1

type workspace struct {
	root     string
	home     string
	resolver *resolver.Resolver
	engine   *policy.Engine
	rules    platform.PathRules
}

func newWorkspace(t *testing.T) *workspace {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	root := filepath.Join(home, "projects", "demo")

	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	t.Setenv(platform.EnvTestMode, "1")
	t.Setenv(platform.EnvTestHome, home)
	t.Setenv(platform.EnvDataDir, filepath.Join(base, "data"))
	t.Setenv(platform.EnvConfigDir, filepath.Join(base, "config"))
	t.Setenv(platform.EnvRuntimeDir, filepath.Join(base, "run"))

	p, err := platform.New()
	if err != nil {
		t.Fatalf("platform.New: %v", err)
	}

	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	return &workspace{
		root:     canonicalRoot,
		home:     home,
		resolver: resolver.New(resolver.NewContextBuilder(p, config.Default()), commandEngineVersion),
		engine:   policy.NewEngine(nil, nil, commandEngineVersion),
		rules:    p.PathRules(),
	}
}

func (w *workspace) write(t *testing.T, relPath, content string) {
	t.Helper()
	path := filepath.Join(w.root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

// decide runs one command through the same two steps as the daemon.
func (w *workspace) decide(t *testing.T, command string, adjust ...func(*config.Config)) action.Decision {
	t.Helper()
	cfg := config.Default()
	for _, apply := range adjust {
		apply(&cfg)
	}

	resolved, ctx := w.resolver.Resolve(action.ActionRequest{
		Agent:      "claude-code",
		SessionID:  "s1",
		Tool:       "Bash",
		Dialect:    action.DialectPosix,
		RawCommand: command,
		Cwd:        w.root,
	})

	return w.engine.Evaluate(policy.Input{
		Action:  resolved,
		Context: ctx.Action,
		Config:  cfg,
		Rules:   w.rules,
		Agent:   "claude-code",
	}, nil)
}

func TestBaselineAllowsTheCommandsAnAgentRunsConstantly(t *testing.T) {
	w := newWorkspace(t)
	w.write(t, "src/main.go", "package main\n")
	w.write(t, "README.md", "# demo\n")

	commands := []string{
		"git status",
		"git diff",
		"git log --oneline -10",
		"grep -r foo src",
		"find . -name '*.go'",
		"ls -la src",
		"cat README.md",
		"head -20 src/main.go",
		"wc -l src/main.go",
		"pwd",
		"echo hello",
		"git status && ls src",
	}

	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			decision := w.decide(t, command)
			if decision.Outcome != action.OutcomeAllow {
				t.Fatalf("%q = %s (%s: %s), want ALLOW",
					command, decision.Outcome, decision.Class, decision.Reason)
			}
		})
	}
}

func TestBaselineStopsAtCredentialsAndEscapes(t *testing.T) {
	w := newWorkspace(t)
	w.write(t, ".env", "API_KEY=secret\n")

	tests := []struct {
		command string
		rule    string
		reason  string
	}{
		{
			command: "cat .env",
			rule:    "R5",
			reason:  "a credential file is not an ordinary workspace read",
		},
		{
			command: "cat ../../etc/passwd",
			rule:    "R6",
			reason:  "reading out of the workspace is not covered by B1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.command, func(t *testing.T) {
			decision := w.decide(t, tc.command)
			if decision.Outcome != action.OutcomeAsk {
				t.Fatalf("%q = %s, want ASK — %s", tc.command, decision.Outcome, tc.reason)
			}
			if decision.HardRule != tc.rule {
				t.Errorf("hard rule = %q, want %q (%s)", decision.HardRule, tc.rule, decision.Reason)
			}
		})
	}
}

func TestBaselineStopsAtCommandsThatOnlyLookHarmless(t *testing.T) {
	w := newWorkspace(t)

	// Each of these reads something — and does something else too.
	commands := map[string]string{
		"cp src/main.go build/main.go": "a copy writes",
		"cat src/main.go > out.txt":    "a redirection writes",
		"curl https://example.com":     "a fetch reaches the network",
		"cat ~/.ssh/config":            "outside the workspace",
		"git push":                     "publishes",
	}

	for command, why := range commands {
		t.Run(command, func(t *testing.T) {
			decision := w.decide(t, command)
			if decision.Outcome == action.OutcomeAllow {
				t.Errorf("%q was allowed (%s); %s", command, decision.Class, why)
			}
		})
	}
}

func TestBaselineOffAsksAboutEverything(t *testing.T) {
	// The prototype's one escape hatch for users who want to see every command
	// (§12.6). Turning it off must not leave a command allowed by accident.
	w := newWorkspace(t)
	w.write(t, "README.md", "# demo\n")
	off := func(cfg *config.Config) { cfg.Policy.AllowReadonlyWorkspace = false }

	for _, command := range []string{"git status", "grep -r foo src", "find . -name '*.go'", "cat README.md"} {
		t.Run(command, func(t *testing.T) {
			if decision := w.decide(t, command); decision.Outcome != action.OutcomeAllow {
				t.Fatalf("%q = %s with the baseline on, want ALLOW", command, decision.Outcome)
			}
			decision := w.decide(t, command, off)
			if decision.Outcome != action.OutcomeAsk {
				t.Errorf("%q = %s with the baseline off, want ASK", command, decision.Outcome)
			}
			if decision.Class != action.ClassNoMatchingApproval {
				t.Errorf("class = %s, want NO_MATCHING_APPROVAL", decision.Class)
			}
		})
	}
}
