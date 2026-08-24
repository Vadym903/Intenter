package scope

import (
	"path/filepath"
	"sync"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/platform"
)

// Context holds everything path classification depends on. One instance serves
// one workspace and is safe for concurrent use.
type Context struct {
	Rules     platform.PathRules
	Home      string
	Workspace string
	TempDir   string
	// GeneratedExtra lists additional generated roots, workspace-relative
	// (config scope.generated_dirs_extra, §16.4).
	GeneratedExtra []string

	mu             sync.Mutex
	generatedCache map[string]bool
}

// classify assigns the scope and the sensitivity, tool-cache, temp and breadth
// flags of a target (§16.3, §16.5, §16.6).
//
// INVARIANT I-14: classification uses the canonical, symlink-resolved path; a
// textual prefix under the workspace is never sufficient.
func (c *Context) classify(target *action.Target) {
	path := target.Canonical
	if path == "" {
		target.Scope = action.ScopeOutsideWorkspace
		return
	}

	target.Scope = c.scopeOf(path)

	if c.Rules.IsTemp(path) {
		target.AddFlags(action.FlagTemp)
	}
	if c.Rules.IsSensitive(path) {
		target.AddFlags(action.FlagSensitive)
	}
	if c.Rules.IsToolCache(path) {
		target.AddFlags(action.FlagToolCache)
	}
	if c.isBroad(path) {
		target.AddFlags(action.FlagBroad)
	}
}

// Classify exposes classification for callers that build targets themselves.
func (c *Context) Classify(target *action.Target) { c.classify(target) }

// scopeOf returns the scope of a canonical path. The scopes are disjoint and
// evaluated in the order of §16.3.
//
// The temp carve-out only disqualifies SYSTEM: a temp path is still classified
// as WORKSPACE or HOME when it happens to live there, and falls through to
// OUTSIDE_WORKSPACE otherwise (PathRules.IsSystem applies the carve-out).
func (c *Context) scopeOf(path string) action.Scope {
	if c.Rules.IsSystem(path) {
		return action.ScopeSystem
	}
	if c.underWorkspace(path) {
		if c.UnderGeneratedRoot(path) {
			return action.ScopeWorkspaceGenerated
		}
		return action.ScopeWorkspace
	}
	if c.Home != "" && c.Rules.Under(path, c.Home) {
		return action.ScopeHome
	}
	return action.ScopeOutsideWorkspace
}

func (c *Context) underWorkspace(path string) bool {
	return c.Workspace != "" && c.Rules.Under(path, c.Workspace)
}

// isBroad reports whether a path names a whole area rather than a single item:
// the workspace root, HOME, a standard home sub-directory, a filesystem or
// drive root, a system root or a temp root (§16.3, §16.5).
func (c *Context) isBroad(path string) bool {
	if c.Rules.IsRoot(path) {
		return true
	}
	if c.Workspace != "" && c.Rules.EqualPath(path, c.Workspace) {
		return true
	}
	if c.Home != "" && c.Rules.EqualPath(path, c.Home) {
		return true
	}
	if c.Home != "" && c.Rules.IsStandardHomeDir(path, c.Home) {
		return true
	}
	if c.Rules.IsTempRoot(path) {
		return true
	}
	for _, root := range c.Rules.SystemRoots {
		if c.Rules.EqualPath(path, root) {
			return true
		}
	}
	return false
}

// SelfProtectionPaths returns the Intenter locations that must be treated as
// sensitive, so an agent cannot disable or rewrite its own guard (§16.6).
func SelfProtectionPaths(p platform.Platform, claudeSettings []string) []string {
	paths := []string{
		platform.RecursivePattern(p.DataDir()),
		platform.RecursivePattern(p.ConfigDir()),
		platform.RecursivePattern(p.RuntimeDir()),
	}
	for _, settings := range claudeSettings {
		if settings != "" {
			paths = append(paths, filepath.Clean(settings))
		}
	}
	return paths
}
