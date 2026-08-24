package resolver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFingerprintKeyFormats(t *testing.T) {
	if got := NpmScriptKey("package.json", "cleanup"); got != "npm-script:package.json#scripts.cleanup" {
		t.Errorf("NpmScriptKey = %q", got)
	}
	if got := NpmScriptKey(filepath.Join("packages", "api", "package.json"), "test"); got != "npm-script:packages/api/package.json#scripts.test" {
		t.Errorf("nested keys must use forward slashes, got %q", got)
	}
	if got := NpmScriptShellKey(); got != "npm-config:.npmrc#script-shell" {
		t.Errorf("NpmScriptShellKey = %q", got)
	}
	if got := NpmPackageManagerKey(); got != "npm-config:package.json#packageManager" {
		t.Errorf("NpmPackageManagerKey = %q", got)
	}
}

func TestValueFingerprintRecordsUnsetDistinctly(t *testing.T) {
	unset := ValueFingerprint(NpmScriptShellKey(), "", "script shell")
	set := ValueFingerprint(NpmScriptShellKey(), "bash", "script shell")

	if unset.Value == set.Value {
		t.Error("setting a previously unset value must change the fingerprint (§15.5.1)")
	}
	if unset.Value == "" || len(unset.Value) != 64 {
		t.Errorf("fingerprint value = %q, want a hex sha256", unset.Value)
	}
	if unset.Description != "script shell" {
		t.Errorf("description = %q", unset.Description)
	}
}

func TestFileFingerprintTracksContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.json")
	if err := os.WriteFile(path, []byte(`{"scripts":{"cleanup":"rm -rf ./dist"}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	before, err := FileFingerprint("npm-script:package.json#scripts.cleanup", path, "cleanup script")
	if err != nil {
		t.Fatalf("FileFingerprint: %v", err)
	}

	if err := os.WriteFile(path, []byte(`{"scripts":{"cleanup":"rm -rf ~/Documents"}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	after, err := FileFingerprint("npm-script:package.json#scripts.cleanup", path, "cleanup script")
	if err != nil {
		t.Fatalf("FileFingerprint: %v", err)
	}

	if before.Value == after.Value {
		t.Error("a changed script must change the fingerprint (the invalidation hypothesis, §3)")
	}
}

func TestFileFingerprintOfMissingFile(t *testing.T) {
	dir := t.TempDir()
	missing, err := FileFingerprint("k", filepath.Join(dir, "absent"), "")
	if err != nil {
		t.Fatalf("a missing file must not be an error: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "absent"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	created, err := FileFingerprint("k", filepath.Join(dir, "absent"), "")
	if err != nil {
		t.Fatalf("FileFingerprint: %v", err)
	}
	if missing.Value == created.Value {
		t.Error("creating a previously missing file must change the fingerprint")
	}
}

func TestFileSetFingerprintCoversMembership(t *testing.T) {
	workspace := t.TempDir()
	write := func(rel, content string) string {
		path := filepath.Join(workspace, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		return path
	}

	settings := write("settings.gradle.kts", "rootProject.name = \"demo\"")
	build := write("app/build.gradle.kts", "plugins { java }")

	base, err := FileSetFingerprint(KeyGradleConfig, workspace, []string{settings, build}, "gradle build files")
	if err != nil {
		t.Fatalf("FileSetFingerprint: %v", err)
	}

	// Order must not matter.
	reordered, err := FileSetFingerprint(KeyGradleConfig, workspace, []string{build, settings}, "gradle build files")
	if err != nil {
		t.Fatalf("FileSetFingerprint: %v", err)
	}
	if base.Value != reordered.Value {
		t.Error("the aggregate must not depend on input order")
	}

	// Editing a member changes the aggregate.
	write("app/build.gradle.kts", "plugins { java }\ntasks.register(\"evil\") {}")
	edited, err := FileSetFingerprint(KeyGradleConfig, workspace, []string{settings, build}, "")
	if err != nil {
		t.Fatalf("FileSetFingerprint: %v", err)
	}
	if edited.Value == base.Value {
		t.Error("editing a build file must change gradle-config (S2)")
	}

	// Removing a member changes the aggregate.
	shrunk, err := FileSetFingerprint(KeyGradleConfig, workspace, []string{settings}, "")
	if err != nil {
		t.Fatalf("FileSetFingerprint: %v", err)
	}
	if shrunk.Value == edited.Value {
		t.Error("removing a build file must change gradle-config")
	}
}

func TestFileSetFingerprintRejectsTooManyFiles(t *testing.T) {
	workspace := t.TempDir()
	paths := make([]string, 0, MaxFingerprintFiles+1)
	for i := range MaxFingerprintFiles + 1 {
		path := filepath.Join(workspace, fmt.Sprintf("file-%d.gradle", i))
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		paths = append(paths, path)
	}

	if _, err := FileSetFingerprint(KeyGradleConfig, workspace, paths, ""); !errors.Is(err, ErrFingerprintTooLarge) {
		t.Errorf("error = %v, want ErrFingerprintTooLarge (§15.1)", err)
	}
}

func TestCollectFilesAppliesFiltersAndCaps(t *testing.T) {
	workspace := t.TempDir()
	mk := func(rel string) {
		path := filepath.Join(workspace, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	mk("build.gradle")
	mk("app/build.gradle.kts")
	mk("app/src/Main.java")
	mk("node_modules/pkg/build.gradle")
	mk("build/generated.gradle")

	files, err := CollectFiles(FileSetOptions{
		Roots: []string{workspace},
		Files: []string{filepath.Join(workspace, "gradlew")},
		MatchName: func(name string) bool {
			return strings.HasSuffix(name, ".gradle") || strings.HasSuffix(name, ".gradle.kts")
		},
		SkipDir: func(dir string) bool { return SkipGeneratedAndVendor(filepath.Base(dir)) },
	})
	if err != nil {
		t.Fatalf("CollectFiles: %v", err)
	}

	joined := strings.Join(files, "\n")
	if !strings.Contains(joined, "build.gradle") || !strings.Contains(joined, "build.gradle.kts") {
		t.Errorf("build files missing: %v", files)
	}
	if strings.Contains(joined, "node_modules") {
		t.Error("vendored directories must be skipped")
	}
	if strings.Contains(joined, filepath.Join("build", "generated.gradle")) {
		t.Error("generated directories must be skipped")
	}
	if strings.Contains(joined, "Main.java") {
		t.Error("MatchName must filter unrelated files")
	}
	if strings.Contains(joined, "gradlew") {
		t.Error("a missing explicit file must be skipped, not included")
	}
}

func TestCollectFilesIsSortedAndDeduplicated(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "pom.xml")
	if err := os.WriteFile(path, []byte("<project/>"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	files, err := CollectFiles(FileSetOptions{Files: []string{path, path, filepath.Clean(path)}})
	if err != nil {
		t.Fatalf("CollectFiles: %v", err)
	}
	if len(files) != 1 {
		t.Errorf("files = %v, want one entry", files)
	}
}
