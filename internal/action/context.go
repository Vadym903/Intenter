package action

// Context is the runtime environment a request is evaluated against. It is
// established per request and cached per workspace root
// (PROTOTYPE_SPEC.md §13.2, §16.2).
type Context struct {
	WorkspaceRoot  string             `json:"workspace_root"`
	ProjectID      string             `json:"project_id"`
	HomeDir        string             `json:"home_dir"`
	TempDir        string             `json:"temp_dir"`
	Platform       string             `json:"platform"`
	GeneratedRoots []string           `json:"generated_roots,omitempty"`
	Git            *GitInfo           `json:"git,omitempty"`
	PackageManager PackageManagerInfo `json:"package_manager"`
	Status         ContextStatus      `json:"context_status"`
	// StatusReason explains a non-OK status in the audit log.
	StatusReason string `json:"context_status_reason,omitempty"`
}

// GitInfo is what Intenter reads out of a repository without ever executing
// git (PROTOTYPE_SPEC.md §15.4, §15.7).
type GitInfo struct {
	GitDir string `json:"gitdir"`
	// DefaultBranch is resolved from refs/remotes/<remote>/HEAD when present.
	DefaultBranch string `json:"default_branch,omitempty"`
	CurrentBranch string `json:"current_branch,omitempty"`
	// Remotes maps a remote name to the host parsed from its URL.
	Remotes map[string]string `json:"remotes,omitempty"`
	// RemoteURLs maps a remote name to its configured URL (audit metadata).
	RemoteURLs map[string]string `json:"remote_urls,omitempty"`
	HooksDir   string            `json:"hooks_dir,omitempty"`
	// HooksPresent lists hook file names that exist, ignoring *.sample.
	HooksPresent []string `json:"hooks_present,omitempty"`
}

// HasHook reports whether a client-side hook with the given name exists.
func (g *GitInfo) HasHook(name string) bool {
	if g == nil {
		return false
	}
	for _, h := range g.HooksPresent {
		if h == name {
			return true
		}
	}
	return false
}

// PackageManagerInfo describes how JavaScript scripts would be executed in this
// workspace (PROTOTYPE_SPEC.md §15.5.4).
type PackageManagerInfo struct {
	Kind PackageManagerKind `json:"kind"`
	// ScriptShell is the configured .npmrc script-shell value, if any.
	ScriptShell string `json:"script_shell,omitempty"`
	// ScriptShellSource records which file supplied ScriptShell.
	ScriptShellSource string `json:"script_shell_source,omitempty"`
	// YarnPath is .yarnrc.yml's yarnPath value, if set: a project-supplied
	// JavaScript file that every `yarn` invocation runs instead of the
	// installed package manager. A non-empty value means Intenter cannot know
	// what `yarn` actually does here (AG-142).
	YarnPath string `json:"yarn_path,omitempty"`
	// PnpmFile is the path of a present pnpm hooks file (.pnpmfile.cjs by
	// default, or the path .npmrc's pnpmfile key names). Its readPackage/
	// afterAllResolved hooks are arbitrary Node.js that pnpm runs during
	// dependency resolution regardless of --ignore-scripts, so a non-empty
	// value means Intenter cannot know what a `pnpm` invocation actually does
	// here (AG-144, the same class as YarnPath).
	PnpmFile string `json:"pnpmfile,omitempty"`
}

// OK reports whether the context is usable for filesystem-targeting decisions.
func (c *Context) OK() bool {
	return c != nil && c.Status == ContextOK
}
