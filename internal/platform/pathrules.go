package platform

import (
	"path/filepath"
	"strings"
)

// PathRules describes how paths behave on this OS and which locations are
// system-owned, credential-bearing, tool caches or temporary
// (PROTOTYPE_SPEC.md §16.3, §16.5, §16.6).
//
// All directory patterns are absolute and use the platform separator. A pattern
// ending in "/**" (or "\**") matches the directory itself and everything under
// it; other patterns may use filepath.Match wildcards.
type PathRules struct {
	// CaseInsensitive is true on macOS (default APFS) and Windows.
	CaseInsensitive bool
	// SystemRoots are OS-owned roots; anything under them is SYSTEM except the
	// temp carve-outs below.
	SystemRoots []string
	// TempRoots are shared/user temp locations. They are OUTSIDE_WORKSPACE with
	// flag temp even when they live under a system root.
	TempRoots []string
	// StandardHomeDirs are home sub-directory names that are `broad` when
	// targeted as a whole (§16.5).
	StandardHomeDirs []string
	// SensitiveDirs are credential-bearing locations (§16.6).
	SensitiveDirs []string
	// SensitiveNames are final-component patterns such as id_rsa* or *.pem.
	SensitiveNames []string
	// ToolCacheDirs are package/build tool caches that DECLARED envelopes may
	// write to.
	ToolCacheDirs []string
}

// sensitiveNamePatterns are credential file names on every OS (§16.6).
var sensitiveNamePatterns = []string{
	"id_rsa*", "id_ed25519*", "id_ecdsa*", "id_dsa*",
	"*.pem", "*.key", "*.p12", "*.pfx", "*.jks", "*.keystore",
	".env", ".env.*", "*.kdbx",
	"credentials.json", "service-account*.json",
	".netrc", "_netrc", ".git-credentials", ".npmrc", ".pypirc",
}

// homeSensitiveDirs are credential locations relative to HOME, shared by all
// platforms (§16.6).
var homeSensitiveDirs = []string{
	".ssh", ".aws", ".gnupg", ".kube", ".azure", ".claude", ".anthropic",
	"config/gh", "config/gcloud",
}

// homeSensitiveFiles are individual credential files relative to HOME.
var homeSensitiveFiles = []string{
	".docker/config.json", ".netrc", "_netrc", ".npmrc", ".yarnrc", ".yarnrc.yml",
	".pypirc", ".gitconfig", ".git-credentials",
}

// homePersistenceFiles are shell and login startup files relative to HOME.
// Writing one is code execution on the next shell or login, so a shell write is
// blocked and a read asks — the same protection credential files get (R5). A
// changed startup file is at least as dangerous as a changed ~/.gitconfig,
// which was already protected while these were not.
var homePersistenceFiles = []string{
	".bashrc", ".bash_profile", ".bash_login", ".profile", ".bash_logout",
	".zshrc", ".zshenv", ".zprofile", ".zlogin", ".zlogout",
	".kshrc", ".cshrc", ".tcshrc", ".login", ".logout",
	".config/fish/config.fish", ".pam_environment",
}

// homePersistenceDirs are startup directories relative to HOME where any file
// runs at shell start, login or session boot. The whole subtree is sensitive.
var homePersistenceDirs = []string{
	".config/fish/conf.d", ".config/environment.d",
	".config/autostart", ".config/systemd/user",
}

// homeToolCacheDirs are tool caches relative to HOME, shared by all platforms.
var homeToolCacheDirs = []string{
	".gradle", ".m2", ".npm", ".yarn", ".pnpm-store", ".cache",
}

// standardHomeDirNames are the home sub-directories that count as broad (§16.5).
var standardHomeDirNames = []string{
	"Desktop", "Documents", "Downloads", "Pictures", "Music", "Movies", "Videos",
	".ssh", ".config",
}

// buildPathRules assembles the rules for the current OS.
func buildPathRules(home, temp string) PathRules {
	rules := PathRules{
		CaseInsensitive:  osCaseInsensitive(),
		SystemRoots:      osSystemRoots(),
		TempRoots:        withResolvedVariants(osTempRoots(temp)),
		StandardHomeDirs: append(append([]string{}, standardHomeDirNames...), osStandardHomeDirs()...),
		SensitiveNames:   append([]string{}, sensitiveNamePatterns...),
	}

	for _, dir := range homeSensitiveDirs {
		rules.SensitiveDirs = append(rules.SensitiveDirs, recursivePattern(filepath.Join(home, filepath.FromSlash(dir))))
	}
	for _, file := range homeSensitiveFiles {
		rules.SensitiveDirs = append(rules.SensitiveDirs, filepath.Join(home, filepath.FromSlash(file)))
	}
	for _, file := range homePersistenceFiles {
		rules.SensitiveDirs = append(rules.SensitiveDirs, filepath.Join(home, filepath.FromSlash(file)))
	}
	for _, dir := range homePersistenceDirs {
		rules.SensitiveDirs = append(rules.SensitiveDirs, recursivePattern(filepath.Join(home, filepath.FromSlash(dir))))
	}
	rules.SensitiveDirs = append(rules.SensitiveDirs, osSensitiveDirs(home)...)

	for _, dir := range homeToolCacheDirs {
		rules.ToolCacheDirs = append(rules.ToolCacheDirs, recursivePattern(filepath.Join(home, dir)))
	}
	rules.ToolCacheDirs = append(rules.ToolCacheDirs, osToolCacheDirs(home)...)

	return rules
}

// recursivePattern turns a directory into a pattern matching it and everything
// below it.
func recursivePattern(dir string) string {
	return filepath.Clean(dir) + string(filepath.Separator) + "**"
}

// withResolvedVariants adds the symlink-resolved form of each path. Scope
// classification compares canonical paths, and macOS resolves /tmp and
// /var/folders to /private/…, so both spellings must be recognized (§16.1).
func withResolvedVariants(paths []string) []string {
	out := make([]string, 0, len(paths)*2)
	seen := make(map[string]bool, len(paths)*2)
	for _, path := range paths {
		for _, variant := range []string{filepath.Clean(path), resolvePath(path)} {
			if variant == "" || seen[variant] {
				continue
			}
			seen[variant] = true
			out = append(out, variant)
		}
	}
	return out
}

func resolvePath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	return filepath.Clean(resolved)
}

// RecursivePattern exposes recursivePattern to packages that extend the rules
// from configuration.
func RecursivePattern(dir string) string { return recursivePattern(dir) }

// WithSensitive returns a copy of the rules with additional sensitive patterns,
// used for Intenter self-protection and policy.sensitive_paths_extra (§16.6).
func (r PathRules) WithSensitive(patterns ...string) PathRules {
	out := r.clone()
	for _, p := range patterns {
		if strings.TrimSpace(p) == "" {
			continue
		}
		out.SensitiveDirs = append(out.SensitiveDirs, filepath.Clean(p))
	}
	return out
}

func (r PathRules) clone() PathRules {
	out := r
	out.SystemRoots = append([]string(nil), r.SystemRoots...)
	out.TempRoots = append([]string(nil), r.TempRoots...)
	out.StandardHomeDirs = append([]string(nil), r.StandardHomeDirs...)
	out.SensitiveDirs = append([]string(nil), r.SensitiveDirs...)
	out.SensitiveNames = append([]string(nil), r.SensitiveNames...)
	out.ToolCacheDirs = append([]string(nil), r.ToolCacheDirs...)
	return out
}

// EqualPath compares two absolute paths under the platform's case rule.
func (r PathRules) EqualPath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if r.CaseInsensitive {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Under reports whether path is the root itself or lies below it.
func (r PathRules) Under(path, root string) bool {
	path, root = filepath.Clean(path), filepath.Clean(root)
	if r.EqualPath(path, root) {
		return true
	}
	prefix := root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if r.CaseInsensitive {
		return strings.HasPrefix(strings.ToLower(path), strings.ToLower(prefix))
	}
	return strings.HasPrefix(path, prefix)
}

// StrictlyUnder reports whether path lies below root but is not root itself.
func (r PathRules) StrictlyUnder(path, root string) bool {
	return r.Under(path, root) && !r.EqualPath(path, root)
}

// Match reports whether path matches one pattern of the given set.
func (r PathRules) Match(patterns []string, path string) bool {
	for _, pattern := range patterns {
		if r.matchOne(pattern, path) {
			return true
		}
	}
	return false
}

func (r PathRules) matchOne(pattern, path string) bool {
	pattern = filepath.Clean(pattern)
	path = filepath.Clean(path)

	if base, ok := strings.CutSuffix(pattern, string(filepath.Separator)+"**"); ok {
		return r.Under(path, base)
	}
	if strings.ContainsAny(pattern, "*?[") {
		return r.matchGlob(pattern, path)
	}
	return r.EqualPath(pattern, path)
}

func (r PathRules) matchGlob(pattern, path string) bool {
	if r.CaseInsensitive {
		pattern, path = strings.ToLower(pattern), strings.ToLower(path)
	}
	ok, err := filepath.Match(pattern, path)
	return err == nil && ok
}

// IsSystem reports whether path is a system location, excluding the temp
// carve-outs (§16.3).
//
// The filesystem root and drive roots are SYSTEM as paths in their own right
// (deleting them is hard rule R1), but their whole subtree is not: every
// workspace and home directory lives under `/` too. Only the named platform
// roots such as /usr or C:\Windows are SYSTEM including their contents.
func (r PathRules) IsSystem(path string) bool {
	if r.IsTemp(path) {
		return false
	}
	if r.IsRoot(path) {
		return true
	}
	for _, root := range r.SystemRoots {
		if r.IsRoot(root) {
			continue
		}
		if r.Under(path, root) {
			return true
		}
	}
	return false
}

// IsTemp reports whether path is inside a temp root.
func (r PathRules) IsTemp(path string) bool {
	for _, root := range r.TempRoots {
		if r.Under(path, root) {
			return true
		}
	}
	return false
}

// IsTempRoot reports whether path is a temp root itself; deleting it wholesale
// is blocked by R3.
func (r PathRules) IsTempRoot(path string) bool {
	for _, root := range r.TempRoots {
		if r.EqualPath(path, root) {
			return true
		}
	}
	return false
}

// IsSensitive reports whether path is a credential location by directory or by
// file name (§16.6).
func (r PathRules) IsSensitive(path string) bool {
	if r.Match(r.SensitiveDirs, path) {
		return true
	}
	base := filepath.Base(path)
	for _, pattern := range r.SensitiveNames {
		if r.matchName(pattern, base) {
			return true
		}
	}
	return false
}

func (r PathRules) matchName(pattern, name string) bool {
	if r.CaseInsensitive {
		pattern, name = strings.ToLower(pattern), strings.ToLower(name)
	}
	ok, err := filepath.Match(pattern, name)
	return err == nil && ok
}

// IsToolCache reports whether path is inside a known package or build tool
// cache (§16.6).
func (r PathRules) IsToolCache(path string) bool { return r.Match(r.ToolCacheDirs, path) }

// IsStandardHomeDir reports whether path is one of the home sub-directories
// that are `broad` when targeted as a whole (§16.5).
func (r PathRules) IsStandardHomeDir(path, home string) bool {
	for _, name := range r.StandardHomeDirs {
		if r.EqualPath(path, filepath.Join(home, name)) {
			return true
		}
	}
	return false
}

// IsRoot reports whether path is a filesystem root or a drive root such as C:\.
func (r PathRules) IsRoot(path string) bool {
	cleaned := filepath.Clean(path)
	if cleaned == string(filepath.Separator) {
		return true
	}
	return isDriveRoot(cleaned)
}
