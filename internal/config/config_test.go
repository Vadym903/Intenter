package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestDefaultsMatchSpecification(t *testing.T) {
	cfg := Default()

	if cfg.Log.Level != "info" {
		t.Errorf("log.level = %q, want info", cfg.Log.Level)
	}
	if !cfg.Daemon.LazyStart {
		t.Error("daemon.lazy_start must default to true")
	}
	if cfg.Daemon.RequestTimeoutMS != 5000 {
		t.Errorf("daemon.request_timeout_ms = %d, want 5000", cfg.Daemon.RequestTimeoutMS)
	}
	if !cfg.Policy.AllowReadonlyWorkspace {
		t.Error("policy.allow_readonly_workspace must default to true")
	}
	if len(cfg.Policy.ProtectedBranches) != 2 {
		t.Errorf("policy.protected_branches = %v, want [main master]", cfg.Policy.ProtectedBranches)
	}
	if cfg.Claude.HookTimeoutSeconds != 10 {
		t.Errorf("claude.hook_timeout_seconds = %d, want 10", cfg.Claude.HookTimeoutSeconds)
	}
	if cfg.Claude.HookConfigChange {
		t.Error("claude.hook_config_change must default to false")
	}
	if !cfg.Audit.StoreResponseSummary {
		t.Error("audit.store_response_summary must default to true")
	}
	if !cfg.Updates.Check || !cfg.Updates.StartupHook {
		t.Errorf("update checking and the start-up hook must default to on: %+v", cfg.Updates)
	}
	if cfg.Updates.Channel != ChannelStable {
		t.Errorf("updates.channel = %q, want stable", cfg.Updates.Channel)
	}
	if got := cfg.Updates.CheckEvery(); got != 24*time.Hour {
		t.Errorf("check interval = %s, want 24h", got)
	}
	if got := cfg.Updates.RemindEvery(); got != 24*time.Hour {
		t.Errorf("remind interval = %s, want 24h", got)
	}
	if got := cfg.Updates.PromptWait(); got != 30*time.Second {
		t.Errorf("prompt timeout = %s, want 30s", got)
	}
}

func TestUpdatesSectionIsRead(t *testing.T) {
	path := writeConfig(t, `
[updates]
check = false
check_interval = "6h"
remind_interval = "90m"
prompt_timeout = "5s"
channel = "prerelease"
startup_hook = false
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Updates.Check || cfg.Updates.StartupHook {
		t.Errorf("switches not applied: %+v", cfg.Updates)
	}
	if !cfg.Updates.Prerelease() {
		t.Error("channel = prerelease must select the pre-release channel")
	}
	if got := cfg.Updates.CheckEvery(); got != 6*time.Hour {
		t.Errorf("check interval = %s, want 6h", got)
	}
	if got := cfg.Updates.RemindEvery(); got != 90*time.Minute {
		t.Errorf("remind interval = %s, want 90m", got)
	}
	if got := cfg.Updates.PromptWait(); got != 5*time.Second {
		t.Errorf("prompt timeout = %s, want 5s", got)
	}
}

func TestUpdateCheckingIsDisabledByTheEnvironment(t *testing.T) {
	// The variable exists so a machine can opt out without editing a file that
	// may be managed by something else, so it has to beat an explicit `true`.
	path := writeConfig(t, "[updates]\ncheck = true\n")
	t.Setenv(EnvNoUpdateCheck, "1")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Updates.Check {
		t.Error("INTENTER_NO_UPDATE_CHECK=1 must switch checking off")
	}
}

func TestTheEnvironmentSelectsTheChannel(t *testing.T) {
	t.Setenv(EnvUpdateChannel, "PreRelease")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Updates.Prerelease() {
		t.Errorf("channel = %q, want prerelease", cfg.Updates.Channel)
	}
}

func TestAnUnknownChannelInTheEnvironmentWarnsRatherThanFails(t *testing.T) {
	// One shell exporting a typo must not make every command in it fail.
	t.Setenv(EnvUpdateChannel, "nightly")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Updates.Channel != ChannelStable {
		t.Errorf("channel = %q, want the stable default", cfg.Updates.Channel)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "nightly") {
		t.Errorf("warnings = %v, want one naming the rejected value", cfg.Warnings)
	}
}

func TestOmittedUpdateDurationsFallBackToTheDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, "[updates]\ncheck = true\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Updates.CheckInterval != "24h" || cfg.Updates.PromptTimeout != "30s" {
		t.Errorf("omitted durations must keep their defaults: %+v", cfg.Updates)
	}
}

func TestLoadMissingFileYieldsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Log.Level != "info" || !cfg.Daemon.LazyStart {
		t.Error("a missing config file must yield defaults")
	}
	if cfg.Path != "" {
		t.Errorf("Path = %q, want empty for a missing file", cfg.Path)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", cfg.Warnings)
	}
}

func TestLoadEmptyPathYieldsDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Daemon.RequestTimeoutMS != 5000 {
		t.Error("empty path must yield defaults")
	}
}

func TestLoadOverridesOnlyProvidedKeys(t *testing.T) {
	path := writeConfig(t, `
[log]
level = "debug"

[policy]
allow_readonly_workspace = false
protected_branches = ["release", "main"]
sensitive_paths_extra = ["/secrets/**"]

[scope]
generated_dirs_extra = ["out", "tmp-build"]

[claude]
hook_timeout_seconds = 20
hook_config_change = true
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Log.Level != "debug" {
		t.Errorf("log.level = %q", cfg.Log.Level)
	}
	if cfg.Policy.AllowReadonlyWorkspace {
		t.Error("policy.allow_readonly_workspace must be overridden to false")
	}
	if len(cfg.Policy.ProtectedBranches) != 2 || cfg.Policy.ProtectedBranches[0] != "release" {
		t.Errorf("protected_branches = %v, want the file value", cfg.Policy.ProtectedBranches)
	}
	if len(cfg.Scope.GeneratedDirsExtra) != 2 {
		t.Errorf("generated_dirs_extra = %v", cfg.Scope.GeneratedDirsExtra)
	}
	if cfg.Claude.HookTimeoutSeconds != 20 || !cfg.Claude.HookConfigChange {
		t.Errorf("claude section not applied: %+v", cfg.Claude)
	}
	// Untouched keys keep their defaults.
	if !cfg.Daemon.LazyStart || cfg.Daemon.RequestTimeoutMS != 5000 {
		t.Errorf("daemon defaults must survive: %+v", cfg.Daemon)
	}
	if !cfg.Audit.StoreResponseSummary {
		t.Error("audit default must survive")
	}
	if cfg.Path != path {
		t.Errorf("Path = %q, want %q", cfg.Path, path)
	}
}

func TestUnknownKeysWarnButDoNotFail(t *testing.T) {
	path := writeConfig(t, `
[log]
level = "info"
colour = "green"

[future]
enabled = true
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unknown keys must not fail: %v", err)
	}
	if len(cfg.Warnings) != 2 {
		t.Fatalf("warnings = %v, want one per unknown key", cfg.Warnings)
	}
	joined := strings.Join(cfg.Warnings, "\n")
	for _, key := range []string{"log.colour", "future.enabled"} {
		if !strings.Contains(joined, key) {
			t.Errorf("warning for %q missing from %v", key, cfg.Warnings)
		}
	}
}

func TestInvalidValuesFail(t *testing.T) {
	tests := map[string]string{
		"bad log level":      "[log]\nlevel = \"chatty\"\n",
		"zero timeout":       "[daemon]\nrequest_timeout_ms = 0\n",
		"negative hook":      "[claude]\nhook_timeout_seconds = -1\n",
		"malformed toml":     "[log\nlevel = \"info\"\n",
		"wrong type":         "[daemon]\nlazy_start = \"yes\"\n",
		"bare update number": "[updates]\ncheck_interval = \"24\"\n",
		"negative interval":  "[updates]\nremind_interval = \"-1h\"\n",
		"unknown channel":    "[updates]\nchannel = \"nightly\"\n",
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, content)); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestProtectedBranchSet(t *testing.T) {
	cfg := Default()
	cfg.Policy.ProtectedBranches = []string{"main", " release ", ""}
	set := cfg.ProtectedBranchSet()

	if !set["main"] || !set["release"] {
		t.Errorf("protected branch set = %v", set)
	}
	if set[""] {
		t.Error("empty branch names must be dropped")
	}
	if len(set) != 2 {
		t.Errorf("protected branch set = %v, want 2 entries", set)
	}
}

func TestValidateNormalizesEmptyLogLevel(t *testing.T) {
	cfg := Config{Daemon: DaemonConfig{RequestTimeoutMS: 1}, Claude: ClaudeConfig{HookTimeoutSeconds: 1}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("log.level = %q, want the info default", cfg.Log.Level)
	}
}
