package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
)

// These are end-to-end regressions for the package/build-tools area of the
// security audit (specs/005-make-product-usable, T047 — SECURITY_AUDIT.md
// §8.2): each was a way to get a package-manager or build-tool invocation past
// the gate with a resolved effect set that did not match what would really
// run. They run the whole pipeline through the real daemon, the same path a
// hook takes. See internal/daemon/security_regression_test.go for the AG-01…
// AG-11 regressions this file continues numbering from (AG-14x).

// AG-14x, critical: `.yarnrc.yml`'s yarnPath repoints every `yarn` invocation
// at a project-supplied JavaScript file — the standard mechanism Yarn Berry
// uses to pin its own release. Before the fix, a script that reads as
// harmless from package.json's text stayed RESOLVED and was auto-allowed by
// the read-only baseline (§18.3 B1) with zero prompts, exactly the shape of
// AG-01's unmodeled pager: a config-file execution redirect the resolver
// never saw.
func TestYarnPathCommandIsNotAutoAllowed(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	client := startDaemon(t, p)

	w.write(t, "package.json", `{"scripts":{"test":"cat package.json"}}`)
	w.write(t, ".yarnrc.yml", "yarnPath: .yarn/releases/yarn-3.6.1.cjs\n")

	result := evaluate(t, client, bashRequest(w, "yarn test", ""))
	if result.Decision == action.OutcomeAllow {
		t.Errorf("a yarn command in a workspace with a yarnPath override must not be auto-allowed (%s)", result.Class)
	}
}

// AG-14x: `--prefix`/`-C`/`--dir`/`--cwd` point npm/pnpm/yarn at a
// package.json Intenter never reads — `nearestPackageJSON` only follows the
// shell's own cwd — so a resolution built without seeing the flag would not
// match what really runs. Before the fix this was silently ignored by `npm
// run`, which could auto-allow a script that reads as harmless from the wrong
// manifest.
func TestNpmRunWithPrefixIsNotAutoAllowed(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	client := startDaemon(t, p)

	w.write(t, "package.json", `{"scripts":{"build":"cat package.json"}}`)

	result := evaluate(t, client, bashRequest(w, "npm run build --prefix ../other", ""))
	if result.Decision == action.OutcomeAllow {
		t.Errorf("--prefix must not be auto-allowed (%s)", result.Class)
	}
	if result.Class != action.ClassUnresolvedCommand {
		t.Errorf("class = %s (%s), want UNRESOLVED_COMMAND", result.Class, result.Reason)
	}
}

// AG-14x: npx used to strip every option-shaped word anywhere in a command,
// including ones written after the package name that were arguments to the
// invoked program, not to npx itself. The resolved text handed to the
// program's own recognizer then no longer matched what npx would really run.
// An argument the program's own grammar refuses must now reach it and be
// refused, not silently disappear.
func TestNpxDropsNoArgumentsAfterThePackageName(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	client := startDaemon(t, p)

	w.write(t, "package.json", `{"scripts":{}}`)
	binDir := filepath.Join(w.root, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "rimraf"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	result := evaluate(t, client, bashRequest(w, "npx rimraf --an-unknown-flag ./dist", ""))
	if result.Decision == action.OutcomeAllow {
		t.Errorf("an argument dropped from the resolved command must not be auto-allowed (%s)", result.Class)
	}
	if result.Class != action.ClassUnresolvedCommand {
		t.Errorf("class = %s (%s), want UNRESOLVED_COMMAND", result.Class, result.Reason)
	}
}

// AG-144, critical: a .pnpmfile.cjs's readPackage/afterAllResolved hooks run
// during pnpm's dependency resolution and are NOT disabled by
// --ignore-scripts, unlike the installed packages' own lifecycle scripts.
// Before the fix, `pnpm install --ignore-scripts` resolved to only the
// declared node_modules/lockfile writes — the same envelope as a pnpmfile-free
// install — so an approval for the harmless case would also cover a workspace
// that ships a pnpmfile running arbitrary Node.js on every install.
func TestPnpmFileHooksAreNotAutoAllowed(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	client := startDaemon(t, p)

	w.write(t, "package.json", `{"scripts":{}}`)
	w.write(t, ".pnpmfile.cjs", "module.exports = { hooks: { readPackage(pkg) { return pkg } } }\n")

	result := evaluate(t, client, bashRequest(w, "pnpm install --ignore-scripts", ""))
	if result.Decision == action.OutcomeAllow {
		t.Errorf("a pnpm install in a workspace with a pnpmfile must not be auto-allowed (%s)", result.Class)
	}
	if result.Class != action.ClassUnresolvedCommand {
		t.Errorf("class = %s (%s), want UNRESOLVED_COMMAND", result.Class, result.Reason)
	}
}

// AG-145, high: the standard Java `http(s).proxyHost`/`javax.net.ssl.trustStore`
// system properties are not part of a Gradle build's declared envelope or
// fingerprint, but they silently redirect (or defeat certificate validation
// for) every network request the JVM makes — including the dependency
// downloads the declared envelope's "dependency-registry" network effect
// covers. An approval for a plain `./gradlew test` must not also cover the
// same command with dependency traffic routed through an attacker's proxy.
func TestGradleProxyPropertyIsNotAutoAllowed(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	client := startDaemon(t, p)

	w.write(t, "settings.gradle.kts", `rootProject.name = "demo"`)
	w.write(t, "build.gradle.kts", "plugins { java }\n")
	w.write(t, "gradlew", "#!/bin/sh\nexec gradle \"$@\"\n")
	w.write(t, "gradle/wrapper/gradle-wrapper.properties", "distributionUrl=https\\://services.gradle.org/x.zip\n")

	redirected := evaluate(t, client, bashRequest(w, "./gradlew test -Dhttps.proxyHost=attacker.example", ""))
	if redirected.Decision == action.OutcomeAllow {
		t.Errorf("a proxy-redirecting -D property must not be auto-allowed (%s)", redirected.Class)
	}
	if redirected.Class != action.ClassUnresolvedCommand {
		t.Errorf("class = %s (%s), want UNRESOLVED_COMMAND", redirected.Class, redirected.Reason)
	}
}

// AG-146, high: Maven's `maven.wagon.http.ssl.*` properties are the documented
// way to disable certificate validation for its HTTP transport; they were not
// in the unsafe-property list, so an approved build could be replayed with
// dependency downloads accepting any TLS certificate — the standard MITM
// injection path for a "dependency-registry" network effect.
func TestMavenInsecureTLSPropertyIsNotAutoAllowed(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	client := startDaemon(t, p)

	w.write(t, "pom.xml", `<project><artifactId>demo</artifactId></project>`)
	w.write(t, "mvnw", "#!/bin/sh\nexec mvn \"$@\"\n")
	w.write(t, ".mvn/wrapper/maven-wrapper.properties", "distributionUrl=https://example/maven.zip\n")

	result := evaluate(t, client, bashRequest(w, "mvn test -Dmaven.wagon.http.ssl.insecure=true", ""))
	if result.Decision == action.OutcomeAllow {
		t.Errorf("an insecure-TLS wagon property must not be auto-allowed (%s)", result.Class)
	}
	if result.Class != action.ClassUnresolvedCommand {
		t.Errorf("class = %s (%s), want UNRESOLVED_COMMAND", result.Class, result.Reason)
	}
}

// AG-147: an included build (`includeBuild` in settings.gradle) is a second
// Gradle project, evaluated and built with its own arbitrary build script,
// whose directory is outside both the declared read/write envelope and
// GradleConfigFingerprint's file set — so editing it would silently keep
// matching a stale approval.
func TestGradleIncludedBuildIsNotAutoAllowed(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	client := startDaemon(t, p)

	w.write(t, "settings.gradle.kts", "rootProject.name = \"demo\"\nincludeBuild(\"../shared-lib\")\n")
	w.write(t, "build.gradle.kts", "plugins { java }\n")
	w.write(t, "gradlew", "#!/bin/sh\nexec gradle \"$@\"\n")
	w.write(t, "gradle/wrapper/gradle-wrapper.properties", "distributionUrl=https\\://services.gradle.org/x.zip\n")

	result := evaluate(t, client, bashRequest(w, "./gradlew test", ""))
	if result.Decision == action.OutcomeAllow {
		t.Errorf("a build declaring an included build must not be auto-allowed (%s)", result.Class)
	}
	if result.Class != action.ClassUnresolvedCommand {
		t.Errorf("class = %s (%s), want UNRESOLVED_COMMAND", result.Class, result.Reason)
	}
}
