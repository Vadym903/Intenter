package resolver

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
)

// Fingerprint limits (§15.1, §15.6).
const (
	// MaxFingerprintFiles caps how many files one aggregate fingerprint covers.
	MaxFingerprintFiles = 500
	// MaxFingerprintBytes caps the total content hashed for one aggregate.
	MaxFingerprintBytes = 50 << 20
	// AlwaysRehashBytes: inputs below this size are always re-read and re-hashed,
	// never served from a mtime-based cache (§15.6).
	AlwaysRehashBytes = 1 << 20
)

// ErrFingerprintTooLarge marks a fingerprint input that exceeds the caps; the
// action becomes UNRESOLVED rather than being approved on partial evidence.
var ErrFingerprintTooLarge = fmt.Errorf("resolver: fingerprint input exceeds the configured limits")

// Fingerprint key formats (data-model.md §1.6).
const (
	keyNpmScriptPrefix = "npm-script:"
	keyNpmConfigPrefix = "npm-config:"
	// KeyGradleConfig covers the Gradle wrapper, build scripts and buildSrc.
	KeyGradleConfig = "gradle-config"
	// KeyMavenConfig covers the Maven wrapper, .mvn and every pom.xml.
	KeyMavenConfig = "maven-config"
)

// NpmScriptKey builds the fingerprint key of one package.json script, e.g.
// "npm-script:package.json#scripts.cleanup" (§15.5.1 step 5).
func NpmScriptKey(relPackageJSON, scriptName string) string {
	return keyNpmScriptPrefix + filepath.ToSlash(relPackageJSON) + "#scripts." + scriptName
}

// NpmScriptShellKey is the fingerprint key of the configured script shell.
func NpmScriptShellKey() string { return keyNpmConfigPrefix + ".npmrc#script-shell" }

// NpmPackageManagerKey is the fingerprint key of package.json's packageManager.
func NpmPackageManagerKey() string { return keyNpmConfigPrefix + "package.json#packageManager" }

// ValueFingerprint hashes a configuration value. An unset value is recorded as
// "<unset>" so that setting it later invalidates the approval (§15.5.1 step 5).
func ValueFingerprint(key, value, description string) action.Fingerprint {
	if value == "" {
		value = "<unset>"
	}
	return action.Fingerprint{Key: key, Value: action.HashString(value), Description: description}
}

// TextFingerprint hashes text content with line-ending normalization.
func TextFingerprint(key, text, description string) action.Fingerprint {
	return action.Fingerprint{Key: key, Value: action.HashText([]byte(text)), Description: description}
}

// FileFingerprint hashes one file's content. Missing files are recorded as
// "<missing>" so that creating the file later invalidates the approval.
func FileFingerprint(key, path, description string) (action.Fingerprint, error) {
	info, err := os.Stat(path)
	if err != nil {
		return action.Fingerprint{Key: key, Value: action.HashString("<missing>"), Description: description}, nil
	}
	if info.Size() > MaxFingerprintBytes {
		return action.Fingerprint{}, fmt.Errorf("%w: %s is %d bytes", ErrFingerprintTooLarge, path, info.Size())
	}

	// Inputs below the threshold are always re-read: a stale fingerprint is the
	// one failure mode approval matching cannot detect (§15.6).
	content, err := readFingerprintInput(path, MaxFingerprintBytes)
	if err != nil {
		return action.Fingerprint{}, fmt.Errorf("resolver: read fingerprint input %s: %w", path, err)
	}
	return action.Fingerprint{Key: key, Value: action.HashText(content), Description: description}, nil
}

// readFingerprintInput reads one fingerprinted file, refusing anything that is
// not a regular file within the cap. The workspace can swap a file for a FIFO
// between the stat and the read; checking the open descriptor closes that gap.
func readFingerprintInput(path string, limit int64) ([]byte, error) {
	file, err := openNoFollowBlocking(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s", ErrNotRegularFile, path)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%w: %s is %d bytes", ErrFingerprintTooLarge, path, info.Size())
	}
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("%w: %s", ErrFingerprintTooLarge, path)
	}
	return content, nil
}

// FileSetFingerprint hashes an ordered set of files as one aggregate value:
// sha256 over the sorted (workspace-relative path, sha256) pairs, so that
// adding, removing or editing any member changes the fingerprint
// (§15.5.2, data-model.md §3).
func FileSetFingerprint(key, workspace string, paths []string, description string) (action.Fingerprint, error) {
	unique := make(map[string]bool, len(paths))
	ordered := make([]string, 0, len(paths))
	for _, path := range paths {
		clean := filepath.Clean(path)
		if unique[clean] {
			continue
		}
		unique[clean] = true
		ordered = append(ordered, clean)
	}
	if len(ordered) > MaxFingerprintFiles {
		return action.Fingerprint{}, fmt.Errorf("%w: %d files (limit %d)", ErrFingerprintTooLarge, len(ordered), MaxFingerprintFiles)
	}
	sort.Strings(ordered)

	pairs := make(map[string]string, len(ordered))
	var total int64
	for _, path := range ordered {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		total += info.Size()
		if total > MaxFingerprintBytes {
			return action.Fingerprint{}, fmt.Errorf("%w: %d bytes (limit %d)", ErrFingerprintTooLarge, total, MaxFingerprintBytes)
		}
		content, err := readFingerprintInput(path, MaxFingerprintBytes-total+info.Size())
		if err != nil {
			return action.Fingerprint{}, fmt.Errorf("resolver: read fingerprint input %s: %w", path, err)
		}
		pairs[relativeKey(workspace, path)] = action.HashText(content)
	}

	return action.Fingerprint{Key: key, Value: action.HashPairs(pairs), Description: description}, nil
}

// relativeKey renders a path relative to the workspace with forward slashes, so
// fingerprints are identical on every platform.
func relativeKey(workspace, path string) string {
	if workspace == "" {
		return filepath.ToSlash(path)
	}
	rel, err := filepath.Rel(workspace, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// FileSetOptions selects the files an aggregate fingerprint covers.
type FileSetOptions struct {
	// Roots are directories to walk; missing roots are skipped.
	Roots []string
	// Files are individual files to include; missing files are skipped.
	Files []string
	// MatchName keeps a file when it returns true for the file's base name.
	MatchName func(name string) bool
	// SkipDir skips a directory (and its subtree) when it returns true for the
	// directory's absolute path.
	SkipDir func(dir string) bool
}

// CollectFiles enumerates the files of a fingerprint set, applying the caps of
// §15.1. Exceeding a cap is an error, so an over-large project fails to
// UNRESOLVED instead of being approved on partial evidence.
func CollectFiles(opts FileSetOptions) ([]string, error) {
	seen := make(map[string]bool)
	var out []string

	add := func(path string) error {
		clean := filepath.Clean(path)
		if seen[clean] {
			return nil
		}
		info, err := os.Stat(clean)
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		seen[clean] = true
		out = append(out, clean)
		if len(out) > MaxFingerprintFiles {
			return fmt.Errorf("%w: more than %d files", ErrFingerprintTooLarge, MaxFingerprintFiles)
		}
		return nil
	}

	for _, file := range opts.Files {
		if err := add(file); err != nil {
			return nil, err
		}
	}

	for _, root := range opts.Roots {
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if entry.IsDir() {
				if opts.SkipDir != nil && opts.SkipDir(path) {
					return filepath.SkipDir
				}
				return nil
			}
			if opts.MatchName != nil && !opts.MatchName(entry.Name()) {
				return nil
			}
			return add(path)
		})
		if err != nil {
			return nil, err
		}
	}

	sort.Strings(out)
	return out, nil
}

// SkipGeneratedAndVendor is the standard directory filter for fingerprint
// walks: generated output and vendored dependencies never define behavior.
func SkipGeneratedAndVendor(name string) bool {
	switch strings.ToLower(name) {
	case "node_modules", ".git", "build", "dist", "target", ".gradle", "out", "coverage", ".idea", ".vscode":
		return true
	}
	return false
}
