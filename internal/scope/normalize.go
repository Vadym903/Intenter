package scope

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/platform"
)

// maxGlobMatches bounds how many entries a wildcard target is expanded to when
// checking for escaping matches (see NormalizeWord).
const maxGlobMatches = 256

// Input is one path word to normalize (PROTOTYPE_SPEC.md §16.1).
type Input struct {
	// Raw is the word as written in the command.
	Raw string
	// Text is the word after the dialect's supported expansions.
	Text string
	// Cwd is the command's effective cwd, after `cd` tracking.
	Cwd string
	// Ambiguous marks a word that still contains an unsupported variable; the
	// resulting target can never be approved or auto-allowed (§18.1 step 3).
	Ambiguous bool
	// Glob marks a word containing glob metacharacters.
	Glob bool
	// WindowsStyle enables backslash separators, drive letters, MSYS paths and
	// UNC detection. It follows the dialect, not the host OS (§14.4).
	WindowsStyle bool
}

// Normalize turns one word into a classified target by running steps 1–8 of
// §16.1. Step 1 (expansion) is the parser's job; Input.Text arrives expanded.
func (c *Context) Normalize(in Input) action.Target {
	target := action.Target{Raw: in.Raw, Status: action.TargetResolved}
	if in.Ambiguous {
		target.Status = action.TargetAmbiguous
	}

	text := in.Text
	if text == "" {
		text = in.Raw
	}

	// Step 2: separators, drive letters, MSYS paths and UNC.
	networkPath := false
	if in.WindowsStyle {
		text, networkPath = normalizeWindowsStyle(text)
	}

	// Step 7 (first half): a glob is classified through its longest literal
	// directory prefix; the wildcard remainder is kept for display.
	literal, globSuffix := splitGlob(text, in.Glob)

	// Step 3: absolutize against the effective cwd.
	absolute := c.absolutize(literal, in.Cwd)

	// Step 4: lexical clean, and traversal detection for paths that started
	// inside the workspace.
	cleaned := filepath.Clean(absolute)
	relativeInput := !isAbsolutePath(literal, in.WindowsStyle)
	if c.escapesWorkspace(literal, cleaned, relativeInput) {
		target.AddFlags(action.FlagTraversal)
	}

	// Step 5: canonicalize through symlinks and junctions.
	canonical := c.canonicalize(cleaned)
	if c.Workspace != "" && c.Rules.Under(cleaned, c.Workspace) && !c.Rules.Under(canonical, c.Workspace) {
		target.AddFlags(action.FlagSymlinkEscape)
	}

	target.Canonical = canonical

	// Step 6: best-effort stat.
	c.stat(&target)

	// Step 7 (second half): wildcard and breadth flags.
	if in.Glob {
		target.AddFlags(action.FlagWildcard)
	}
	if networkPath {
		target.AddFlags(action.FlagNetworkPath)
	}

	// Step 8: scope and sensitivity flags.
	c.classify(&target)

	target.Display = c.display(canonical, globSuffix)
	return target
}

// NormalizeWord returns the primary target for a word plus, for wildcards, any
// match that resolves outside the wildcard's own directory.
//
// §16.1 step 7 classifies a glob through its literal prefix, which keeps
// approvals stable as directory contents change. That alone would miss the
// case scenario S9 requires — `rm -rf build/*` where `build/link` points at
// HOME — so escaping matches are added as extra targets. Matches that stay
// inside the prefix are not enumerated.
func (c *Context) NormalizeWord(in Input) []action.Target {
	primary := c.Normalize(in)
	targets := []action.Target{primary}
	if !in.Glob || primary.Status == action.TargetAmbiguous {
		return targets
	}
	return append(targets, c.escapingMatches(in, primary)...)
}

func (c *Context) escapingMatches(in Input, primary action.Target) []action.Target {
	text := in.Text
	if text == "" {
		text = in.Raw
	}
	if in.WindowsStyle {
		text, _ = normalizeWindowsStyle(text)
	}
	pattern := c.absolutize(text, in.Cwd)

	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 || len(matches) > maxGlobMatches {
		return nil
	}

	prefix := primary.Canonical
	var out []action.Target
	for _, match := range matches {
		canonical := c.canonicalize(filepath.Clean(match))
		if c.Rules.Under(canonical, prefix) {
			continue
		}
		escaped := action.Target{
			Raw:       match,
			Canonical: canonical,
			Status:    action.TargetResolved,
		}
		escaped.AddFlags(action.FlagSymlinkEscape)
		c.stat(&escaped)
		c.classify(&escaped)
		escaped.Display = c.display(canonical, "")
		out = append(out, escaped)
	}
	return out
}

// absolutize resolves a path against the effective cwd (§16.1 step 3).
func (c *Context) absolutize(path, cwd string) string {
	if path == "" {
		return cwd
	}
	if isRooted(path) {
		return anchorRooted(path, cwd)
	}
	if isAbsolutePath(path, true) {
		return path
	}
	if cwd == "" {
		return path
	}
	return filepath.Join(cwd, path)
}

// isRooted reports whether a path starts at the root of a drive without naming
// the drive: `\etc\passwd` or `/etc/passwd` on a Windows host. filepath.IsAbs
// rejects it, yet it is not relative to the cwd either, and joining it under
// the cwd would turn `rm -rf /` into a delete of the project directory.
func isRooted(path string) bool {
	return filepath.Separator == '\\' && path != "" && os.IsPathSeparator(path[0]) &&
		filepath.VolumeName(path) == ""
}

// anchorRooted puts a rooted path on the drive of the cwd — where the shell
// that wrote it would look — or on the process's own drive when there is no
// cwd.
func anchorRooted(path, cwd string) string {
	if volume := filepath.VolumeName(cwd); volume != "" {
		return volume + path
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// escapesWorkspace reports whether a path that was written as relative, or that
// lexically sits inside the workspace, ends up outside it (§16.1 step 4).
func (c *Context) escapesWorkspace(original, cleaned string, relativeInput bool) bool {
	if c.Workspace == "" {
		return false
	}
	if !strings.Contains(original, "..") {
		return false
	}
	if !relativeInput && !c.Rules.Under(filepath.Clean(original), c.Workspace) {
		return false
	}
	return !c.Rules.Under(cleaned, c.Workspace)
}

// canonicalize resolves symlinks and junctions on the longest existing prefix
// and appends the non-existing remainder lexically (§16.1 step 5, research R-14).
func (c *Context) canonicalize(path string) string {
	if path == "" {
		return path
	}
	remainder := ""
	current := path

	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			if remainder == "" {
				return filepath.Clean(resolved)
			}
			return filepath.Clean(filepath.Join(resolved, remainder))
		}
		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Clean(path)
		}
		remainder = filepath.Join(filepath.Base(current), remainder)
		current = parent
	}
}

// stat fills in existence, directory and symlink information (§16.1 step 6).
func (c *Context) stat(target *action.Target) {
	if target.Canonical == "" {
		return
	}
	if info, err := os.Lstat(target.Canonical); err == nil {
		target.Exists = true
		target.IsSymlink = info.Mode()&os.ModeSymlink != 0
		target.IsDir = info.IsDir()
		if target.IsSymlink {
			if resolved, err := os.Stat(target.Canonical); err == nil {
				target.IsDir = resolved.IsDir()
			}
		}
	}
}

// display renders the path the way approvals and explanations show it:
// workspace-relative under W, ~-relative under HOME, absolute otherwise
// (§13.4). Forward slashes keep approvals comparable across platforms.
func (c *Context) display(canonical, globSuffix string) string {
	base := c.displayBase(canonical)
	if globSuffix == "" {
		return base
	}
	if strings.HasSuffix(base, "/") {
		return base + globSuffix
	}
	return base + "/" + globSuffix
}

func (c *Context) displayBase(canonical string) string {
	if canonical == "" {
		return ""
	}
	if c.Workspace != "" && c.Rules.Under(canonical, c.Workspace) {
		if c.Rules.EqualPath(canonical, c.Workspace) {
			return "."
		}
		if rel, err := filepath.Rel(c.Workspace, canonical); err == nil {
			return "./" + filepath.ToSlash(rel)
		}
	}
	if c.Home != "" && c.Rules.Under(canonical, c.Home) {
		if c.Rules.EqualPath(canonical, c.Home) {
			return "~"
		}
		if rel, err := filepath.Rel(c.Home, canonical); err == nil {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return canonical
}

// splitGlob returns the longest literal directory prefix of a pattern and the
// wildcard remainder (§16.1 step 7).
func splitGlob(text string, isGlob bool) (literal, suffix string) {
	if !isGlob {
		return text, ""
	}
	index := strings.IndexAny(text, "*?[")
	if index < 0 {
		return text, ""
	}
	cut := strings.LastIndexAny(text[:index], `/\`)
	if cut < 0 {
		// The pattern has no directory part: it matches inside the cwd.
		return ".", text
	}
	return text[:cut+1], text[cut+1:]
}

// normalizeWindowsStyle applies Windows path rules regardless of the host OS:
// backslash separators, MSYS `/c/…` paths and UNC detection (§16.1 step 2).
func normalizeWindowsStyle(text string) (normalized string, networkPath bool) {
	if strings.HasPrefix(text, `\\`) || strings.HasPrefix(text, "//") {
		return filepath.FromSlash(text), true
	}
	if converted, ok := msysToWindows(text); ok {
		return converted, false
	}
	if filepath.Separator == '\\' {
		return filepath.FromSlash(text), false
	}
	// On a POSIX host, backslashes in a Windows-dialect command are still
	// separators; converting them keeps cross-platform tests meaningful.
	return strings.ReplaceAll(text, `\`, "/"), false
}

// msysToWindows rewrites Git Bash style `/c/Users/u` into `C:\Users\u`.
func msysToWindows(text string) (string, bool) {
	if len(text) < 3 || text[0] != '/' || text[2] != '/' {
		return text, false
	}
	drive := text[1]
	isLetter := (drive >= 'a' && drive <= 'z') || (drive >= 'A' && drive <= 'Z')
	if !isLetter {
		return text, false
	}
	rest := strings.ReplaceAll(text[3:], "/", string(filepath.Separator))
	return strings.ToUpper(string(drive)) + ":" + string(filepath.Separator) + rest, true
}

// isAbsolutePath reports whether a path is absolute, optionally applying
// Windows rules on a POSIX host. A rooted path on a Windows host counts: it
// is anchored at a drive root, not at the cwd.
func isAbsolutePath(path string, windowsStyle bool) bool {
	if filepath.IsAbs(path) || isRooted(path) {
		return true
	}
	if !windowsStyle {
		return false
	}
	if len(path) >= 2 && path[1] == ':' {
		return true
	}
	return strings.HasPrefix(path, `\\`)
}

// NewContext builds a scope context for one workspace.
//
// The reference paths are canonicalized here rather than trusted from the
// caller. Classification compares canonical target paths (I-14), so a home or
// workspace root reached through a symlink would silently stop matching its own
// contents — and a HOME that does not match means rule R2 stops protecting it.
func NewContext(rules platform.PathRules, home, workspace, tempDir string, generatedExtra []string) *Context {
	return &Context{
		Rules:          rules,
		Home:           canonicalPath(home),
		Workspace:      canonicalPath(workspace),
		TempDir:        canonicalPath(tempDir),
		GeneratedExtra: generatedExtra,
		generatedCache: make(map[string]bool),
	}
}

// canonicalPath resolves symlinks where possible, falling back to a lexical
// clean for a path that does not exist.
func canonicalPath(path string) string {
	if path == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}
