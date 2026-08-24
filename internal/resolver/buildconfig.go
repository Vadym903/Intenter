package resolver

import (
	"path/filepath"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
)

// GradleConfigFingerprint covers every file that decides what a Gradle task
// does: the wrapper, the settings and build scripts, gradle.properties, and
// everything under buildSrc (§15.5.2) — plus the user-level files Gradle reads
// before any of those: `~/.gradle/gradle.properties` (a `-D`-equivalent way to
// set `org.gradle.jvmargs`, blocked on the command line but not here until
// now) and every `~/.gradle/init.d/*.gradle(.kts)` init script, which Gradle
// runs unconditionally at the start of every invocation on the machine. The
// declared envelope already grants Gradle recursive read/write of `~/.gradle`
// as its tool cache, so these files are already effects an approval covers —
// the gap this closes is that editing them did not withdraw the approval.
//
// A build tool is approved as a declared envelope rather than a modeled
// command, so this fingerprint is what makes that safe: editing any of these
// files changes the fingerprint and withdraws the approval.
func GradleConfigFingerprint(workspace, home string) (action.Fingerprint, error) {
	fixed := []string{
		filepath.Join(workspace, "gradlew"),
		filepath.Join(workspace, "gradlew.bat"),
		filepath.Join(workspace, "gradle", "wrapper", "gradle-wrapper.properties"),
		filepath.Join(workspace, "gradle", "wrapper", "gradle-wrapper.jar"),
		filepath.Join(workspace, "settings.gradle"),
		filepath.Join(workspace, "settings.gradle.kts"),
		filepath.Join(workspace, "gradle.properties"),
	}
	if home != "" {
		fixed = append(fixed,
			filepath.Join(home, ".gradle", "gradle.properties"),
			filepath.Join(home, ".gradle", "init.gradle"),
			filepath.Join(home, ".gradle", "init.gradle.kts"))
	}

	files, err := CollectFiles(FileSetOptions{
		Files: fixed,
		Roots: []string{workspace, filepath.Join(workspace, "buildSrc")},
		MatchName: func(name string) bool {
			return strings.HasSuffix(name, ".gradle") || strings.HasSuffix(name, ".gradle.kts")
		},
		SkipDir: func(dir string) bool {
			return skipBuildConfigDir(workspace, dir)
		},
	})
	if err != nil {
		return action.Fingerprint{}, err
	}

	// buildSrc is build logic in full, so every file there counts, not only the
	// ones matching the *.gradle pattern above.
	buildSrc, err := CollectFiles(FileSetOptions{
		Roots:   []string{filepath.Join(workspace, "buildSrc")},
		SkipDir: func(dir string) bool { return skipBuildConfigDir(workspace, dir) },
	})
	if err != nil {
		return action.Fingerprint{}, err
	}
	files = append(files, buildSrc...)

	if home != "" {
		initDir, err := CollectFiles(FileSetOptions{
			Roots: []string{filepath.Join(home, ".gradle", "init.d")},
			MatchName: func(name string) bool {
				return strings.HasSuffix(name, ".gradle") || strings.HasSuffix(name, ".gradle.kts")
			},
		})
		if err != nil {
			return action.Fingerprint{}, err
		}
		files = append(files, initDir...)
	}

	return FileSetFingerprint(KeyGradleConfig, workspace, files,
		"Gradle wrapper, settings, build scripts, buildSrc and the user-level init scripts and properties")
}

// MavenConfigFingerprint covers the Maven wrapper, the .mvn directory and every
// pom.xml under the workspace (§15.5.3) — plus `~/.m2/settings.xml` and
// `~/.m2/toolchains.xml`, which Maven reads before any of the workspace files
// and can redirect artifact resolution (mirrors, plugin repositories) or the
// JDK a build runs under. The declared envelope already reads `~/.m2`
// recursively for every invocation, so this only closes the fingerprint gap.
func MavenConfigFingerprint(workspace, home string) (action.Fingerprint, error) {
	fixed := []string{
		filepath.Join(workspace, "mvnw"),
		filepath.Join(workspace, "mvnw.cmd"),
	}
	if home != "" {
		fixed = append(fixed,
			filepath.Join(home, ".m2", "settings.xml"),
			filepath.Join(home, ".m2", "toolchains.xml"))
	}

	poms, err := CollectFiles(FileSetOptions{
		Files:     fixed,
		Roots:     []string{workspace},
		MatchName: func(name string) bool { return name == "pom.xml" },
		SkipDir: func(dir string) bool {
			return skipBuildConfigDir(workspace, dir)
		},
	})
	if err != nil {
		return action.Fingerprint{}, err
	}

	mvnDir, err := CollectFiles(FileSetOptions{
		Roots:   []string{filepath.Join(workspace, ".mvn")},
		SkipDir: func(dir string) bool { return skipBuildConfigDir(workspace, dir) },
	})
	if err != nil {
		return action.Fingerprint{}, err
	}

	return FileSetFingerprint(KeyMavenConfig, workspace, append(poms, mvnDir...),
		"Maven wrapper, .mvn, every pom.xml and the user-level settings and toolchains")
}

// skipBuildConfigDir skips generated output and vendored dependencies, which
// never define what a build does. The workspace root itself is always walked.
func skipBuildConfigDir(workspace, dir string) bool {
	if filepath.Clean(dir) == filepath.Clean(workspace) {
		return false
	}
	return SkipGeneratedAndVendor(filepath.Base(dir))
}
