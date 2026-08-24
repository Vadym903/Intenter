package platform

import (
	"fmt"

	"github.com/Vadym903/Intenter/internal/action"
)

// Platform is the single seam through which Intenter touches OS specifics.
// Business logic consumes this interface; runtime.GOOS checks live only here,
// in scope path rules and in dialect selection (PROTOTYPE_SPEC.md §8.1).
type Platform interface {
	// DataDir holds the database, logs, backups and daemon.json.
	DataDir() string
	// ConfigDir holds config.toml.
	ConfigDir() string
	// RuntimeDir holds the socket, pid and lock files.
	RuntimeDir() string
	// HomeDir is the canonical user home.
	HomeDir() string
	// TempDir is the canonical per-user temp directory.
	TempDir() string
	// IPCEndpoint is the default daemon endpoint (§10.1).
	IPCEndpoint() string
	// FindExecutable resolves a program name through PATH, honoring PATHEXT
	// on Windows.
	FindExecutable(name string) (string, error)
	// DefaultShellDialect is the OS's native shell dialect.
	DefaultShellDialect() action.Dialect
	// PathRules describes case sensitivity, system roots, standard home
	// directories, sensitive paths and tool caches (§16).
	PathRules() PathRules
	// SelfExecutablePath is the absolute path of the running binary, used for
	// hook commands, service entries and lazy start.
	SelfExecutablePath() (string, error)
	// OS is the GOOS name, recorded in the audit context.
	OS() string
}

// osPlatform is the single implementation; per-OS behavior comes from
// build-tagged functions in dirs_<os>.go and pathrules_<os>.go.
type osPlatform struct {
	dataDir    string
	configDir  string
	runtimeDir string
	homeDir    string
	tempDir    string
	endpoint   string
	rules      PathRules
}

// Overrides let the CLI's global flags take precedence over the INTENTER_*
// environment variables (contracts/cli.md: --data-dir, --config).
type Overrides struct {
	DataDir    string
	ConfigDir  string
	RuntimeDir string
	Endpoint   string
}

// New builds the platform for the current OS, applying the INTENTER_*
// environment overrides (§8.2).
func New() (Platform, error) { return NewWithOverrides(Overrides{}) }

// NewWithOverrides builds the platform with explicit overrides; empty fields
// fall back to the environment and then to the platform defaults.
func NewWithOverrides(overrides Overrides) (Platform, error) {
	home, err := homeDir()
	if err != nil {
		return nil, fmt.Errorf("platform: cannot determine home directory: %w", err)
	}
	temp := tempDir()

	dirs, err := resolveDirs(home)
	if err != nil {
		return nil, err
	}
	dirs = dirs.apply(overrides)

	p := &osPlatform{
		dataDir:    dirs.data,
		configDir:  dirs.config,
		runtimeDir: dirs.runtime,
		homeDir:    home,
		tempDir:    temp,
	}
	p.endpoint = resolveEndpoint(p.runtimeDir, p.homeDir)
	if overrides.Endpoint != "" {
		p.endpoint = overrides.Endpoint
	}
	p.rules = buildPathRules(home, temp)
	return p, nil
}

func (p *osPlatform) DataDir() string                     { return p.dataDir }
func (p *osPlatform) ConfigDir() string                   { return p.configDir }
func (p *osPlatform) RuntimeDir() string                  { return p.runtimeDir }
func (p *osPlatform) HomeDir() string                     { return p.homeDir }
func (p *osPlatform) TempDir() string                     { return p.tempDir }
func (p *osPlatform) IPCEndpoint() string                 { return p.endpoint }
func (p *osPlatform) PathRules() PathRules                { return p.rules }
func (p *osPlatform) DefaultShellDialect() action.Dialect { return defaultShellDialect() }
func (p *osPlatform) OS() string                          { return goos() }

func (p *osPlatform) FindExecutable(name string) (string, error) { return FindExecutable(name) }

func (p *osPlatform) SelfExecutablePath() (string, error) { return SelfExecutablePath() }
