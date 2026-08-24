package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Vadym903/Intenter/internal/adapter/claude"
	"github.com/Vadym903/Intenter/internal/platform"
	"github.com/Vadym903/Intenter/internal/storage"
)

// This file, like internal/platform/legacy.go and
// internal/adapter/claude/legacy.go, is one of the places the pre-rename
// product's identifiers may appear (contracts/identity-and-rename.md §2,
// enforced by scripts/check-rename.sh): only to report a leftover, never to
// use it.

// checkLegacyLeftovers reports every trace of a pre-rename AgentGuard
// installation still on this machine — an unmigrated data/config/runtime
// directory, a still-registered service, a stray binary on PATH, or a legacy
// hook entry Claude's settings still carry
// (contracts/identity-and-rename.md §1, §2.3–2.5). Every other check in
// `doctor` assumes the rename finished; this is the one that looks for what
// came before it.
func checkLegacyLeftovers(_ context.Context, app *App, meta map[string]string) Check {
	const name = "Pre-rename install"

	var details []string
	var fixes []string

	for _, leftover := range platform.LegacyLeftovers(app.Platform) {
		fix := leftover.Fix
		if leftover.Kind == platform.LegacyKindBinary {
			// A stray pre-rename executable on PATH is not something setup or
			// the daemon migrate on their own like the directories and
			// service registration are — only removing it fixes it.
			fix = fmt.Sprintf("remove %s (the pre-rename binary)", leftover.Path)
		}
		details = append(details, fmt.Sprintf("%s: %s", leftover.Kind, leftover.Path))
		fixes = append(fixes, fix)
	}

	settingsPath := meta[storage.MetaClaudeSettingsPath]
	if settingsPath == "" {
		settingsPath = filepath.Join(app.Platform.HomeDir(), ".claude", "settings.json")
	}
	if events, err := claude.LegacyHookEvents(settingsPath); err == nil && len(events) > 0 {
		details = append(details, fmt.Sprintf("hook entries: %s in %s", strings.Join(events, ", "), settingsPath))
		fixes = append(fixes, "run `intenter setup claude` (replaces them)")
	}

	if len(details) == 0 {
		return Check{Name: name, OK: true, Detail: "none found"}
	}
	return Check{
		Name:   name,
		Detail: strings.Join(details, "; "),
		Fix:    strings.Join(fixes, "; "),
	}
}
