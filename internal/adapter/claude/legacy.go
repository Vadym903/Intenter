package claude

import (
	"sort"
	"strings"
)

// legacy handling for the AgentGuard → Intenter rename
// (specs/005-make-product-usable/contracts/identity-and-rename.md §1 "Hook
// command in Claude settings", §2.3). The old name is never used or trusted —
// only detected so `setup claude` can replace it and `uninstall claude` can
// remove it.

// legacyHookBinary is the executable name the product shipped as before the
// rename.
const legacyHookBinary = "agentguard"

// ownedByLegacy reports whether an entry is a hook the legacy AgentGuard
// binary installed.
//
// The match is exact, mirroring OwnedByAnyIntenter (AG-11): the command must
// run precisely the legacy binary followed by `hook claude` and nothing
// else. A wrapper (`sh -c '…'`), a chained command, a trailing argument, or a
// different base name (`agentguard-helper`) is not claimed — I-9 forbids
// touching a setting Intenter (or its predecessor) did not create.
func ownedByLegacy(entry map[string]any) bool {
	command, _ := entry["command"].(string)
	args, _ := entry["args"].([]any)

	// Exec form: command is the binary, args are exactly ["hook","claude"].
	if len(args) > 0 {
		if len(args) != 2 {
			return false
		}
		first, _ := args[0].(string)
		second, _ := args[1].(string)
		return first == "hook" && second == "claude" && isLegacyBinary(command)
	}
	// Shell form: exactly `<legacy binary> hook claude`.
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) != 3 || fields[1] != "hook" || fields[2] != "claude" {
		return false
	}
	return isLegacyBinary(fields[0])
}

// isLegacyBinary reports whether a token names the legacy executable.
func isLegacyBinary(token string) bool {
	return commandBase(strings.Trim(strings.TrimSpace(token), `"'`)) == legacyHookBinary
}

// LegacyHookEvents reports which events in the Claude settings file still
// carry a legacy AgentGuard hook, for `intenter doctor` to report as a
// leftover (mirrors HooksInstalled, which reports the current identity's
// hooks instead). A missing or unreadable settings file is not an error here:
// the caller's own settings check already reports that in its own terms.
func LegacyHookEvents(settingsPath string) ([]string, error) {
	tree, err := readSettingsTree(settingsPath)
	if err != nil {
		return nil, err
	}
	hooks, ok := tree["hooks"].(map[string]any)
	if !ok {
		return nil, nil
	}

	var events []string
	for event, raw := range hooks {
		groups, ok := raw.([]any)
		if !ok {
			continue
		}
		for _, rawGroup := range groups {
			group, ok := rawGroup.(map[string]any)
			if !ok {
				continue
			}
			entries, _ := group["hooks"].([]any)
			for _, rawEntry := range entries {
				entry, ok := rawEntry.(map[string]any)
				if ok && ownedByLegacy(entry) {
					events = append(events, event)
				}
			}
		}
	}
	sort.Strings(events)
	return events, nil
}

// removeLegacyEntries strips every legacy AgentGuard hook out of a list of
// event groups, dropping a group left with none once its legacy entry is
// gone. Unrelated hooks in the same group, and groups with none of the
// legacy identity, are untouched.
//
// Setup calls this before adding its own entry, so a machine that still has
// the legacy hook installed converges on Intenter's entry alone rather than
// running both.
func removeLegacyEntries(groups []any) []any {
	remaining := make([]any, 0, len(groups))
	for _, raw := range groups {
		group, ok := raw.(map[string]any)
		if !ok {
			remaining = append(remaining, raw)
			continue
		}
		hooks, ok := group["hooks"].([]any)
		if !ok {
			remaining = append(remaining, raw)
			continue
		}

		kept := make([]any, 0, len(hooks))
		for _, rawHook := range hooks {
			hook, ok := rawHook.(map[string]any)
			if ok && ownedByLegacy(hook) {
				continue
			}
			kept = append(kept, rawHook)
		}
		if len(kept) == 0 {
			// The group existed only for the legacy hook.
			continue
		}
		group["hooks"] = kept
		remaining = append(remaining, group)
	}
	return remaining
}
