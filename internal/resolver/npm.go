package resolver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/parser"
)

// NpmRecognizer models npm, pnpm, yarn and npx (§15.4, §15.5.1). Scripts are
// resolved to the commands they actually run, and every file that decided the
// outcome is fingerprinted so the approval stops matching when it changes.
func NpmRecognizer() Recognizer { return npmRecognizer{} }

type npmRecognizer struct{}

func (npmRecognizer) Names() []string { return []string{"npm", "pnpm", "yarn", "npx"} }

// npmInstallSubcommands write node_modules and run lifecycle scripts.
var npmInstallSubcommands = map[string]bool{
	"install": true, "i": true, "ci": true, "add": true, "update": true,
	"up": true, "upgrade": true, "uninstall": true, "remove": true, "rm": true,
	"install-test": true, "it": true,
}

// npmLifecycleScripts map a bare subcommand onto the script it runs.
var npmLifecycleScripts = map[string]string{
	"test": "test", "t": "test", "tst": "test",
	"start": "start", "stop": "stop", "restart": "restart",
}

// npmBuiltins are subcommands that are never a package script, so the
// `pnpm <script>` / `yarn <script>` shorthand must not claim them.
var npmBuiltins = map[string]bool{
	"access": true, "adduser": true, "audit": true, "bin": true, "bugs": true,
	"cache": true, "config": true, "create": true, "dedupe": true,
	"deprecate": true, "dist-tag": true, "docs": true, "doctor": true,
	"edit": true, "explore": true, "fund": true, "help": true, "hook": true,
	"info": true, "init": true, "link": true, "list": true, "login": true,
	"logout": true, "ls": true, "org": true, "outdated": true, "owner": true,
	"pack": true, "patch": true, "ping": true, "prefix": true, "profile": true,
	"prune": true, "publish": true, "rebuild": true, "repo": true,
	"root": true, "search": true, "set": true, "star": true, "store": true,
	"team": true, "token": true, "unlink": true, "unpublish": true,
	"unstar": true, "version": true, "view": true, "whoami": true, "why": true,
	"workspace": true, "workspaces": true, "import": true, "licenses": true,
	"env": true, "server": true, "setup": true,
}

// npmWorkspaceFlags select a different package than the one the cwd resolves
// to, which Intenter does not model (§15.4).
var npmWorkspaceFlags = []string{
	"-w", "--workspace", "--workspaces", "--filter", "-r", "--recursive",
	"--if-present", "--include-workspace-root", "-F",
}

// npmDirectoryFlags point the manager at a project Intenter did not resolve
// the manifest from: `nearestPackageJSON` walks up from the shell's cwd, and
// none of these is a `cd`, so a real npm/pnpm/yarn invocation could read and
// run a completely different package.json than the one Intenter examined
// (AG-140, the same class as AG-05's `curl --resolve`).
var npmDirectoryFlags = []string{
	"--prefix", "-C", "--dir", "--cwd",
}

// npmConfigOverrideFlags replace the .npmrc files Intenter reads to decide the
// script-shell dialect, the registry and other behavior, with a source it
// never reads or fingerprints (§15.5.4).
var npmConfigOverrideFlags = []string{
	"--userconfig", "--globalconfig", "--script-shell", "--registry",
}

func (npmRecognizer) Recognize(req Request) action.ResolvedCommand {
	manager := npmManagerName(req.Command.Name())

	if manager == "yarn" {
		if path := npmPackageManagerInfo(req).YarnPath; path != "" {
			return Unresolved(req, action.OpUnknown, fmt.Sprintf(
				".yarnrc.yml pins yarn to %s, a project-supplied implementation Intenter cannot model", path))
		}
	}
	if manager == "pnpm" {
		if path := npmPackageManagerInfo(req).PnpmFile; path != "" {
			return Unresolved(req, action.OpUnknown, fmt.Sprintf(
				"%s hooks run on every pnpm command regardless of --ignore-scripts, and Intenter cannot model what they do", path))
		}
	}

	args := req.Command.Args()

	if manager == "npx" {
		return npmExec(req, "npx", args)
	}
	if len(args) == 0 {
		return Unresolved(req, action.OpUnknown, manager+" was called without a subcommand")
	}

	subcommand := args[0].Text
	rest := args[1:]

	switch {
	case npmInstallSubcommands[subcommand]:
		return npmInstall(req, manager, subcommand, rest)

	case subcommand == "exec" || subcommand == "dlx":
		return npmExec(req, manager+" "+subcommand, rest)

	case subcommand == "run" || subcommand == "run-script":
		if len(rest) == 0 {
			return Unresolved(req, action.OpRunScript, manager+" run was called without a script name")
		}
		return npmRunScript(req, manager, rest[0].Text, rest[1:])

	case npmLifecycleScripts[subcommand] != "":
		return npmRunScript(req, manager, npmLifecycleScripts[subcommand], rest)

	case npmBuiltins[subcommand]:
		return Unresolved(req, action.OpUnknown, fmt.Sprintf(
			"%s %s is not a subcommand Intenter models", manager, subcommand))

	case manager == "pnpm" || manager == "yarn":
		// pnpm and yarn run a package script when the name is not a builtin.
		return npmRunScript(req, manager, subcommand, rest)
	}

	return Unresolved(req, action.OpUnknown, fmt.Sprintf(
		"%s %s is not a subcommand Intenter models", manager, subcommand))
}

// npmRunScript resolves a package script to the commands it actually runs
// (§15.5.1).
func npmRunScript(req Request, manager, scriptName string, rest []parser.Word) action.ResolvedCommand {
	out := resolved(req, action.OpRunScript)

	if flag, ok := npmFlagPresent(rest, npmWorkspaceFlags); ok {
		return Unresolved(req, action.OpRunScript, fmt.Sprintf(
			"%s selects another workspace package, which Intenter does not model", flag))
	}
	if flag, ok := npmFlagPresent(rest, npmDirectoryFlags); ok {
		return Unresolved(req, action.OpRunScript, fmt.Sprintf(
			"%s changes which project's manifest is used, which Intenter does not model", flag))
	}
	if flag, ok := npmFlagPresent(rest, npmConfigOverrideFlags); ok {
		return Unresolved(req, action.OpRunScript, fmt.Sprintf(
			"%s overrides npm configuration Intenter reads from disk, which Intenter does not model", flag))
	}

	workspace := req.Workspace()
	if workspace == "" {
		return Unresolved(req, action.OpRunScript, "no workspace root was established for this request")
	}

	manifestPath, ok := nearestPackageJSON(req.Command.EffectiveCwd, workspace)
	if !ok {
		return Unresolved(req, action.OpRunScript,
			"no package.json was found between the working directory and the workspace root")
	}
	manifest, err := readPackageManifest(manifestPath)
	if err != nil {
		return Unresolved(req, action.OpRunScript, "package.json could not be read: "+err.Error())
	}

	relative := relativeKey(workspace, manifestPath)
	cycleKey := relative + "#" + scriptName
	if req.InChain(cycleKey) {
		return Unresolved(req, action.OpRunScript, fmt.Sprintf(
			"the %s script calls itself in a loop", scriptName))
	}

	if _, defined := manifest.Scripts[scriptName]; !defined {
		return Unresolved(req, action.OpRunScript, fmt.Sprintf(
			"%s defines no %q script", relative, scriptName))
	}

	dialects, understood := ScriptDialects(npmPackageManagerInfo(req), npmHostOS(req))
	if !understood {
		return Unresolved(req, action.OpRunScript, fmt.Sprintf(
			"the configured script-shell %q is not a shell Intenter understands",
			npmPackageManagerInfo(req).ScriptShell))
	}

	// The configuration that decided how the script is interpreted is part of
	// what was approved (§15.5.1 step 5).
	info := npmPackageManagerInfo(req)
	out.Fingerprints = action.MergeFingerprints(out.Fingerprints,
		ValueFingerprint(NpmScriptShellKey(), info.ScriptShell, "npm script-shell"),
		ValueFingerprint(NpmPackageManagerKey(), manifest.PackageManager, "package.json packageManager"))

	manifestDir := filepath.Dir(manifestPath)
	passthrough := npmPassthrough(rest)

	for _, stage := range npmScriptStages(manifest, scriptName) {
		text := manifest.Scripts[stage]
		if stage == scriptName && passthrough != "" {
			text += " " + passthrough
		}

		out.Fingerprints = action.MergeFingerprints(out.Fingerprints,
			TextFingerprint(NpmScriptKey(relative, stage), manifest.Scripts[stage],
				fmt.Sprintf("%s script %s", relative, stage)))

		label := fmt.Sprintf("%s run %s", manager, stage)
		result := req.ResolveScript(Script{
			Text:     text,
			Cwd:      manifestDir,
			Dialects: dialects,
			Label:    label,
			Key:      relative + "#" + stage,
		})

		out.Children = append(out.Children, result.Commands...)
		out.ResolvedFrom = append(out.ResolvedFrom, label+" -> "+text)
		if result.Status != "" && result.Status != action.StatusResolved {
			out.Status = action.WeakerStatus(out.Status, result.Status)
			if out.StatusReason == "" {
				out.StatusReason = result.StatusReason
			}
		}
	}

	npmAbsorbChildren(&out)
	return out
}

// npmScriptStages lists the scripts a run executes, in order: pre, the script
// itself, then post. Over-approximating with pre/post for every manager is safe
// (§15.5.1 step 3).
func npmScriptStages(manifest *packageManifest, scriptName string) []string {
	stages := make([]string, 0, 3)
	if _, ok := manifest.Scripts["pre"+scriptName]; ok {
		stages = append(stages, "pre"+scriptName)
	}
	stages = append(stages, scriptName)
	if _, ok := manifest.Scripts["post"+scriptName]; ok {
		stages = append(stages, "post"+scriptName)
	}
	return stages
}

// npmAbsorbChildren lifts the effects, targets and fingerprints of the resolved
// script into the wrapper, so policy sees one action.
func npmAbsorbChildren(out *action.ResolvedCommand) {
	for _, child := range out.Children {
		out.Effects = append(out.Effects, child.Effects...)
		for _, target := range child.Targets {
			appendTarget(out, target)
		}
		out.Fingerprints = action.MergeFingerprints(out.Fingerprints, child.Fingerprints...)
		out.Status = action.WeakerStatus(out.Status, child.Status)
		if out.StatusReason == "" && child.Status != action.StatusResolved {
			out.StatusReason = child.StatusReason
		}
	}
}

// npmInstall models the dependency-install family (§15.4). Lifecycle scripts of
// the installed packages are arbitrary code, so the action only resolves with
// --ignore-scripts.
func npmInstall(req Request, manager, subcommand string, rest []parser.Word) action.ResolvedCommand {
	out := resolved(req, action.OpInstallDependencies)
	out.Status = action.StatusDeclared

	if flag, ok := npmFlagPresent(rest, npmWorkspaceFlags); ok {
		return Unresolved(req, action.OpInstallDependencies, fmt.Sprintf(
			"%s selects another workspace package, which Intenter does not model", flag))
	}
	if flag, ok := npmFlagPresent(rest, npmDirectoryFlags); ok {
		return Unresolved(req, action.OpInstallDependencies, fmt.Sprintf(
			"%s changes which project's manifest is used, which Intenter does not model", flag))
	}
	if flag, ok := npmFlagPresent(rest, npmConfigOverrideFlags); ok {
		return Unresolved(req, action.OpInstallDependencies, fmt.Sprintf(
			"%s overrides npm configuration Intenter reads from disk, which Intenter does not model", flag))
	}

	workspace := req.Workspace()
	if workspace == "" {
		return Unresolved(req, action.OpInstallDependencies, "no workspace root was established for this request")
	}
	manifestPath, ok := nearestPackageJSON(req.Command.EffectiveCwd, workspace)
	if !ok {
		return Unresolved(req, action.OpInstallDependencies,
			"no package.json was found between the working directory and the workspace root")
	}
	manifestDir := filepath.Dir(manifestPath)

	out.Effects = append(out.Effects, action.Effect{
		Type:    action.EffectNetwork,
		Network: &action.NetworkTarget{DeclaredKind: "dependency-registry"},
	})

	if target, ok := req.PathTarget(filepath.Join(manifestDir, "node_modules")); ok {
		addEffect(&out, target, action.EffectCreate, action.EffectFlagRecursive)
		addEffect(&out, target, action.EffectWrite, action.EffectFlagRecursive)
	}
	if target, ok := req.PathTarget(manifestPath); ok {
		addEffect(&out, target, action.EffectWrite)
	}
	for _, lockfile := range []string{"package-lock.json", "pnpm-lock.yaml", "yarn.lock", "npm-shrinkwrap.json"} {
		if !fileExists(filepath.Join(manifestDir, lockfile)) {
			continue
		}
		if target, ok := req.PathTarget(filepath.Join(manifestDir, lockfile)); ok {
			addEffect(&out, target, action.EffectWrite)
		}
	}

	if !npmHasFlag(rest, "--ignore-scripts") {
		out.Effects = append(out.Effects, action.Effect{
			Type:    action.EffectExecute,
			Program: &action.ProgramRef{Name: manager + " lifecycle scripts", Resolution: action.ProgramUnresolved},
		})
		degrade(&out, fmt.Sprintf(
			"%s %s runs the lifecycle scripts of every installed package; --ignore-scripts avoids that",
			manager, subcommand))
	}
	return out
}

// npmExecSafeFlags never change which binary npx/dlx invokes or what
// arguments it receives, so they may be dropped before the package name.
// Anything else appearing before the package name is refused rather than
// silently ignored — that previously let `--prefix`/`--workspace`/etc. through
// unexamined (AG-141, the denylist-vs-allowlist pattern of §4.2). Everything
// from the package name onward is kept verbatim: it is the invoked program's
// own arguments, and dropping option-shaped ones there meant the text handed
// to that program's recognizer did not match what would really run.
var npmExecSafeFlags = []string{"-y", "--yes", "--no-install"}

// npmExec models npx/`npm exec`/`pnpm dlx`/`yarn dlx`. Only a binary already
// present in node_modules is known not to be downloaded first (§15.4).
func npmExec(req Request, label string, rest []parser.Word) action.ResolvedCommand {
	var name parser.Word
	var invocation []parser.Word
	found := false
	endOfOptions := false

	for i, word := range rest {
		if !endOfOptions && word.Text == "--" {
			endOfOptions = true
			continue
		}
		if !endOfOptions && isOption(word.Text) {
			bare, _, _ := strings.Cut(word.Text, "=")
			if !contains(npmExecSafeFlags, bare) {
				return Unresolved(req, action.OpUnknown, fmt.Sprintf(
					"%s %s is a flag Intenter does not model", label, word.Text))
			}
			continue
		}
		name = word
		invocation = rest[i+1:]
		found = true
		break
	}
	if !found {
		return Unresolved(req, action.OpUnknown, label+" was called without a package")
	}

	workspace := req.Workspace()
	if workspace == "" {
		return Unresolved(req, action.OpUnknown, "no workspace root was established for this request")
	}
	manifestPath, ok := nearestPackageJSON(req.Command.EffectiveCwd, workspace)
	if !ok {
		return Unresolved(req, action.OpUnknown,
			"no package.json was found between the working directory and the workspace root")
	}

	binary := filepath.Join(filepath.Dir(manifestPath), "node_modules", ".bin", name.Text)
	if !npmLocalBinaryExists(binary) {
		return Unresolved(req, action.OpUnknown, fmt.Sprintf(
			"%s %s would download and run a package that is not installed locally", label, name.Text))
	}

	// The local binary is a normal invocation; resolving it is the pipeline's
	// job, reached through the script resolver so depth and limits still apply.
	out := resolved(req, action.OpRunScript)
	text := strings.Join(npmWordTexts(append([]parser.Word{name}, invocation...)), " ")
	result := req.ResolveScript(Script{
		Text:     text,
		Cwd:      req.Command.EffectiveCwd,
		Dialects: []action.Dialect{req.Dialect},
		Label:    label + " " + name.Text,
		Key:      "exec:" + binary,
	})
	out.Children = append(out.Children, result.Commands...)
	out.ResolvedFrom = append(out.ResolvedFrom, label+" "+name.Text+" -> "+text)
	if result.Status != "" && result.Status != action.StatusResolved {
		out.Status = action.WeakerStatus(out.Status, result.Status)
		out.StatusReason = result.StatusReason
	}
	npmAbsorbChildren(&out)
	return out
}

// packageManifest is the part of package.json Intenter reads.
type packageManifest struct {
	Scripts        map[string]string `json:"scripts"`
	PackageManager string            `json:"packageManager"`
}

// readPackageManifest parses package.json, tolerating a missing scripts block.
func readPackageManifest(path string) (*packageManifest, error) {
	content, err := readConfigFile(path)
	if err != nil {
		return nil, err
	}
	manifest := &packageManifest{}
	if err := json.Unmarshal(content, manifest); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if manifest.Scripts == nil {
		manifest.Scripts = map[string]string{}
	}
	return manifest, nil
}

// nearestPackageJSON walks up from dir looking for package.json, never leaving
// the workspace (§15.5.1 step 1).
func nearestPackageJSON(dir, workspace string) (string, bool) {
	current := canonicalize(dir)
	root := canonicalize(workspace)
	if current == "" || root == "" {
		return "", false
	}
	for {
		candidate := filepath.Join(current, "package.json")
		if fileExists(candidate) {
			return candidate, true
		}
		if current == root {
			return "", false
		}
		parent := filepath.Dir(current)
		if parent == current || len(parent) < len(root) {
			return "", false
		}
		current = parent
	}
}

// npmManagerName normalizes the executable to the manager it is.
func npmManagerName(executable string) string {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(executable, `\`, "/")))
	for _, extension := range []string{".exe", ".cmd", ".bat", ".ps1"} {
		base = strings.TrimSuffix(base, extension)
	}
	return base
}

// npmPassthrough joins the arguments after `--`, which npm appends to the
// script verbatim (§15.5.1 step 3).
func npmPassthrough(rest []parser.Word) string {
	for i, word := range rest {
		if word.Text == "--" {
			return strings.Join(npmWordTexts(rest[i+1:]), " ")
		}
	}
	return ""
}

func npmWordTexts(words []parser.Word) []string {
	out := make([]string, 0, len(words))
	for _, word := range words {
		out = append(out, word.Text)
	}
	return out
}

// npmFlagPresent reports the first flag from the given set that appears
// before the `--` separator, so callers can refuse workspace-, directory- and
// config-redirecting flags without guessing at their value.
func npmFlagPresent(rest []parser.Word, flags []string) (string, bool) {
	for _, word := range rest {
		if word.Text == "--" {
			return "", false
		}
		name, _, _ := strings.Cut(word.Text, "=")
		if contains(flags, name) {
			return name, true
		}
	}
	return "", false
}

// npmHasFlag reports whether a flag appears before the `--` separator.
func npmHasFlag(rest []parser.Word, flag string) bool {
	for _, word := range rest {
		if word.Text == "--" {
			return false
		}
		if word.Text == flag {
			return true
		}
	}
	return false
}

// npmLocalBinaryExists reports whether a node_modules/.bin entry is present.
// Windows installs write .cmd and .ps1 shims next to the extensionless file.
func npmLocalBinaryExists(binary string) bool {
	for _, candidate := range []string{binary, binary + ".cmd", binary + ".ps1", binary + ".exe"} {
		if _, err := os.Lstat(candidate); err == nil {
			return true
		}
	}
	return false
}

func npmPackageManagerInfo(req Request) action.PackageManagerInfo {
	if req.Context == nil || req.Context.Action == nil {
		return action.PackageManagerInfo{}
	}
	return req.Context.Action.PackageManager
}

func npmHostOS(req Request) string {
	if req.Context == nil || req.Context.Action == nil {
		return ""
	}
	return req.Context.Action.Platform
}
