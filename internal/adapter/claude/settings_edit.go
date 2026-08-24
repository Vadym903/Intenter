package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Vadym903/Intenter/internal/platform"
)

// HookTimeoutSeconds is what setup writes for each hook entry
// (contracts/claude-hooks.md).
const HookTimeoutSeconds = 10

// HookMatcher selects the tools Intenter gates.
const HookMatcher = "Bash|PowerShell"

// ConfigChangeMatcher selects the settings files a ConfigChange hook watches.
const ConfigChangeMatcher = "user_settings|project_settings|local_settings"

// SessionEndMatcher selects which endings the SessionEnd hook runs for. A
// session's decisions are worth reporting however it ended, and "*" keeps that
// true for reasons Claude has not defined yet.
const SessionEndMatcher = "*"

// MaxBackups is how many settings backups are kept (§12.2 step 2).
const MaxBackups = 10

// hookEvents are the events Intenter always installs a hook for.
var hookEvents = []string{EventPreToolUse, EventPermissionRequest, EventPostToolUse, EventSessionEnd}

// RequiredHookEvents are the events a working installation must have hooks for.
// EventConfigChange is not among them: it is opt-in through configuration.
//
// `doctor` needs this because an installation that predates a new event keeps
// working for everything else, so nothing else would ever notice the gap — the
// feature attached to that event would simply never happen.
func RequiredHookEvents() []string { return append([]string(nil), hookEvents...) }

// MissingHookEvents returns the required events a settings file has no Intenter
// hook for, in the order they are declared.
func MissingHookEvents(settingsPath string) ([]string, error) {
	installed, err := HooksInstalled(settingsPath)
	if err != nil {
		return nil, err
	}
	present := make(map[string]bool, len(installed))
	for _, event := range installed {
		present[event] = true
	}

	missing := make([]string, 0, len(hookEvents))
	for _, event := range hookEvents {
		if !present[event] {
			missing = append(missing, event)
		}
	}
	return missing, nil
}

// BackupSettings copies the settings file before it is modified.
//
// INVARIANT I-9: the file is never modified without a backup first, because
// everything else in it belongs to the user.
func BackupSettings(settingsPath, dataDir string, now time.Time) (string, error) {
	content, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("claude: read %s: %w", settingsPath, err)
	}

	backupDir := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(backupDir, platform.DirMode); err != nil {
		return "", fmt.Errorf("claude: create the backup directory: %w", err)
	}

	name := fmt.Sprintf("claude-settings-%s.json", now.UTC().Format("20060102T150405Z"))
	path := filepath.Join(backupDir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return "", fmt.Errorf("claude: write %s: %w", path, err)
	}

	pruneBackups(backupDir)
	return path, nil
}

// pruneBackups keeps the most recent backups and removes the rest. A failure
// here is not worth failing setup over: the backup that matters was written.
func pruneBackups(backupDir string) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "claude-settings-") {
			names = append(names, entry.Name())
		}
	}
	if len(names) <= MaxBackups {
		return
	}
	// The timestamp format sorts chronologically as text.
	sort.Strings(names)
	for _, name := range names[:len(names)-MaxBackups] {
		_ = os.Remove(filepath.Join(backupDir, name))
	}
}

// HookCommand is the command line a hook entry runs.
type HookCommand struct {
	// Command is the executable, quoted where the shell form needs it.
	Command string
	// Args is set only for the exec form, which Windows requires.
	Args []string
}

// NewHookCommand builds the hook invocation for a platform (§11.7).
//
// Windows uses the exec form because the same shell-quoted string is read
// differently by Git Bash and PowerShell; every other platform uses the shell
// form with a quoted absolute path.
func NewHookCommand(executable, hostOS string) HookCommand {
	if hostOS == "windows" {
		return HookCommand{Command: executable, Args: []string{"hook", "claude"}}
	}
	return HookCommand{Command: fmt.Sprintf("%q hook claude", executable)}
}

// ExecutablePath is the binary this hook runs, without the quoting the shell
// form needs or the `hook claude` arguments that follow it.
//
// Callers use it to notice a hook left pointing at a binary an upgrade moved,
// so it reads what Claude would actually execute rather than what Intenter
// last wrote.
func (c HookCommand) ExecutablePath() string {
	if len(c.Args) > 0 {
		return strings.Trim(strings.TrimSpace(c.Command), `"'`)
	}
	command := strings.TrimSpace(c.Command)
	command = strings.TrimSpace(strings.TrimSuffix(command, "hook claude"))
	return strings.Trim(command, `"'`)
}

// entry renders the hook entry as the generic JSON tree Claude expects.
func (c HookCommand) entry() map[string]any {
	entry := map[string]any{
		"type":    "command",
		"command": c.Command,
		"timeout": HookTimeoutSeconds,
	}
	if len(c.Args) > 0 {
		args := make([]any, 0, len(c.Args))
		for _, arg := range c.Args {
			args = append(args, arg)
		}
		entry["args"] = args
	}
	return entry
}

// owns reports whether a hook entry is one Intenter installed.
//
// Ownership is decided by what the entry runs — the Intenter executable
// followed by `hook claude` — rather than by a marker key, so nothing outside
// Claude's own settings schema is written (§11.7).
func owns(entry map[string]any, executable string) bool {
	command, _ := entry["command"].(string)
	if command == "" {
		return false
	}

	args := make([]string, 0, 2)
	if raw, ok := entry["args"].([]any); ok {
		for _, item := range raw {
			if text, ok := item.(string); ok {
				args = append(args, text)
			}
		}
	}

	// Exec form: the command is the executable and the args say `hook claude`.
	if len(args) >= 2 {
		return sameExecutable(command, executable) && args[0] == "hook" && args[1] == "claude"
	}
	// Shell form: a quoted or bare path followed by `hook claude`.
	trimmed := strings.TrimSpace(command)
	if !strings.HasSuffix(trimmed, "hook claude") {
		return false
	}
	path := strings.TrimSpace(strings.TrimSuffix(trimmed, "hook claude"))
	return sameExecutable(strings.Trim(path, `"'`), executable)
}

// sameExecutable compares two executable paths, tolerating the case-insensitive
// filesystems where a hook may have been written with different casing. A
// binary that moved is deliberately *not* the same: setup replaces that entry.
func sameExecutable(candidate, executable string) bool {
	candidate = filepath.Clean(strings.Trim(candidate, `"'`))
	executable = filepath.Clean(executable)
	if candidate == executable {
		return true
	}
	return strings.EqualFold(candidate, executable)
}

// OwnedByAnyIntenter reports whether an entry is an Intenter hook,
// regardless of which binary path it names. Uninstall uses it so a hook left by
// a binary that has since moved is still removed.
//
// The match is exact rather than a suffix test: the entry must run precisely
// the Intenter binary followed by `hook claude` and nothing else. A suffix
// test would claim a user's own hook that happens to end in `hook claude`, or
// one that chains another command before it — and I-9 forbids touching a
// setting Intenter did not create.
func OwnedByAnyIntenter(entry map[string]any) bool {
	command, _ := entry["command"].(string)
	args, _ := entry["args"].([]any)

	// Exec form: command is the binary, args are exactly ["hook","claude"].
	if len(args) > 0 {
		if len(args) != 2 {
			return false
		}
		first, _ := args[0].(string)
		second, _ := args[1].(string)
		return first == "hook" && second == "claude" && isIntenterBinary(command)
	}
	// Shell form: exactly `<intenter binary> hook claude`.
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) != 3 || fields[1] != "hook" || fields[2] != "claude" {
		return false
	}
	return isIntenterBinary(fields[0])
}

// isIntenterBinary reports whether a token names the Intenter executable.
func isIntenterBinary(token string) bool {
	return commandBase(strings.Trim(strings.TrimSpace(token), `"'`)) == "intenter"
}

// commandBase is the executable name of a path, ignoring directory and
// extension.
//
// Both separators are handled regardless of the host: a settings file written
// on Windows has to be readable by a test running on macOS or Linux, which is
// the only way the Windows exec form gets covered at all.
func commandBase(path string) string {
	if index := strings.LastIndexAny(path, `/\`); index >= 0 {
		path = path[index+1:]
	}
	return strings.TrimSuffix(strings.ToLower(path), ".exe")
}

// InstallHooks adds or refreshes Intenter's hook entries, leaving every other
// key, hook and matcher exactly as it was (INVARIANT I-9).
//
// The settings file is edited as a generic JSON tree rather than a typed
// struct: anything Intenter does not model would otherwise be dropped on
// write.
func InstallHooks(settingsPath, executable, hostOS string, configChange bool) error {
	tree, err := readSettingsTree(settingsPath)
	if err != nil {
		return err
	}

	command := NewHookCommand(executable, hostOS)
	events := append([]string(nil), hookEvents...)
	if configChange {
		events = append(events, EventConfigChange)
	}

	hooks := childObject(tree, "hooks")
	for _, event := range events {
		hooks[event] = installEntry(hooks[event], matcherFor(event), command, executable)
	}
	tree["hooks"] = hooks

	return writeSettingsTree(settingsPath, tree)
}

// matcherFor is what an event's matcher selects on. The tool events filter by
// tool name; the others filter by something of their own, and using the tool
// matcher there would install a hook that can never fire.
func matcherFor(event string) string {
	switch event {
	case EventConfigChange:
		return ConfigChangeMatcher
	case EventSessionEnd:
		return SessionEndMatcher
	default:
		return HookMatcher
	}
}

// installEntry adds Intenter's hook to one event's group list, replacing a
// stale entry of its own, removing any entry left by the legacy identity
// (legacy.go), and leaving the user's alone.
func installEntry(existing any, matcher string, command HookCommand, executable string) []any {
	groups, _ := existing.([]any)
	entry := command.entry()

	// A machine upgrading from the legacy identity must end with only
	// Intenter's entry: the legacy hook is removed before we look for our own.
	groups = removeLegacyEntries(groups)

	for _, raw := range groups {
		group, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		hooks, ok := group["hooks"].([]any)
		if !ok {
			continue
		}
		for i, rawHook := range hooks {
			hook, ok := rawHook.(map[string]any)
			if !ok {
				continue
			}
			if owns(hook, executable) {
				// Already ours: refresh it in place, keeping the surrounding
				// group and any other hooks in it untouched.
				hooks[i] = entry
				group["hooks"] = hooks
				return groups
			}
			if OwnedByAnyIntenter(hook) {
				// Ours, but from a binary that has moved.
				hooks[i] = entry
				group["hooks"] = hooks
				return groups
			}
		}
	}

	return append(groups, map[string]any{
		"matcher": matcher,
		"hooks":   []any{entry},
	})
}

// RemoveHooks deletes Intenter's own entries and any left by the legacy
// identity (legacy.go), and any group left empty by doing so (§12.3).
func RemoveHooks(settingsPath string) error {
	tree, err := readSettingsTree(settingsPath)
	if err != nil {
		return err
	}

	hooks, ok := tree["hooks"].(map[string]any)
	if !ok {
		return nil
	}

	for event, raw := range hooks {
		groups, ok := raw.([]any)
		if !ok {
			continue
		}
		remaining := make([]any, 0, len(groups))
		for _, rawGroup := range groups {
			group, ok := rawGroup.(map[string]any)
			if !ok {
				remaining = append(remaining, rawGroup)
				continue
			}
			entries, ok := group["hooks"].([]any)
			if !ok {
				remaining = append(remaining, rawGroup)
				continue
			}

			kept := make([]any, 0, len(entries))
			for _, rawEntry := range entries {
				entry, ok := rawEntry.(map[string]any)
				if ok && (OwnedByAnyIntenter(entry) || ownedByLegacy(entry)) {
					continue
				}
				kept = append(kept, rawEntry)
			}
			if len(kept) == 0 {
				// The group existed only for Intenter's hook (or the legacy
				// identity's one).
				continue
			}
			group["hooks"] = kept
			remaining = append(remaining, group)
		}

		if len(remaining) == 0 {
			delete(hooks, event)
			continue
		}
		hooks[event] = remaining
	}

	if len(hooks) == 0 {
		delete(tree, "hooks")
	} else {
		tree["hooks"] = hooks
	}
	return writeSettingsTree(settingsPath, tree)
}

// HooksInstalled reports which events currently carry an Intenter hook.
func HooksInstalled(settingsPath string) ([]string, error) {
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
				if ok && OwnedByAnyIntenter(entry) {
					events = append(events, event)
				}
			}
		}
	}
	sort.Strings(events)
	return events, nil
}

// InstalledHookCommand returns the exact command line one installed hook runs,
// which is what the self-test executes (§12.2 step 7).
func InstalledHookCommand(settingsPath, event string) (HookCommand, bool) {
	tree, err := readSettingsTree(settingsPath)
	if err != nil {
		return HookCommand{}, false
	}
	hooks, ok := tree["hooks"].(map[string]any)
	if !ok {
		return HookCommand{}, false
	}
	groups, ok := hooks[event].([]any)
	if !ok {
		return HookCommand{}, false
	}

	for _, rawGroup := range groups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			continue
		}
		entries, _ := group["hooks"].([]any)
		for _, rawEntry := range entries {
			entry, ok := rawEntry.(map[string]any)
			if !ok || !OwnedByAnyIntenter(entry) {
				continue
			}
			command, _ := entry["command"].(string)
			result := HookCommand{Command: command}
			if raw, ok := entry["args"].([]any); ok {
				for _, item := range raw {
					if text, ok := item.(string); ok {
						result.Args = append(result.Args, text)
					}
				}
			}
			return result, true
		}
	}
	return HookCommand{}, false
}

// readSettingsTree parses the settings file as a generic tree. Invalid JSON is
// an error, never something to overwrite: the file is the user's (§12.2 step 3).
func readSettingsTree(path string) (map[string]any, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("claude: read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(content))) == 0 {
		return map[string]any{}, nil
	}

	var tree map[string]any
	if err := json.Unmarshal(content, &tree); err != nil {
		return nil, fmt.Errorf(
			"claude: %s is not valid JSON, so Intenter will not modify it: %w", path, err)
	}
	if tree == nil {
		tree = map[string]any{}
	}
	return tree, nil
}

// writeSettingsTree writes the settings atomically, so an interrupted write
// never leaves the user without their configuration.
func writeSettingsTree(path string, tree map[string]any) error {
	encoded, err := json.MarshalIndent(tree, "", "  ")
	if err != nil {
		return fmt.Errorf("claude: encode settings: %w", err)
	}
	encoded = append(encoded, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("claude: create the settings directory: %w", err)
	}

	temp, err := os.CreateTemp(filepath.Dir(path), ".intenter-settings-*")
	if err != nil {
		return fmt.Errorf("claude: create a temporary settings file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if _, err := temp.Write(encoded); err != nil {
		temp.Close()
		return fmt.Errorf("claude: write the settings: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("claude: close the temporary settings file: %w", err)
	}
	if err := os.Chmod(tempPath, 0o600); err != nil {
		return fmt.Errorf("claude: set the settings permissions: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("claude: replace %s: %w", path, err)
	}
	return nil
}

// childObject returns a nested object, creating it when absent.
func childObject(tree map[string]any, key string) map[string]any {
	if existing, ok := tree[key].(map[string]any); ok {
		return existing
	}
	return map[string]any{}
}
