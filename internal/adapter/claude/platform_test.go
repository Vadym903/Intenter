package claude

import (
	"fmt"
	"path/filepath"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/platform"
)

// fakePlatform is a platform whose directories are all under the test's own
// temp tree, so the adapter never touches real user state.
type fakePlatform struct {
	home       string
	dataDir    string
	runtimeDir string
	os         string
	executable string
}

func (p fakePlatform) DataDir() string {
	if p.dataDir != "" {
		return p.dataDir
	}
	return filepath.Join(p.home, ".intenter")
}

func (p fakePlatform) ConfigDir() string { return filepath.Join(p.home, ".config", "intenter") }

func (p fakePlatform) RuntimeDir() string {
	if p.runtimeDir != "" {
		return p.runtimeDir
	}
	return filepath.Join(p.home, ".intenter", "run")
}

func (p fakePlatform) HomeDir() string     { return p.home }
func (p fakePlatform) TempDir() string     { return filepath.Join(p.home, "tmp") }
func (p fakePlatform) IPCEndpoint() string { return filepath.Join(p.RuntimeDir(), "intenter.sock") }

func (p fakePlatform) FindExecutable(name string) (string, error) {
	return "", fmt.Errorf("fakePlatform: %s not found", name)
}

func (p fakePlatform) DefaultShellDialect() action.Dialect {
	if p.os == "windows" {
		return action.DialectPowerShell
	}
	return action.DialectPosix
}

func (p fakePlatform) PathRules() platform.PathRules { return platform.PathRules{} }

func (p fakePlatform) SelfExecutablePath() (string, error) {
	if p.executable == "" {
		return "", fmt.Errorf("fakePlatform: no executable path")
	}
	return p.executable, nil
}

func (p fakePlatform) OS() string {
	if p.os == "" {
		return "darwin"
	}
	return p.os
}
