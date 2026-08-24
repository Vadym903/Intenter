package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Environment overrides for the update checker (003 data-model §6). They exist
// so a user can silence checking on a machine — a CI image, a locked-down
// workstation — without editing a file that may be managed elsewhere.
const (
	EnvNoUpdateCheck = "INTENTER_NO_UPDATE_CHECK"
	EnvUpdateChannel = "INTENTER_UPDATE_CHANNEL"
)

// Update channels (003 data-model §6).
const (
	ChannelStable     = "stable"
	ChannelPrerelease = "prerelease"
)

// Config is the effective configuration. Every key is optional and the zero
// installation works without a file (PROTOTYPE_SPEC.md §12.6).
type Config struct {
	Log     LogConfig     `toml:"log"`
	Daemon  DaemonConfig  `toml:"daemon"`
	Policy  PolicyConfig  `toml:"policy"`
	Scope   ScopeConfig   `toml:"scope"`
	Claude  ClaudeConfig  `toml:"claude"`
	Audit   AuditConfig   `toml:"audit"`
	Updates UpdatesConfig `toml:"updates"`

	// Path is the file this configuration was loaded from, empty when defaults
	// were used.
	Path string `toml:"-"`
	// Warnings lists unknown keys and other non-fatal problems.
	Warnings []string `toml:"-"`
}

type LogConfig struct {
	Level string `toml:"level"`
}

type DaemonConfig struct {
	LazyStart        bool `toml:"lazy_start"`
	RequestTimeoutMS int  `toml:"request_timeout_ms"`
}

type PolicyConfig struct {
	AllowReadonlyWorkspace bool     `toml:"allow_readonly_workspace"`
	ProtectedBranches      []string `toml:"protected_branches"`
	SensitivePathsExtra    []string `toml:"sensitive_paths_extra"`
}

type ScopeConfig struct {
	GeneratedDirsExtra []string `toml:"generated_dirs_extra"`
}

type ClaudeConfig struct {
	SettingsPath       string `toml:"settings_path"`
	HookTimeoutSeconds int    `toml:"hook_timeout_seconds"`
	HookConfigChange   bool   `toml:"hook_config_change"`
}

type AuditConfig struct {
	StoreResponseSummary bool `toml:"store_response_summary"`
}

// UpdatesConfig controls the release check and the start-up prompt (003
// data-model §6). The intervals are duration strings rather than numbers so a
// user reading config.toml can tell "24h" from "24" without consulting the
// documentation.
type UpdatesConfig struct {
	// Check is the master switch: false means no network check, no prompt and
	// an immediately-returning start-up path.
	Check bool `toml:"check"`
	// CheckInterval is the minimum time between background checks.
	CheckInterval string `toml:"check_interval"`
	// RemindInterval is the quiet period after "not now" and between prompts.
	RemindInterval string `toml:"remind_interval"`
	// PromptTimeout is how long the start-up prompt waits before it counts as
	// "not now".
	PromptTimeout string `toml:"prompt_timeout"`
	// Channel is "stable" or "prerelease".
	Channel string `toml:"channel"`
	// StartupHook is whether `setup claude` installs the start-up block.
	StartupHook bool `toml:"startup_hook"`
}

// CheckEvery is the parsed check_interval; validation guarantees it parses.
func (u UpdatesConfig) CheckEvery() time.Duration {
	return parseDurationOr(u.CheckInterval, 24*time.Hour)
}

// RemindEvery is the parsed remind_interval.
func (u UpdatesConfig) RemindEvery() time.Duration {
	return parseDurationOr(u.RemindInterval, 24*time.Hour)
}

// PromptWait is the parsed prompt_timeout.
func (u UpdatesConfig) PromptWait() time.Duration {
	return parseDurationOr(u.PromptTimeout, 30*time.Second)
}

// Prerelease reports whether pre-releases are eligible for this installation.
func (u UpdatesConfig) Prerelease() bool { return u.Channel == ChannelPrerelease }

func parseDurationOr(value string, fallback time.Duration) time.Duration {
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

// Default returns the documented defaults (PROTOTYPE_SPEC.md §12.6).
func Default() Config {
	return Config{
		Log: LogConfig{Level: "info"},
		Daemon: DaemonConfig{
			LazyStart:        true,
			RequestTimeoutMS: 5000,
		},
		Policy: PolicyConfig{
			AllowReadonlyWorkspace: true,
			ProtectedBranches:      []string{"main", "master"},
			SensitivePathsExtra:    []string{},
		},
		Scope: ScopeConfig{GeneratedDirsExtra: []string{}},
		Claude: ClaudeConfig{
			HookTimeoutSeconds: 10,
			HookConfigChange:   false,
		},
		Audit: AuditConfig{StoreResponseSummary: true},
		Updates: UpdatesConfig{
			Check:          true,
			CheckInterval:  "24h",
			RemindInterval: "24h",
			PromptTimeout:  "30s",
			Channel:        ChannelStable,
			StartupHook:    true,
		},
	}
}

// Load reads the configuration from path. A missing file yields the defaults.
// Unknown keys produce warnings, never failures; invalid values fail (§12.6).
func Load(path string) (Config, error) {
	cfg := Default()
	if strings.TrimSpace(path) == "" {
		cfg.applyEnv()
		return cfg, nil
	}
	cfg.Path = path

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			cfg.Path = ""
			cfg.applyEnv()
			return cfg, nil
		}
		return cfg, fmt.Errorf("config: read %s: %w", path, err)
	}

	meta, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return Default(), fmt.Errorf("config: parse %s: %w", path, err)
	}

	for _, key := range unknownKeys(meta) {
		cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("unknown configuration key %q in %s (ignored)", key, path))
	}

	if err := cfg.Validate(); err != nil {
		return Default(), err
	}
	cfg.applyEnv()
	return cfg, nil
}

// applyEnv lets the environment override the file for the update settings a
// user may need to change per machine or per shell (003 R-08).
//
// It runs after Validate deliberately: an unusable value in the environment of
// one terminal must not make the whole configuration invalid, so a channel this
// build does not know is ignored with a warning rather than refused.
func (c *Config) applyEnv() {
	if strings.TrimSpace(os.Getenv(EnvNoUpdateCheck)) == "1" {
		c.Updates.Check = false
	}
	channel := strings.ToLower(strings.TrimSpace(os.Getenv(EnvUpdateChannel)))
	switch channel {
	case "":
	case ChannelStable, ChannelPrerelease:
		c.Updates.Channel = channel
	default:
		c.Warnings = append(c.Warnings,
			fmt.Sprintf("%s=%q must be %q or %q (ignored)", EnvUpdateChannel, channel, ChannelStable, ChannelPrerelease))
	}
}

// unknownKeys returns the undecoded keys of a parsed file, dropping table names
// that only exist because one of their own keys is unknown, so a stray
// "[future] enabled = true" warns once rather than twice.
func unknownKeys(meta toml.MetaData) []string {
	all := make([]string, 0, len(meta.Undecoded()))
	for _, key := range meta.Undecoded() {
		all = append(all, key.String())
	}
	sort.Strings(all)

	out := make([]string, 0, len(all))
	for i, key := range all {
		prefix := key + "."
		isParent := false
		for _, other := range all[i+1:] {
			if strings.HasPrefix(other, prefix) {
				isParent = true
				break
			}
		}
		if !isParent {
			out = append(out, key)
		}
	}
	return out
}

// Validate rejects values the daemon cannot work with.
func (c *Config) Validate() error {
	switch strings.ToLower(strings.TrimSpace(c.Log.Level)) {
	case "debug", "info", "warn", "error":
	case "":
		c.Log.Level = "info"
	default:
		return fmt.Errorf("config: log.level %q must be one of debug, info, warn, error", c.Log.Level)
	}

	if c.Daemon.RequestTimeoutMS <= 0 {
		return fmt.Errorf("config: daemon.request_timeout_ms must be positive, got %d", c.Daemon.RequestTimeoutMS)
	}
	if c.Claude.HookTimeoutSeconds <= 0 {
		return fmt.Errorf("config: claude.hook_timeout_seconds must be positive, got %d", c.Claude.HookTimeoutSeconds)
	}
	return c.Updates.validate()
}

// validate checks the update settings. Durations and the channel are rejected
// rather than silently defaulted: a user who wrote `check_interval = "24"`
// meant something by it, and quietly waiting 24 hours instead of the 24
// somethings they intended is the kind of surprise a security tool should not
// spring.
func (u *UpdatesConfig) validate() error {
	for _, field := range []struct {
		key      string
		value    *string
		fallback string
	}{
		{"updates.check_interval", &u.CheckInterval, "24h"},
		{"updates.remind_interval", &u.RemindInterval, "24h"},
		{"updates.prompt_timeout", &u.PromptTimeout, "30s"},
	} {
		// An omitted key is the documented default, the same way an omitted
		// log.level is; only a value the user actually wrote is rejected.
		if strings.TrimSpace(*field.value) == "" {
			*field.value = field.fallback
			continue
		}
		parsed, err := time.ParseDuration(strings.TrimSpace(*field.value))
		if err != nil {
			return fmt.Errorf("config: %s %q is not a duration (e.g. \"24h\", \"30s\")", field.key, *field.value)
		}
		if parsed <= 0 {
			return fmt.Errorf("config: %s must be positive, got %q", field.key, *field.value)
		}
	}

	switch strings.ToLower(strings.TrimSpace(u.Channel)) {
	case ChannelStable, ChannelPrerelease:
		u.Channel = strings.ToLower(strings.TrimSpace(u.Channel))
	case "":
		u.Channel = ChannelStable
	default:
		return fmt.Errorf("config: updates.channel %q must be %q or %q", u.Channel, ChannelStable, ChannelPrerelease)
	}
	return nil
}

// ProtectedBranchSet returns the configured protected branches as a set for
// hard rule R7 (§18.2).
func (c *Config) ProtectedBranchSet() map[string]bool {
	out := make(map[string]bool, len(c.Policy.ProtectedBranches))
	for _, branch := range c.Policy.ProtectedBranches {
		branch = strings.TrimSpace(branch)
		if branch != "" {
			out[branch] = true
		}
	}
	return out
}
