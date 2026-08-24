package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Vadym903/Intenter/internal/platform"
)

// Scope names a settings file's precedence level (§11.6).
type Scope string

const (
	ScopeManaged Scope = "managed"
	ScopeUser    Scope = "user"
	ScopeProject Scope = "project"
	ScopeLocal   Scope = "local"
)

// Permissions is the part of a Claude settings file Intenter reads.
type Permissions struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny"`
	Ask   []string `json:"ask"`
}

// Settings is the subset of a Claude settings file Intenter parses. Every
// other key is ignored here and preserved verbatim when setup edits the file.
type Settings struct {
	Permissions Permissions `json:"permissions"`
}

// SettingsFile is one discovered settings file and its permission rules.
type SettingsFile struct {
	Scope       Scope
	Path        string
	Exists      bool
	Permissions Permissions
}

// managedSettingsPath returns the platform's managed-policy location, which an
// administrator may use to set rules for every user.
func managedSettingsPath(hostOS string) string {
	switch hostOS {
	case "darwin":
		return "/Library/Application Support/ClaudeCode/managed-settings.json"
	case "windows":
		return filepath.Join(os.Getenv("PROGRAMDATA"), "ClaudeCode", "managed-settings.json")
	default:
		return "/etc/claude-code/managed-settings.json"
	}
}

// SettingsReader discovers Claude's settings files and caches what it parsed.
//
// The cache is keyed on modification time and size, so a file the user edits is
// re-read on the next hook without a restart (§11.6).
type SettingsReader struct {
	platform platform.Platform
	// userOverride replaces the user-scope path, for `--settings` and tests.
	userOverride string

	mu    sync.Mutex
	cache map[string]cachedSettings
}

type cachedSettings struct {
	modTimeUnixNano int64
	size            int64
	permissions     Permissions
}

// NewSettingsReader builds a reader for one platform.
func NewSettingsReader(p platform.Platform, userOverride string) *SettingsReader {
	return &SettingsReader{
		platform:     p,
		userOverride: userOverride,
		cache:        make(map[string]cachedSettings),
	}
}

// UserSettingsPath is the file `intenter setup claude` installs hooks into.
func (r *SettingsReader) UserSettingsPath() string {
	if r.userOverride != "" {
		return r.userOverride
	}
	home := r.platform.HomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".claude", "settings.json")
}

// Discover lists the settings files that apply, in the precedence order of
// §11.6. Files that do not exist are still listed, so callers can report where
// Intenter looked.
func (r *SettingsReader) Discover(projectDir string) []SettingsFile {
	paths := []struct {
		scope Scope
		path  string
	}{
		{ScopeManaged, managedSettingsPath(r.platform.OS())},
		{ScopeUser, r.UserSettingsPath()},
	}

	if root := gitRootOf(projectDir); root != "" {
		paths = append(paths,
			struct {
				scope Scope
				path  string
			}{ScopeProject, filepath.Join(root, ".claude", "settings.json")},
			struct {
				scope Scope
				path  string
			}{ScopeLocal, filepath.Join(root, ".claude", "settings.local.json")},
		)
	}

	files := make([]SettingsFile, 0, len(paths))
	for _, candidate := range paths {
		if candidate.path == "" {
			continue
		}
		permissions, exists := r.read(candidate.path)
		files = append(files, SettingsFile{
			Scope:       candidate.scope,
			Path:        candidate.path,
			Exists:      exists,
			Permissions: permissions,
		})
	}
	return files
}

// read parses one settings file, reusing the cached parse while the file is
// unchanged. A malformed file yields no rules rather than an error: it must not
// stop the hook from answering.
func (r *SettingsReader) read(path string) (Permissions, bool) {
	info, err := os.Stat(path)
	if err != nil {
		r.forget(path)
		return Permissions{}, false
	}

	r.mu.Lock()
	cached, ok := r.cache[path]
	r.mu.Unlock()
	if ok && cached.modTimeUnixNano == info.ModTime().UnixNano() && cached.size == info.Size() {
		return cached.permissions, true
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return Permissions{}, false
	}
	var settings Settings
	if err := json.Unmarshal(content, &settings); err != nil {
		// An unparsable settings file means Intenter cannot tell what the
		// user permitted, which is the same as having permitted nothing.
		return Permissions{}, true
	}

	r.mu.Lock()
	r.cache[path] = cachedSettings{
		modTimeUnixNano: info.ModTime().UnixNano(),
		size:            info.Size(),
		permissions:     settings.Permissions,
	}
	r.mu.Unlock()
	return settings.Permissions, true
}

func (r *SettingsReader) forget(path string) {
	r.mu.Lock()
	delete(r.cache, path)
	r.mu.Unlock()
}

// gitRootOf walks up from a directory looking for `.git`, which may be a
// directory or a worktree's pointer file (§11.6).
func gitRootOf(dir string) string {
	current := strings.TrimSpace(dir)
	if current == "" {
		return ""
	}
	if absolute, err := filepath.Abs(current); err == nil {
		current = absolute
	}
	for {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}
