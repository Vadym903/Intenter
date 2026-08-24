package resolver

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
)

// MavenRecognizer models `mvn` and the wrapper scripts (§15.5.3).
func MavenRecognizer() Recognizer { return mavenRecognizer{} }

type mavenRecognizer struct{}

func (mavenRecognizer) Names() []string { return []string{"mvn", "mvnw", "mvnw.cmd"} }

// mavenGoalOps maps a phase or goal onto what it means.
var mavenGoalOps = map[string]action.SemanticOp{
	"test":               action.OpRunTests,
	"verify":             action.OpRunTests,
	"integration-test":   action.OpRunTests,
	"surefire:test":      action.OpRunTests,
	"compile":            action.OpBuild,
	"test-compile":       action.OpBuild,
	"package":            action.OpBuild,
	"install":            action.OpBuild,
	"validate":           action.OpBuildToolInfo,
	"clean":              action.OpClean,
	"dependency:tree":    action.OpBuildToolInfo,
	"dependency:list":    action.OpBuildToolInfo,
	"dependency:analyze": action.OpBuildToolInfo,
}

var mavenGrammar = Grammar{
	Safe: []string{
		"-q", "--quiet", "-B", "--batch-mode", "-e", "--errors", "-X", "--debug",
		"-o", "--offline", "-U", "--update-snapshots", "-am", "--also-make",
		"-amd", "--also-make-dependents", "-ntp", "--no-transfer-progress",
		"-V", "--show-version", "-C", "--strict-checksums", "-c", "--lax-checksums",
		"-fae", "--fail-at-end", "-ff", "--fail-fast", "-fn", "--fail-never",
		"-N", "--non-recursive", "-o",
	},
	SafeValue:    []string{"-pl", "--projects", "-T", "--threads", "-rf", "--resume-from"},
	SafePrefixes: []string{"-D", "-P"},
}

// mavenUnsafeSystemProperties move where Maven reads and writes.
//
// The proxy properties and the `maven.wagon.http.ssl.*` flags (AG-146) are the
// same class of gap as AG-05's curl `--resolve`/`--cookie-jar`: the declared
// envelope's network effect is a generic "dependency-registry" target, but
// these properties silently redirect every dependency download through a
// host of the agent's choosing, or disable certificate validation for it
// entirely — the well-known way to make Maven's HTTP wagon accept any
// certificate — without changing the fingerprint or effect set an approval
// was granted against.
var mavenUnsafeSystemProperties = []string{
	"-Dmaven.repo.local", "-Duser.home", "-Dmaven.home",
	"-Dhttp.proxyHost", "-Dhttp.proxyPort", "-Dhttp.proxyUser", "-Dhttp.proxyPassword",
	"-Dhttps.proxyHost", "-Dhttps.proxyPort", "-Dhttps.proxyUser", "-Dhttps.proxyPassword",
	"-Dmaven.wagon.http.ssl.insecure", "-Dmaven.wagon.http.ssl.allowall",
	"-Dmaven.wagon.http.ssl.ignore.validity.dates",
}

func (mavenRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := mavenGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpBuildToolInfo)
	out.Status = action.StatusDeclared

	for _, word := range req.Command.Args() {
		for _, property := range mavenUnsafeSystemProperties {
			if word.Text == property || strings.HasPrefix(word.Text, property+"=") {
				return Unresolved(req, action.OpUnknown, fmt.Sprintf(
					"%s changes where Maven reads and writes", property))
			}
		}
	}
	if !args.OK() {
		return Unresolved(req, action.OpUnknown, args.UnknownReason(req.Command.Name()))
	}
	if len(args.Operands) == 0 {
		return Unresolved(req, action.OpUnknown, "mvn was called without a goal")
	}

	workspace := req.Workspace()
	if workspace == "" {
		return Unresolved(req, action.OpUnknown, "no workspace root was established for this request")
	}

	op := action.OpBuildToolInfo
	cleans := false
	installs := false
	for _, operand := range args.Operands {
		goalOp, known := mavenGoalOp(operand.Text)
		if !known {
			return Unresolved(req, action.OpUnknown, fmt.Sprintf(
				"the Maven goal %q is not one Intenter models", operand.Text))
		}
		if goalOp == action.OpClean {
			cleans = true
		}
		if operand.Text == "install" {
			installs = true
		}
		op = strongerBuildOp(op, goalOp)
	}
	out.SemanticOp = op

	// `install` additionally writes the local artifact repository.
	toolCache := ""
	if installs {
		toolCache = ".m2"
	}
	declareBuildEnvelope(&out, req, workspace, buildEnvelope{
		generatedDirs: mavenGeneratedDirs(workspace),
		toolCache:     toolCache,
		clean:         cleans,
	})
	if !installs {
		// Even without install, Maven reads the local repository.
		if home := requestHome(req); home != "" {
			if target, ok := req.PathTarget(filepath.Join(home, ".m2")); ok {
				addEffect(&out, target, action.EffectRead, action.EffectFlagRecursive)
			}
		}
	}

	fingerprint, err := req.Context.Fingerprint(KeyMavenConfig+"@"+workspace, func() (action.Fingerprint, error) {
		return MavenConfigFingerprint(workspace, requestHome(req))
	})
	if err != nil {
		return Unresolved(req, op, "the Maven build files could not be fingerprinted: "+err.Error())
	}
	out.Fingerprints = action.MergeFingerprints(out.Fingerprints, fingerprint)
	return out
}

// mavenGoalOp resolves a phase or goal, refusing the ones that publish.
func mavenGoalOp(goal string) (action.SemanticOp, bool) {
	if op, known := mavenGoalOps[goal]; known {
		return op, true
	}
	switch {
	case strings.HasPrefix(goal, "failsafe:"):
		return action.OpRunTests, true
	case strings.HasPrefix(goal, "help:"), strings.HasPrefix(goal, "versions:display"):
		return action.OpBuildToolInfo, true
	case goal == "deploy", goal == "site-deploy", strings.HasPrefix(goal, "release:"):
		// Publishing sends artifacts somewhere; never auto-approvable here.
		return "", false
	}
	return "", false
}

// mavenGeneratedDirs lists the build output directories, including one per
// module of a multi-module build.
func mavenGeneratedDirs(workspace string) []string {
	dirs := []string{filepath.Join(workspace, "target")}

	modules, err := CollectFiles(FileSetOptions{
		Roots:     []string{workspace},
		MatchName: func(name string) bool { return name == "pom.xml" },
		SkipDir:   func(dir string) bool { return skipBuildConfigDir(workspace, dir) },
	})
	if err != nil {
		return dirs
	}
	for _, pom := range modules {
		dirs = append(dirs, filepath.Join(filepath.Dir(pom), "target"))
	}
	return dirs
}
