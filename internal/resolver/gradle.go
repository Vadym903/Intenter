package resolver

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/parser"
)

// GradleRecognizer models `gradle` and the wrapper scripts (§15.5.2).
//
// A build tool is DECLARED rather than RESOLVED: Intenter does not read the
// build scripts to work out what a task does, it declares the envelope such a
// task is allowed to act in and fingerprints every file that could change that
// behavior. A task outside the known set is not guessed at.
func GradleRecognizer() Recognizer { return gradleRecognizer{} }

type gradleRecognizer struct{}

func (gradleRecognizer) Names() []string {
	return []string{"gradle", "gradlew", "gradlew.bat"}
}

// gradleTaskOps maps a task name to what it means. Suffix rules below cover the
// families (`*Test`, `compile*`, `publish*`).
var gradleTaskOps = map[string]action.SemanticOp{
	"test":            action.OpRunTests,
	"check":           action.OpRunTests,
	"integrationTest": action.OpRunTests,
	"build":           action.OpBuild,
	"assemble":        action.OpBuild,
	"classes":         action.OpBuild,
	"jar":             action.OpBuild,
	"bootJar":         action.OpBuild,
	"war":             action.OpBuild,
	"clean":           action.OpClean,
	"wrapper":         action.OpBuildToolInfo,
	"init":            action.OpBuildToolInfo,
	"dependencies":    action.OpBuildToolInfo,
	"tasks":           action.OpBuildToolInfo,
	"help":            action.OpBuildToolInfo,
	"projects":        action.OpBuildToolInfo,
}

var gradleGrammar = Grammar{
	Safe: []string{
		"--info", "--debug", "--stacktrace", "--full-stacktrace", "--no-daemon",
		"--daemon", "--offline", "--continue", "--rerun-tasks", "-q", "--quiet",
		"--parallel", "--no-parallel", "--build-cache", "--no-build-cache",
		"--configuration-cache", "--no-configuration-cache",
		"--refresh-dependencies", "-S", "-i", "-d", "--scan", "--no-scan",
		"--dry-run", "-m", "--no-rebuild", "-a", "--console",
	},
	SafeValue:         []string{"--tests", "-x", "--exclude-task", "--max-workers"},
	SafeOptionalValue: []string{"--warning-mode", "--console", "--priority"},
	// Every path or execution flag changes which build runs, so none is safe.
	SafePrefixes: []string{"-P", "-D"},
}

// gradleUnsafeSystemProperties are -D values that change where the build reads
// and writes, so they take the invocation outside the declared envelope.
//
// The proxy and TLS-trust properties (AG-145) are the same class of gap as
// AG-05's curl `--resolve`: the declared envelope's network effect is a
// generic "dependency-registry" target, and Java's standard networking
// properties silently redirect every HTTP(S) request the JVM makes —
// including dependency downloads — through a host of the agent's choosing, or
// disable certificate validation against it, without changing the fingerprint
// or the effect set an approval was granted against.
var gradleUnsafeSystemProperties = []string{
	"-Dorg.gradle.jvmargs", "-Duser.home", "-Dgradle.user.home",
	"-Dorg.gradle.java.home", "-Dorg.gradle.project.buildDir",
	"-Dhttp.proxyHost", "-Dhttp.proxyPort", "-Dhttp.proxyUser", "-Dhttp.proxyPassword",
	"-Dhttps.proxyHost", "-Dhttps.proxyPort", "-Dhttps.proxyUser", "-Dhttps.proxyPassword",
	"-Dsocks.proxyHost", "-Dsocks.proxyPort",
	"-Djavax.net.ssl.trustStore", "-Djavax.net.ssl.trustStorePassword", "-Djavax.net.ssl.trustStoreType",
}

func (gradleRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := gradleGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpBuildToolInfo)
	out.Status = action.StatusDeclared

	for _, word := range req.Command.Args() {
		if property, unsafe := gradleUnsafeProperty(word.Text); unsafe {
			return Unresolved(req, action.OpUnknown, fmt.Sprintf(
				"%s changes where Gradle reads and writes", property))
		}
	}
	if !args.OK() {
		return Unresolved(req, action.OpUnknown, args.UnknownReason(req.Command.Name()))
	}
	if len(args.Operands) == 0 {
		return Unresolved(req, action.OpUnknown, "gradle was called without a task")
	}

	workspace := req.Workspace()
	if workspace == "" {
		return Unresolved(req, action.OpUnknown, "no workspace root was established for this request")
	}

	hasIncludedBuild, err := gradleHasIncludedBuild(workspace)
	if err != nil {
		return Unresolved(req, action.OpUnknown, "the Gradle settings file could not be read: "+err.Error())
	}
	if hasIncludedBuild {
		return Unresolved(req, action.OpUnknown,
			"the settings file declares an included build (includeBuild), whose directory Intenter does not read or fingerprint")
	}

	// The strongest meaning among the requested tasks decides the operation,
	// and any task outside the known set makes the whole invocation unknown.
	op := action.OpBuildToolInfo
	cleans := false
	for _, operand := range args.Operands {
		taskOp, known := gradleTaskOp(operand.Text)
		if !known {
			return Unresolved(req, action.OpUnknown, fmt.Sprintf(
				"the Gradle task %q is not one Intenter models", operand.Text))
		}
		if taskOp == action.OpClean {
			cleans = true
		}
		op = strongerBuildOp(op, taskOp)
	}
	out.SemanticOp = op

	declareBuildEnvelope(&out, req, workspace, buildEnvelope{
		generatedDirs: gradleGeneratedDirs(req, workspace, args.Operands),
		toolCache:     ".gradle",
		clean:         cleans,
	})

	fingerprint, err := req.Context.Fingerprint(KeyGradleConfig+"@"+workspace, func() (action.Fingerprint, error) {
		return GradleConfigFingerprint(workspace, requestHome(req))
	})
	if err != nil {
		return Unresolved(req, op, "the Gradle build files could not be fingerprinted: "+err.Error())
	}
	out.Fingerprints = action.MergeFingerprints(out.Fingerprints, fingerprint)
	return out
}

// gradleTaskOp resolves a task name, honoring module qualifiers and the task
// families §15.5.2 lists.
func gradleTaskOp(task string) (action.SemanticOp, bool) {
	// `:module:test` targets the same task in a subproject.
	name := task
	if index := strings.LastIndex(task, ":"); index >= 0 {
		name = task[index+1:]
	}
	if name == "" {
		return "", false
	}

	if op, known := gradleTaskOps[name]; known {
		return op, true
	}
	switch {
	case strings.HasSuffix(name, "Test"):
		return action.OpRunTests, true
	case strings.HasPrefix(name, "compile"):
		return action.OpBuild, true
	case strings.HasPrefix(name, "publish"), strings.HasPrefix(name, "upload"), strings.HasPrefix(name, "deploy"):
		// Publishing sends artifacts somewhere; never auto-approvable here.
		return "", false
	}
	return "", false
}

// gradleHasIncludedBuild reports whether a settings script declares a
// composite build (`includeBuild(...)`, including inside a `pluginManagement`
// block, which uses the same function).
//
// An included build is a second, independent Gradle project Gradle evaluates
// and builds — running its own build script (arbitrary Groovy/Kotlin) and
// writing its own output directory — that can live entirely outside the
// current workspace. declareBuildEnvelope only models reads/writes under the
// workspace and the tool cache, and GradleConfigFingerprint only walks the
// workspace, buildSrc and the user-level Gradle config, so an included
// build's directory is neither an effect target nor a fingerprint input:
// editing it would silently keep matching a stale approval, and Intenter
// cannot see what it actually reads or writes (AG-147, the same class as
// AG-142's yarnPath).
func gradleHasIncludedBuild(workspace string) (bool, error) {
	for _, name := range []string{"settings.gradle", "settings.gradle.kts"} {
		path := filepath.Join(workspace, name)
		if !fileExists(path) {
			continue
		}
		content, err := readConfigFile(path)
		if err != nil {
			return false, err
		}
		if bytes.Contains(content, []byte("includeBuild")) {
			return true, nil
		}
	}
	return false, nil
}

// gradleUnsafeProperty reports a -D property that moves the build's inputs or
// outputs.
func gradleUnsafeProperty(word string) (string, bool) {
	for _, property := range gradleUnsafeSystemProperties {
		if word == property || strings.HasPrefix(word, property+"=") {
			return property, true
		}
	}
	return "", false
}

// gradleGeneratedDirs lists the directories a Gradle build writes to.
func gradleGeneratedDirs(req Request, workspace string, tasks []parser.Word) []string {
	dirs := []string{filepath.Join(workspace, "build"), filepath.Join(workspace, ".gradle")}

	// A module-qualified task writes into that module's build directory.
	for _, task := range tasks {
		module, ok := gradleModulePath(workspace, task.Text)
		if ok {
			dirs = append(dirs, filepath.Join(module, "build"))
		}
	}
	_ = req
	return dirs
}

// gradleModulePath turns `:app:test` into `<W>/app`.
func gradleModulePath(workspace, task string) (string, bool) {
	index := strings.LastIndex(task, ":")
	if index <= 0 {
		return "", false
	}
	segments := strings.Split(strings.Trim(task[:index], ":"), ":")
	if len(segments) == 0 || segments[0] == "" {
		return "", false
	}
	return filepath.Join(append([]string{workspace}, segments...)...), true
}

// strongerBuildOp picks the operation that describes the most of what happens.
func strongerBuildOp(a, b action.SemanticOp) action.SemanticOp {
	rank := map[action.SemanticOp]int{
		action.OpBuildToolInfo: 0,
		action.OpClean:         1,
		action.OpRunTests:      2,
		action.OpBuild:         3,
	}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// buildEnvelope describes what a declared build tool may do (§17.3).
type buildEnvelope struct {
	// generatedDirs are the build output directories it writes.
	generatedDirs []string
	// toolCache is the home-directory cache it reads and writes, e.g. ".gradle".
	toolCache string
	// clean marks an invocation that deletes its output directories.
	clean bool
}

// declareBuildEnvelope records the effects a build tool is declared to have:
// it reads the project, writes its own output directories and tool cache, talks
// to a dependency registry, and executes project code (§15.5.2, §15.5.3).
func declareBuildEnvelope(out *action.ResolvedCommand, req Request, workspace string, envelope buildEnvelope) {
	if target, ok := req.PathTarget(workspace); ok {
		addEffect(out, target, action.EffectRead, action.EffectFlagRecursive)
	}

	for _, dir := range envelope.generatedDirs {
		target, ok := req.PathTarget(dir)
		if !ok {
			continue
		}
		addEffect(out, target, action.EffectCreate, action.EffectFlagRecursive)
		addEffect(out, target, action.EffectWrite, action.EffectFlagRecursive)
		if envelope.clean {
			addEffect(out, target, action.EffectDelete, action.EffectFlagRecursive)
		}
	}

	if envelope.toolCache != "" {
		if home := requestHome(req); home != "" {
			if target, ok := req.PathTarget(filepath.Join(home, envelope.toolCache)); ok {
				addEffect(out, target, action.EffectRead, action.EffectFlagRecursive)
				addEffect(out, target, action.EffectWrite, action.EffectFlagRecursive)
			}
		}
	}

	out.Effects = append(out.Effects,
		action.Effect{
			Type:    action.EffectNetwork,
			Network: &action.NetworkTarget{DeclaredKind: "dependency-registry"},
		},
		action.Effect{
			Type:    action.EffectExecute,
			Program: &action.ProgramRef{Name: displayName(out.Executable), Resolution: action.ProgramDeclared},
		})
}

// requestHome is the home directory of the request context.
func requestHome(req Request) string {
	if req.Context == nil || req.Context.Action == nil {
		return ""
	}
	return req.Context.Action.HomeDir
}
