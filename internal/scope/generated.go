package scope

import (
	"os"
	"path/filepath"
)

// nodeGeneratedDirs are build/cache directories of the JavaScript ecosystem.
// They only count when their parent directory holds a package.json (§16.4).
var nodeGeneratedDirs = map[string]bool{
	"node_modules": true, "dist": true, "build": true, "coverage": true,
	".next": true, ".nuxt": true, ".turbo": true, ".cache": true,
	".parcel-cache": true, "storybook-static": true,
}

// gradleMarkers identify a Gradle project directory whose `build` is generated.
var gradleMarkers = []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"}

// UnderGeneratedRoot reports whether a canonical path lies in a generated root
// of the workspace (§16.4).
//
// Rather than scanning the workspace, this walks from the path up to W and
// tests each ancestor, so a deep node_modules tree costs a few stat calls.
func (c *Context) UnderGeneratedRoot(path string) bool {
	if c.Workspace == "" || !c.Rules.Under(path, c.Workspace) {
		return false
	}

	current := filepath.Clean(path)
	for {
		if c.isGeneratedRoot(current) {
			return true
		}
		if c.Rules.EqualPath(current, c.Workspace) {
			return false
		}
		parent := filepath.Dir(current)
		if parent == current || !c.Rules.Under(parent, c.Workspace) {
			return false
		}
		current = parent
	}
}

// isGeneratedRoot applies the G(W) rules to one directory, with memoization.
func (c *Context) isGeneratedRoot(dir string) bool {
	c.mu.Lock()
	if cached, ok := c.generatedCache[dir]; ok {
		c.mu.Unlock()
		return cached
	}
	c.mu.Unlock()

	result := c.computeGeneratedRoot(dir)

	c.mu.Lock()
	if c.generatedCache == nil {
		c.generatedCache = make(map[string]bool)
	}
	c.generatedCache[dir] = result
	c.mu.Unlock()
	return result
}

func (c *Context) computeGeneratedRoot(dir string) bool {
	if c.Rules.EqualPath(dir, c.Workspace) {
		return false
	}

	name := filepath.Base(dir)
	parent := filepath.Dir(dir)

	// Configured extras, workspace-relative (config scope.generated_dirs_extra).
	for _, extra := range c.GeneratedExtra {
		if extra == "" {
			continue
		}
		candidate := extra
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(c.Workspace, candidate)
		}
		if c.Rules.EqualPath(dir, filepath.Clean(candidate)) {
			return true
		}
	}

	// Node: a known build directory next to a package.json.
	if nodeGeneratedDirs[name] && fileExists(filepath.Join(parent, "package.json")) {
		return true
	}

	// Gradle: `build` next to a Gradle build file, plus <W>/.gradle and
	// buildSrc/build.
	if name == "build" {
		for _, marker := range gradleMarkers {
			if fileExists(filepath.Join(parent, marker)) {
				return true
			}
		}
		if c.Rules.EqualPath(parent, filepath.Join(c.Workspace, "buildSrc")) {
			return true
		}
	}
	if c.Rules.EqualPath(dir, filepath.Join(c.Workspace, ".gradle")) {
		return true
	}

	// Maven: `target` next to a pom.xml.
	if name == "target" && fileExists(filepath.Join(parent, "pom.xml")) {
		return true
	}

	return false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// GeneratedRoots enumerates the generated roots directly under the workspace.
// It is used for reporting and for the declared CLEAN envelope; classification
// itself uses UnderGeneratedRoot.
func (c *Context) GeneratedRoots() []string {
	if c.Workspace == "" {
		return nil
	}
	var out []string
	c.collectGeneratedRoots(c.Workspace, 0, &out)
	return out
}

// maxGeneratedScanDepth bounds the search for generated roots; deeper trees are
// still classified correctly by UnderGeneratedRoot.
const maxGeneratedScanDepth = 3

func (c *Context) collectGeneratedRoots(dir string, depth int, out *[]string) {
	if depth > maxGeneratedScanDepth {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		child := filepath.Join(dir, entry.Name())
		if c.isGeneratedRoot(child) {
			// A generated root that is a symlink escaping the workspace is not
			// generated (§16.4, I-14).
			if canonical := c.canonicalize(child); c.Rules.Under(canonical, c.Workspace) {
				*out = append(*out, canonical)
			}
			continue
		}
		c.collectGeneratedRoots(child, depth+1, out)
	}
}
