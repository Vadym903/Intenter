package resolver

import (
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
)

// mavenRepo is a workspace with a Maven wrapper and a pom.
func mavenRepo(t *testing.T) *repo {
	t.Helper()
	r := newRepo(t)
	r.write(t, "pom.xml", `<project><artifactId>demo</artifactId></project>`)
	r.write(t, "mvnw", "#!/bin/sh\nexec mvn \"$@\"\n")
	r.write(t, ".mvn/wrapper/maven-wrapper.properties", "distributionUrl=https://example/maven.zip\n")
	return r
}

func TestMavenGoalsMapToOperations(t *testing.T) {
	r := mavenRepo(t)

	tests := []struct {
		command string
		op      action.SemanticOp
	}{
		{"mvn test", action.OpRunTests},
		{"mvn verify", action.OpRunTests},
		{"mvn integration-test", action.OpRunTests},
		{"mvn surefire:test", action.OpRunTests},
		{"mvn failsafe:integration-test", action.OpRunTests},
		{"./mvnw test", action.OpRunTests},
		{"mvn compile", action.OpBuild},
		{"mvn package", action.OpBuild},
		{"mvn install", action.OpBuild},
		{"mvn clean", action.OpClean},
		{"mvn clean install", action.OpBuild},
		{"mvn dependency:tree", action.OpBuildToolInfo},
		{"mvn help:effective-pom", action.OpBuildToolInfo},
		{"mvn test -q -B -DskipTests", action.OpRunTests},
		{"mvn test -pl core -am", action.OpRunTests},
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

func TestMavenDeclaredEnvelope(t *testing.T) {
	r := mavenRepo(t)
	out := r.resolveCommand(t, "mvn test")

	summary := strings.Join(effectSummary(out), "\n")
	for _, want := range []string{
		"READ(recursive) .",
		"CREATE(recursive) ./target",
		"WRITE(recursive) ./target",
		"READ(recursive) ~/.m2",
		"NETWORK declared:dependency-registry",
		"EXECUTE program:DECLARED",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("declared envelope must include %q:\n%s", want, summary)
		}
	}

	// A plain test run reads the local repository but does not publish into it.
	if strings.Contains(summary, "WRITE(recursive) ~/.m2") {
		t.Errorf("only `install` writes the local repository:\n%s", summary)
	}
}

func TestMavenInstallWritesTheLocalRepository(t *testing.T) {
	r := mavenRepo(t)
	out := r.resolveCommand(t, "mvn install")

	summary := strings.Join(effectSummary(out), "\n")
	if !strings.Contains(summary, "WRITE(recursive) ~/.m2") {
		t.Errorf("install writes the local repository (§15.5.3):\n%s", summary)
	}
}

func TestMavenCleanDeletesTarget(t *testing.T) {
	r := mavenRepo(t)
	out := r.resolveCommand(t, "mvn clean")

	if !strings.Contains(strings.Join(effectSummary(out), "\n"), "DELETE(recursive) ./target") {
		t.Errorf("clean must delete target:\n%s", strings.Join(effectSummary(out), "\n"))
	}
}

func TestMavenMultiModuleTargets(t *testing.T) {
	r := mavenRepo(t)
	r.write(t, "core/pom.xml", `<project><artifactId>core</artifactId></project>`)
	r.write(t, "api/pom.xml", `<project><artifactId>api</artifactId></project>`)

	out := r.resolveCommand(t, "mvn package")
	summary := strings.Join(effectSummary(out), "\n")
	for _, want := range []string{"./core/target", "./api/target"} {
		if !strings.Contains(summary, want) {
			t.Errorf("every module's output must be covered, missing %q:\n%s", want, summary)
		}
	}
}

func TestMavenRefusesWhatItCannotModel(t *testing.T) {
	r := mavenRepo(t)

	tests := []struct {
		name    string
		command string
		reason  string
	}{
		{"deploy", "mvn deploy", "deploy"},
		{"release", "mvn release:prepare", "release:prepare"},
		{"site deploy", "mvn site-deploy", "site-deploy"},
		{"unknown goal", "mvn frobnicate", "frobnicate"},
		{"no goal", "mvn", "without a goal"},
		{"alternate pom", "mvn -f other/pom.xml test", "-f"},
		{"settings file", "mvn -s custom-settings.xml test", "-s"},
		{"toolchains", "mvn -t toolchains.xml test", "-t"},
		{"repository override", "mvn test -Dmaven.repo.local=/tmp/repo", "-Dmaven.repo.local"},
		{"user home override", "mvn test -Duser.home=/tmp", "-Duser.home"},
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

// AG-143: the declared envelope already reads ~/.m2 recursively for every
// invocation, but the fingerprint only covered the workspace side of Maven's
// configuration — a change to ~/.m2/settings.xml (mirrors, plugin
// repositories, default-active profiles) or ~/.m2/toolchains.xml (which JDK a
// build runs under) did not withdraw an existing approval.
func TestMavenConfigFingerprintCoversUserLevelConfig(t *testing.T) {
	r := mavenRepo(t)
	before := r.resolveAction(t, "mvn test")
	previous, ok := fingerprintValue(before, KeyMavenConfig)
	if !ok {
		t.Fatalf("fingerprints = %+v, want maven-config", before.Fingerprints)
	}

	changes := []struct {
		name string
		file string
		body string
	}{
		{"user settings.xml", ".m2/settings.xml", "<settings><mirrors><mirror><id>evil</id><url>http://evil.example/repo</url><mirrorOf>*</mirrorOf></mirror></mirrors></settings>"},
		{"user toolchains.xml", ".m2/toolchains.xml", "<toolchains><toolchain></toolchain></toolchains>"},
	}

	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			r.writeHome(t, change.file, change.body)
			after := r.resolveAction(t, "mvn test")
			current, ok := fingerprintValue(after, KeyMavenConfig)
			if !ok {
				t.Fatal("want a maven-config fingerprint")
			}
			if current == previous {
				t.Errorf("adding %s must change maven-config", change.file)
			}
			previous = current
		})
	}
}

func TestMavenConfigFingerprintCoversTheBuildFiles(t *testing.T) {
	r := mavenRepo(t)

	before := r.resolveAction(t, "mvn test")
	previous, ok := fingerprintValue(before, KeyMavenConfig)
	if !ok {
		t.Fatalf("fingerprints = %+v, want maven-config", before.Fingerprints)
	}

	changes := []struct {
		name string
		file string
		body string
	}{
		{"root pom", "pom.xml", `<project><artifactId>demo</artifactId><version>2</version></project>`},
		{"new module pom", "core/pom.xml", `<project><artifactId>core</artifactId></project>`},
		{"wrapper", ".mvn/wrapper/maven-wrapper.properties", "distributionUrl=https://evil.example/x.zip\n"},
		{"wrapper script", "mvnw", "#!/bin/sh\nrm -rf ~\n"},
	}

	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			r.write(t, change.file, change.body)
			after := r.resolveAction(t, "mvn test")
			current, ok := fingerprintValue(after, KeyMavenConfig)
			if !ok {
				t.Fatal("want a maven-config fingerprint")
			}
			if current == previous {
				t.Errorf("changing %s must change maven-config", change.file)
			}
			previous = current
		})
	}
}
