package updater

import "path/filepath"

// legacyMarkerBegin and legacyMarkerEnd delimited the same block under the
// product's previous name, AgentGuard. They are recognized only to be
// replaced or removed — never written, and never read for anything but
// finding the block to clean up (identity-and-rename.md §2.3).
const (
	legacyMarkerBegin = "# >>> agentguard:update-check >>>"
	legacyMarkerEnd   = "# <<< agentguard:update-check <<<"
)

// legacyFishBlockFile is the fish drop-in AgentGuard installed for the same
// purpose fishBlockFile serves now.
const legacyFishBlockFile = "agentguard-update.fish"

// legacyCandidates mirrors allCandidates, but for the files AgentGuard used to
// hold the block under its old name. Every shell rc file and PowerShell
// profile is the same physical file as today — only the markers inside it
// changed — so only the fish drop-in, which is named after the product, has a
// different path.
func (s *StartupCheck) legacyCandidates() []target {
	candidates := s.allCandidates()
	out := make([]target, 0, len(candidates))
	for _, c := range candidates {
		if c.shell == ShellFish {
			c.path = filepath.Join(s.Home, ".config", "fish", "conf.d", legacyFishBlockFile)
		}
		out = append(out, c)
	}
	return out
}

// removeLegacyBlock strips the block AgentGuard installed, under its old
// markers, from every file it could be in, and deletes the legacy fish
// drop-in if removing the block leaves nothing but blank lines in it — the
// same rule removeBlock applies to the current one. It returns the files that
// were changed or removed.
func (s *StartupCheck) removeLegacyBlock() ([]string, error) {
	removed := make([]string, 0, 4)
	for _, t := range s.legacyCandidates() {
		gone, err := s.removeBlockMarked(t, legacyMarkerBegin, legacyMarkerEnd)
		if err != nil {
			return removed, err
		}
		if gone {
			removed = append(removed, t.path)
		}
	}
	return removed, nil
}
