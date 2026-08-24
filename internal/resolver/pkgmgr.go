package resolver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
)

// DetectPackageManager works out which package manager a workspace uses and
// which shell its scripts would run under (§15.5.4).
func DetectPackageManager(workspace, home string) action.PackageManagerInfo {
	info := action.PackageManagerInfo{Kind: detectManagerKind(workspace)}
	info.ScriptShell, info.ScriptShellSource = detectScriptShell(workspace, home)
	info.YarnPath = readYarnPath(workspace)
	info.PnpmFile = readPnpmFile(workspace)
	return info
}

// detectManagerKind prefers explicit markers over lockfiles.
func detectManagerKind(workspace string) action.PackageManagerKind {
	if workspace == "" {
		return action.PMUnknown
	}

	// yarn-berry announces itself with .yarnrc.yml or packageManager: yarn@2+.
	if fileExists(filepath.Join(workspace, ".yarnrc.yml")) {
		return action.PMYarnBerry
	}
	if declared := readPackageManagerField(workspace); declared != "" {
		name, version, _ := strings.Cut(declared, "@")
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "yarn":
			if isYarnBerryVersion(version) {
				return action.PMYarnBerry
			}
			return action.PMYarnClassic
		case "pnpm":
			return action.PMPnpm
		case "npm":
			return action.PMNpm
		}
	}

	switch {
	case fileExists(filepath.Join(workspace, "pnpm-lock.yaml")):
		return action.PMPnpm
	case fileExists(filepath.Join(workspace, "yarn.lock")):
		return action.PMYarnClassic
	case fileExists(filepath.Join(workspace, "package-lock.json")):
		return action.PMNpm
	case fileExists(filepath.Join(workspace, "npm-shrinkwrap.json")):
		return action.PMNpm
	case fileExists(filepath.Join(workspace, "package.json")):
		return action.PMNpm
	}
	return action.PMUnknown
}

func isYarnBerryVersion(version string) bool {
	version = strings.TrimSpace(version)
	if version == "" {
		return false
	}
	major, _, _ := strings.Cut(version, ".")
	return major != "" && major != "0" && major != "1"
}

// readPackageManagerField returns the `packageManager` field of package.json.
func readPackageManagerField(workspace string) string {
	raw, err := readConfigFile(filepath.Join(workspace, "package.json"))
	if err != nil {
		return ""
	}
	var parsed struct {
		PackageManager string `json:"packageManager"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ""
	}
	return parsed.PackageManager
}

// detectScriptShell reads `script-shell` from the workspace .npmrc and then the
// user's .npmrc; the value decides which dialect resolved scripts are parsed
// with (§15.5.4).
func detectScriptShell(workspace, home string) (value, source string) {
	candidates := []string{}
	if workspace != "" {
		candidates = append(candidates, filepath.Join(workspace, ".npmrc"))
	}
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".npmrc"))
	}

	for _, path := range candidates {
		if shell := readNpmrcKey(path, "script-shell"); shell != "" {
			return shell, path
		}
	}
	return "", ""
}

// readYarnPath returns a workspace's .yarnrc.yml yarnPath override, if set.
//
// yarnPath repoints every `yarn` invocation at a JavaScript file the
// repository ships in place of the installed package manager (the standard
// mechanism Yarn Berry uses to pin its own release, `yarn set version …`).
// Intenter has no way to know what that file does, and it is not fingerprinted
// under any of the keys the npm recognizer already tracks, so its presence
// must make the command unresolved rather than pass an approval scoped to
// what package.json's script text alone says (AG-142, the same class as
// AG-01's unmodeled pager).
func readYarnPath(workspace string) string {
	if workspace == "" {
		return ""
	}
	return readYarnrcKey(filepath.Join(workspace, ".yarnrc.yml"), "yarnPath")
}

// readYarnrcKey reads one top-level `key: value` line from a YAML file the
// lightweight way .npmrc keys are read below; a single scalar value does not
// need a full YAML parser.
func readYarnrcKey(path, key string) string {
	content, err := readConfigFile(path)
	if err != nil {
		return ""
	}

	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), key) {
			return strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	return ""
}

// readPnpmFile returns the path (relative to the workspace) of pnpm's hooks
// file, if one is present: the default .pnpmfile.cjs, or the file .npmrc's
// pnpmfile key names.
//
// Unlike the pre/post lifecycle scripts npmInstall already refuses to resolve
// without --ignore-scripts, pnpm's readPackage/afterAllResolved hooks run
// during dependency resolution itself and are not disabled by
// --ignore-scripts — the flag only skips the installed packages' own
// lifecycle scripts. A pnpmfile is therefore project-supplied Node.js code
// that a real `pnpm install`/`add`/`update` always executes, and Intenter has
// no way to know what it does (AG-144, the same class as AG-142's yarnPath).
func readPnpmFile(workspace string) string {
	if workspace == "" {
		return ""
	}
	name := readNpmrcKey(filepath.Join(workspace, ".npmrc"), "pnpmfile")
	if name == "" {
		name = ".pnpmfile.cjs"
	}
	if !fileExists(filepath.Join(workspace, name)) {
		return ""
	}
	return name
}

// readNpmrcKey reads one key from an .npmrc file.
func readNpmrcKey(path, key string) string {
	content, err := readConfigFile(path)
	if err != nil {
		return ""
	}

	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		name, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), key) {
			return strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	return ""
}

// ScriptDialects returns the dialect(s) a package-manager script must be
// evaluated under, and whether the shell is understood at all (§15.5.4).
//
// INVARIANT I-13: when the interpretation is uncertain — Windows without an
// explicit script-shell — every plausible dialect is returned and the caller
// combines their effects conservatively.
func ScriptDialects(info action.PackageManagerInfo, hostOS string) (dialects []action.Dialect, ok bool) {
	if info.ScriptShell != "" {
		dialect, known := shellDialect(info.ScriptShell)
		if !known {
			return nil, false
		}
		return []action.Dialect{dialect}, true
	}

	// yarn-berry ships its own POSIX-like shell on every platform.
	if info.Kind == action.PMYarnBerry {
		return []action.Dialect{action.DialectPosix}, true
	}

	if hostOS == "windows" {
		// npm, pnpm and yarn-classic run scripts through cmd.exe, but Git Bash
		// may supply the utilities the script calls; evaluate both.
		return []action.Dialect{action.DialectCmd, action.DialectPosix}, true
	}
	return []action.Dialect{action.DialectPosix}, true
}

// shellDialect maps a configured script shell onto a dialect.
//
// Both separators are handled regardless of the host: this decides how a
// Windows script is read, and that has to be answerable from a macOS or Linux
// run for the dual-dialect union to be testable at all (§15.5.4, I-13).
func shellDialect(shell string) (action.Dialect, bool) {
	trimmed := strings.Trim(shell, `"'`)
	if index := strings.LastIndexAny(trimmed, `/\`); index >= 0 {
		trimmed = trimmed[index+1:]
	}
	base := strings.ToLower(trimmed)
	base = strings.TrimSuffix(base, ".exe")
	switch base {
	case "sh", "bash", "zsh", "dash":
		return action.DialectPosix, true
	case "pwsh", "powershell":
		return action.DialectPowerShell, true
	case "cmd":
		return action.DialectCmd, true
	}
	return "", false
}
