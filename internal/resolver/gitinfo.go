package resolver

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
)

// hookNamesByOperation lists the client-side hooks that make a git operation
// execute arbitrary workspace code (§15.7).
var hookNamesByOperation = map[action.SemanticOp][]string{
	action.OpGitCommit:   {"pre-commit", "prepare-commit-msg", "commit-msg", "post-commit"},
	action.OpGitPush:     {"pre-push"},
	action.OpGitCheckout: {"post-checkout"},
}

// HooksForOperation returns the hook names that apply to a git operation.
func HooksForOperation(op action.SemanticOp) []string { return hookNamesByOperation[op] }

// ReadGitInfo reads everything Intenter needs from a repository without ever
// executing git (§15.4, §15.7). repoRoot is the directory containing `.git`.
func ReadGitInfo(repoRoot, home string) *action.GitInfo {
	gitPath := filepath.Join(repoRoot, ".git")
	gitDir, ok := resolveGitDir(gitPath, repoRoot)
	if !ok {
		return nil
	}

	info := &action.GitInfo{GitDir: gitDir}
	info.CurrentBranch = readCurrentBranch(gitDir)

	config := parseGitConfig(filepath.Join(gitDir, "config"))
	info.Remotes, info.RemoteURLs = remoteHosts(config)
	info.DefaultBranch = readDefaultBranch(gitDir, info.Remotes)

	info.HooksDir = resolveHooksDir(repoRoot, gitDir, config, home)
	info.HooksPresent = presentHooks(info.HooksDir)
	return info
}

// resolveGitDir handles `.git` being a directory or a file pointing elsewhere
// (worktrees and submodules, §15.7).
func resolveGitDir(gitPath, repoRoot string) (string, bool) {
	info, err := os.Stat(gitPath)
	if err != nil {
		return "", false
	}
	if info.IsDir() {
		return gitPath, true
	}

	raw, err := readConfigFile(gitPath)
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(raw))
	target, found := strings.CutPrefix(line, "gitdir:")
	if !found {
		return "", false
	}
	target = strings.TrimSpace(target)
	if !filepath.IsAbs(target) {
		target = filepath.Join(repoRoot, target)
	}
	return filepath.Clean(target), true
}

// readCurrentBranch parses HEAD; a detached HEAD yields no branch.
func readCurrentBranch(gitDir string) string {
	raw, err := readConfigFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(raw))
	ref, found := strings.CutPrefix(line, "ref:")
	if !found {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(ref), "refs/heads/")
}

// readDefaultBranch resolves refs/remotes/<remote>/HEAD, preferring origin
// (§18.2 R7 uses it as a protected branch).
func readDefaultBranch(gitDir string, remotes map[string]string) string {
	others := make([]string, 0, len(remotes))
	for name := range remotes {
		if name != "origin" {
			others = append(others, name)
		}
	}
	sort.Strings(others)

	names := make([]string, 0, len(remotes)+1)
	if _, ok := remotes["origin"]; ok {
		names = append(names, "origin")
	}
	names = append(names, others...)

	for _, remote := range names {
		raw, err := readConfigFile(filepath.Join(gitDir, "refs", "remotes", remote, "HEAD"))
		if err != nil {
			continue
		}
		line := strings.TrimSpace(string(raw))
		ref, found := strings.CutPrefix(line, "ref:")
		if !found {
			continue
		}
		ref = strings.TrimSpace(ref)
		if branch := strings.TrimPrefix(ref, "refs/remotes/"+remote+"/"); branch != ref {
			return branch
		}
	}
	return ""
}

// gitConfig is a minimal representation of an INI-style git config.
type gitConfig struct {
	// values maps "section.subsection.key" (lower-cased section and key) to a value.
	values map[string]string
}

func (c gitConfig) get(key string) string { return c.values[strings.ToLower(key)] }

// parseGitConfig reads the subset of git's INI format Intenter needs. It
// never executes git (§15.4).
func parseGitConfig(path string) gitConfig {
	config := gitConfig{values: make(map[string]string)}

	content, err := readConfigFile(path)
	if err != nil {
		return config
	}

	section := ""
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = parseSectionHeader(line)
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.Trim(strings.TrimSpace(value), `"`)
		if section == "" {
			continue
		}
		config.values[section+"."+key] = value
	}
	return config
}

// parseSectionHeader turns `[remote "origin"]` into `remote.origin`.
func parseSectionHeader(line string) string {
	inner := strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
	name, sub, found := strings.Cut(inner, " ")
	name = strings.ToLower(strings.TrimSpace(name))
	if !found {
		return name
	}
	sub = strings.Trim(strings.TrimSpace(sub), `"`)
	if sub == "" {
		return name
	}
	return name + "." + sub
}

// remoteHosts extracts remote names with their hosts and URLs.
func remoteHosts(config gitConfig) (hosts map[string]string, urls map[string]string) {
	hosts = make(map[string]string)
	urls = make(map[string]string)
	for key, value := range config.values {
		name, found := strings.CutPrefix(key, "remote.")
		if !found || !strings.HasSuffix(name, ".url") {
			continue
		}
		remote := strings.TrimSuffix(name, ".url")
		if remote == "" {
			continue
		}
		urls[remote] = value
		if host := RemoteHost(value); host != "" {
			hosts[remote] = host
		}
	}
	if len(hosts) == 0 {
		hosts = nil
	}
	if len(urls) == 0 {
		urls = nil
	}
	return hosts, urls
}

// RemoteHost parses the host out of a git remote URL: `git@host:path`,
// `ssh://user@host/path`, `https://host/path` and `git://host/path` (§15.4).
func RemoteHost(url string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		return ""
	}

	if scheme, rest, found := strings.Cut(url, "://"); found {
		_ = scheme
		if _, after, ok := strings.Cut(rest, "@"); ok {
			rest = after
		}
		host, _, _ := strings.Cut(rest, "/")
		host, _, _ = strings.Cut(host, ":")
		return host
	}

	// scp-like syntax: [user@]host:path
	if before, _, found := strings.Cut(url, ":"); found {
		if _, after, ok := strings.Cut(before, "@"); ok {
			return after
		}
		if !strings.Contains(before, string(filepath.Separator)) && before != "" {
			return before
		}
	}
	return ""
}

// resolveHooksDir applies core.hooksPath from the repository config, then from
// the user's global config, then falls back to <gitdir>/hooks (§15.7).
//
// git runs hooks from the top level of the working tree, so a relative
// core.hooksPath is resolved against the repository root rather than the
// gitdir. Resolving it against the gitdir would look in `.git/.githooks`, miss
// the `.githooks` convention entirely, and report a repository with hooks as
// hook-free.
func resolveHooksDir(repoRoot, gitDir string, config gitConfig, home string) string {
	if configured := config.get("core.hookspath"); configured != "" {
		return absoluteHooksPath(configured, repoRoot)
	}
	for _, candidate := range globalGitConfigPaths(home) {
		global := parseGitConfig(candidate)
		if configured := global.get("core.hookspath"); configured != "" {
			return absoluteHooksPath(configured, home)
		}
	}
	return filepath.Join(gitDir, "hooks")
}

func absoluteHooksPath(configured, base string) string {
	if strings.HasPrefix(configured, "~/") || configured == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			configured = filepath.Join(home, strings.TrimPrefix(configured, "~"))
		}
	}
	if filepath.IsAbs(configured) {
		return filepath.Clean(configured)
	}
	return filepath.Clean(filepath.Join(base, configured))
}

func globalGitConfigPaths(home string) []string {
	if home == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".gitconfig"),
		filepath.Join(home, ".config", "git", "config"),
	}
}

// presentHooks lists hook files that would actually run, ignoring git's
// `.sample` templates (§15.7).
func presentHooks(hooksDir string) []string {
	if hooksDir == "" {
		return nil
	}
	entries, err := os.ReadDir(hooksDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".sample") {
			continue
		}
		out = append(out, entry.Name())
	}
	sort.Strings(out)
	return out
}
