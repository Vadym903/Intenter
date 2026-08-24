package resolver

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/platform"
	"github.com/Vadym903/Intenter/internal/scope"
)

// Context bundles the request context with the scope classifier built for the
// same workspace.
type Context struct {
	Action *action.Context
	Scope  *scope.Context

	// fingerprints memoizes the aggregate build fingerprints for the life of
	// one request. Hashing the Gradle or Maven tree is the most expensive step
	// of resolution; a command line naming the tool many times must not pay
	// for it many times, or the time budget runs out before the last command
	// — and the last command is where an evasion would put the dangerous one.
	mu           sync.Mutex
	fingerprints map[string]fingerprintMemo
}

type fingerprintMemo struct {
	fingerprint action.Fingerprint
	err         error
}

// Fingerprint returns the memoized result of compute for key, computing it once
// per request. It re-reads nothing across requests: every request builds a
// fresh Context, so §15.6's rule that inputs are re-hashed on every evaluation
// still holds.
func (c *Context) Fingerprint(key string, compute func() (action.Fingerprint, error)) (action.Fingerprint, error) {
	if c == nil {
		return compute()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if memo, ok := c.fingerprints[key]; ok {
		return memo.fingerprint, memo.err
	}
	fingerprint, err := compute()
	if c.fingerprints == nil {
		c.fingerprints = make(map[string]fingerprintMemo)
	}
	c.fingerprints[key] = fingerprintMemo{fingerprint: fingerprint, err: err}
	return fingerprint, err
}

// OK reports whether filesystem decisions can be made in this context.
func (c *Context) OK() bool { return c != nil && c.Action != nil && c.Action.OK() }

// ContextBuilder establishes the workspace context for a request and caches the
// expensive parts per workspace root (§13.2, §16.2).
type ContextBuilder struct {
	platform platform.Platform
	config   config.Config

	mu    sync.Mutex
	scope map[string]*scope.Context
}

// NewContextBuilder creates a builder for one daemon instance.
func NewContextBuilder(p platform.Platform, cfg config.Config) *ContextBuilder {
	return &ContextBuilder{platform: p, config: cfg, scope: make(map[string]*scope.Context)}
}

// Build establishes the context for one request. It never fails: an unusable
// workspace yields context_status = WORKSPACE_UNDEFINED, which makes every
// filesystem-targeting action ASK (§16.2, §18.1 step 3).
func (b *ContextBuilder) Build(cwd, projectHint string) *Context {
	home := b.platform.HomeDir()
	temp := b.platform.TempDir()

	canonicalCwd := canonicalize(cwd)
	workspace, reason := b.findWorkspace(canonicalCwd, canonicalize(projectHint), home)

	actionCtx := &action.Context{
		HomeDir:  home,
		TempDir:  temp,
		Platform: runtime.GOOS,
		Status:   action.ContextOK,
	}

	if workspace == "" {
		actionCtx.Status = action.ContextWorkspaceUndefined
		actionCtx.StatusReason = reason
		return &Context{
			Action: actionCtx,
			Scope:  b.scopeContext("", home, temp),
		}
	}

	actionCtx.WorkspaceRoot = workspace
	actionCtx.ProjectID = action.ProjectID(workspace)
	actionCtx.Git = ReadGitInfo(workspace, home)
	actionCtx.PackageManager = DetectPackageManager(workspace, home)

	scopeCtx := b.scopeContext(workspace, home, temp)
	actionCtx.GeneratedRoots = scopeCtx.GeneratedRoots()

	return &Context{Action: actionCtx, Scope: scopeCtx}
}

// scopeContext returns the cached classifier for a workspace, creating it on
// first use (§13.2: cached per workspace).
func (b *ContextBuilder) scopeContext(workspace, home, temp string) *scope.Context {
	b.mu.Lock()
	defer b.mu.Unlock()

	if cached, ok := b.scope[workspace]; ok {
		return cached
	}

	rules := b.platform.PathRules().WithSensitive(b.sensitiveExtra(workspace)...)
	created := scope.NewContext(rules, home, workspace, temp, b.config.Scope.GeneratedDirsExtra)
	b.scope[workspace] = created
	return created
}

// sensitiveExtra combines Intenter's self-protection paths with the
// configured extras (§16.6).
func (b *ContextBuilder) sensitiveExtra(workspace string) []string {
	extra := scope.SelfProtectionPaths(b.platform, b.claudeSettingsPaths(workspace))
	return append(extra, b.config.Policy.SensitivePathsExtra...)
}

// claudeSettingsPaths lists the agent settings files Intenter reads or
// manages, so an agent cannot rewrite its own hooks or grant itself permission
// through a shell command (§16.6).
//
// The workspace files matter because they carry the consent Intenter imports:
// a shell write to `<W>/.claude/settings.local.json` could add an allow rule
// that the next command's import turns into an approval. Making them sensitive
// forces that write to be a blocked change rather than an ordinary edit.
//
// The paths are derived from the home directory and the workspace only; no
// adapter code is imported here (INVARIANT I-7).
func (b *ContextBuilder) claudeSettingsPaths(workspace string) []string {
	var paths []string
	if home := b.platform.HomeDir(); home != "" {
		claudeDir := filepath.Join(home, ".claude")
		paths = append(paths,
			platform.RecursivePattern(claudeDir),
			filepath.Join(claudeDir, "settings.json"),
		)
	}
	if workspace != "" {
		claudeDir := filepath.Join(workspace, ".claude")
		paths = append(paths,
			filepath.Join(claudeDir, "settings.json"),
			filepath.Join(claudeDir, "settings.local.json"),
		)
	}
	return paths
}

// findWorkspace applies §16.2: the nearest ancestor of cwd containing `.git`,
// else the project hint when cwd lies inside it, else cwd — and then validates
// the candidate.
func (b *ContextBuilder) findWorkspace(cwd, hint, home string) (workspace string, reason string) {
	candidate := nearestGitRoot(cwd)
	if candidate == "" {
		if hint != "" && b.rules().Under(cwd, hint) {
			candidate = hint
		} else {
			candidate = cwd
		}
	}
	if candidate == "" {
		return "", "no working directory was reported"
	}
	if reason := b.invalidWorkspaceReason(candidate, home); reason != "" {
		return "", reason
	}
	return candidate, ""
}

// invalidWorkspaceReason rejects workspaces that would make approvals absurdly
// broad: HOME, an ancestor of HOME, a filesystem or drive root, or a SYSTEM
// location (§16.2).
func (b *ContextBuilder) invalidWorkspaceReason(candidate, home string) string {
	rules := b.rules()

	if rules.IsRoot(candidate) {
		return "the working directory is a filesystem root"
	}
	if home != "" {
		if rules.EqualPath(candidate, home) {
			return "the working directory is the home directory"
		}
		if rules.Under(home, candidate) {
			return "the working directory contains the home directory"
		}
	}
	if rules.IsSystem(candidate) {
		return "the working directory is a system location"
	}
	return ""
}

func (b *ContextBuilder) rules() platform.PathRules { return b.platform.PathRules() }

// nearestGitRoot walks up from dir looking for a `.git` directory or file.
func nearestGitRoot(dir string) string {
	current := dir
	for current != "" {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
	return ""
}

// fileExists reports whether a path exists and is a regular file; marker files
// decide package-manager and generated-root detection. A FIFO or device under a
// marker's name is not a marker — reading it would block the request.
func fileExists(path string) bool { return isRegularFile(path) }

// canonicalize resolves a path through symlinks where possible, so the
// workspace root and every target are compared in the same form (I-14).
func canonicalize(path string) string {
	if path == "" {
		return ""
	}
	absolute := path
	if !filepath.IsAbs(absolute) {
		if resolved, err := filepath.Abs(absolute); err == nil {
			absolute = resolved
		}
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(absolute)
}
