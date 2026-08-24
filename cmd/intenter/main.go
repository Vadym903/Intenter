// Command intenter is the Intenter executable: CLI, Claude Code hook client
// and per-user daemon in one binary (PROTOTYPE_SPEC.md §6, §9.2, P10).
package main

import (
	"os"

	"github.com/Vadym903/Intenter/internal/cli"
	"github.com/Vadym903/Intenter/internal/platform"
	"github.com/Vadym903/Intenter/internal/updater"
)

func main() {
	// An update on Windows cannot delete the executable it is replacing while
	// that executable is running, so it renames it aside and leaves the file
	// behind. This is the only moment it can be removed: the process that held
	// it is gone, and this one is the new binary (003 contracts §3 step 5).
	if executable, err := platform.SelfExecutablePath(); err == nil {
		updater.CleanStaleReplacements(executable)
	}

	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
