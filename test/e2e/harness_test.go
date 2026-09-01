// Package e2e drives the built intenter binary the way Claude Code and a
// user would: hook payloads on stdin, CLI commands in a shell, one real daemon
// against a temporary home directory (PROTOTYPE_SPEC.md §28.3, §29).
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// hookTimeout bounds one hook invocation; a hook slower than this would be a
// user-visible stall.
const hookTimeout = 20 * time.Second

var (
	buildOnce   sync.Once
	binaryPath  string
	buildErr    error
	buildTmpDir string
)

// binary builds the CLI once per test run and returns its path.
func binary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "intenter-e2e-bin")
		if err != nil {
			buildErr = err
			return
		}
		buildTmpDir = dir

		name := "intenter"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		binaryPath = filepath.Join(dir, name)

		cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/intenter")
		cmd.Dir = repoRoot()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			buildErr = fmt.Errorf("build intenter: %v\n%s", err, stderr.String())
		}
	})
	if buildErr != nil {
		t.Fatalf("%v", buildErr)
	}
	return binaryPath
}

// repoRoot locates the module root from this file's own path.
func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// Env is one isolated Intenter installation: its own home, data, config and
// runtime directories, and its own daemon.
type Env struct {
	t *testing.T

	Binary     string
	Home       string
	DataDir    string
	ConfigDir  string
	RuntimeDir string
	Workspace  string

	// ExtraEnv overrides environment variables for child processes.
	ExtraEnv map[string]string

	daemon *exec.Cmd
	done   chan error
}

// NewEnv creates the directories and starts a daemon serving them.
func NewEnv(t *testing.T) *Env {
	t.Helper()

	base := shortTempDir(t)
	env := &Env{
		t:          t,
		Binary:     binary(t),
		Home:       filepath.Join(base, "home"),
		DataDir:    filepath.Join(base, "data"),
		ConfigDir:  filepath.Join(base, "config"),
		RuntimeDir: filepath.Join(base, "run"),
	}
	for _, dir := range []string{env.Home, env.DataDir, env.ConfigDir, env.RuntimeDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	env.Workspace = env.NewWorkspace("demo")
	env.StartDaemon()
	return env
}

// shortTempDir keeps the unix socket path within its length limit (§10.1).
func shortTempDir(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return t.TempDir()
	}
	dir, err := os.MkdirTemp("/tmp", "age")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// environ is the environment every child process inherits.
func (e *Env) environ() []string {
	environ := append(os.Environ(),
		"INTENTER_TEST_MODE=1",
		"INTENTER_TEST_HOME="+e.Home,
		"INTENTER_DATA_DIR="+e.DataDir,
		"INTENTER_CONFIG_DIR="+e.ConfigDir,
		"INTENTER_RUNTIME_DIR="+e.RuntimeDir,
		"INTENTER_ENDPOINT=",
		"HOME="+e.Home,
		// CI runners export XDG_CONFIG_HOME pointing at the real home; fish
		// follows it, so it must follow HOME into the fixture too.
		"XDG_CONFIG_HOME="+filepath.Join(e.Home, ".config"),
		"CLAUDE_PROJECT_DIR="+e.Workspace,
	)
	// Later entries win, so an override replaces whatever was inherited.
	for name, value := range e.ExtraEnv {
		environ = append(environ, name+"="+value)
	}
	return environ
}

// HideRealClaude points PATH at an empty directory, so a Claude Code installed
// on the machine running the tests cannot be mistaken for the fixture's.
func (e *Env) HideRealClaude() {
	e.t.Helper()
	empty := filepath.Join(e.RuntimeDir, "empty-path")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		e.t.Fatalf("mkdir: %v", err)
	}
	if e.ExtraEnv == nil {
		e.ExtraEnv = map[string]string{}
	}
	e.ExtraEnv["PATH"] = empty
}

// StartDaemon runs `intenter daemon run` until the test ends.
func (e *Env) StartDaemon() {
	e.t.Helper()
	if e.daemon != nil {
		return
	}

	cmd := exec.Command(e.Binary, "daemon", "run")
	cmd.Env = e.environ()
	cmd.Dir = e.Workspace
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	if err := cmd.Start(); err != nil {
		e.t.Fatalf("start daemon: %v", err)
	}
	e.daemon = cmd
	e.done = make(chan error, 1)
	go func() { e.done <- cmd.Wait() }()

	e.t.Cleanup(func() {
		if e.daemon != nil {
			e.StopDaemon()
		}
	})

	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, _, code := e.CLI("daemon", "status"); code == 0 {
			return
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("daemon did not become ready:\n%s", output.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// AdoptRunningDaemon shuts down a daemon the harness did not start, which is
// what a hook's lazy start leaves behind.
func (e *Env) AdoptRunningDaemon() {
	e.t.Helper()
	e.t.Cleanup(func() {
		if _, _, code := e.CLI("daemon", "stop"); code != 0 {
			e.t.Logf("could not stop the lazily started daemon (exit %d)", code)
		}
	})
}

// ForgetDaemon releases the harness's claim on the daemon, for the scenarios
// that stop it through the CLI rather than the harness.
func (e *Env) ForgetDaemon() {
	e.t.Helper()
	if e.daemon == nil {
		return
	}
	_ = e.daemon.Process.Kill()
	select {
	case <-e.done:
	case <-time.After(10 * time.Second):
	}
	e.daemon = nil
}

// StopDaemon shuts the daemon down, for the scenarios that need it gone.
func (e *Env) StopDaemon() {
	e.t.Helper()
	if e.daemon == nil {
		return
	}
	_ = e.daemon.Process.Kill()
	select {
	case <-e.done:
	case <-time.After(10 * time.Second):
		e.t.Error("daemon did not exit")
	}
	e.daemon = nil
}

// NewWorkspace creates a git-backed project with a package.json and a
// generated dist directory.
func (e *Env) NewWorkspace(name string) string {
	e.t.Helper()

	root := filepath.Join(e.Home, "projects", name)
	for _, dir := range []string{filepath.Join(root, ".git"), filepath.Join(root, "dist"), filepath.Join(root, "src")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			e.t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	writeFile(e.t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeFile(e.t, filepath.Join(root, ".git", "config"),
		"[remote \"origin\"]\n\turl = git@github.com:acme/demo.git\n")
	writeFile(e.t, filepath.Join(root, "package.json"), `{"name":"demo","scripts":{}}`)
	writeFile(e.t, filepath.Join(root, "README.md"), "# demo\n")

	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		e.t.Fatalf("EvalSymlinks: %v", err)
	}
	return resolved
}

// WriteWorkspaceFile writes a file inside the workspace.
func (e *Env) WriteWorkspaceFile(relPath, content string) {
	e.t.Helper()
	path := filepath.Join(e.Workspace, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatalf("mkdir: %v", err)
	}
	writeFile(e.t, path, content)
}

// ReadWorkspaceFile reads a file from the workspace.
func (e *Env) ReadWorkspaceFile(relPath string) string {
	e.t.Helper()
	path := filepath.Join(e.Workspace, filepath.FromSlash(relPath))
	content, err := os.ReadFile(path)
	if err != nil {
		e.t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

// SetScripts replaces the package.json scripts block.
func (e *Env) SetScripts(scripts string) {
	e.t.Helper()
	e.WriteWorkspaceFile("package.json", `{"name":"demo","scripts":`+scripts+`}`)
}

// WriteConfig writes config.toml for the environment.
func (e *Env) WriteConfig(content string) {
	e.t.Helper()
	writeFile(e.t, filepath.Join(e.ConfigDir, "config.toml"), content)
}

// DisableLazyStart stops the hook from starting a daemon of its own, which is
// what a genuinely unreachable daemon looks like (§9.5 is otherwise
// self-healing).
func (e *Env) DisableLazyStart() {
	e.t.Helper()
	e.WriteConfig("[daemon]\nlazy_start = false\n")
}

// WriteClaudeSettings writes the project-local Claude settings file, which is
// where Claude persists "don't ask again" rules.
func (e *Env) WriteClaudeSettings(content string) {
	e.t.Helper()
	path := filepath.Join(e.Workspace, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatalf("mkdir: %v", err)
	}
	writeFile(e.t, path, content)
}

// CLI runs an intenter command in the workspace and returns stdout, stderr
// and the exit code.
func (e *Env) CLI(args ...string) (string, string, int) {
	e.t.Helper()

	cmd := exec.Command(e.Binary, args...)
	cmd.Env = e.environ()
	cmd.Dir = e.Workspace
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if ok := asExitError(err, &exitErr); ok {
			code = exitErr.ExitCode()
		} else {
			e.t.Fatalf("run %v: %v", args, err)
		}
	}
	return stdout.String(), stderr.String(), code
}

// MustCLI runs a command and fails the test unless it succeeded.
func (e *Env) MustCLI(args ...string) string {
	e.t.Helper()
	stdout, stderr, code := e.CLI(args...)
	if code != 0 {
		e.t.Fatalf("intenter %v exited %d\nstdout: %s\nstderr: %s", args, code, stdout, stderr)
	}
	return stdout
}

// HookResult is what one hook invocation produced.
type HookResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	// Output is the parsed JSON object, or nil when the hook stayed silent.
	Output map[string]any
}

// PermissionDecision returns the PreToolUse decision, or "" when the hook
// deferred.
func (r HookResult) PermissionDecision() string {
	output, _ := r.Output["hookSpecificOutput"].(map[string]any)
	if output == nil {
		return ""
	}
	decision, _ := output["permissionDecision"].(string)
	return decision
}

// PermissionBehavior returns the PermissionRequest behavior, or "".
func (r HookResult) PermissionBehavior() string {
	output, _ := r.Output["hookSpecificOutput"].(map[string]any)
	if output == nil {
		return ""
	}
	decision, _ := output["decision"].(map[string]any)
	if decision == nil {
		return ""
	}
	behavior, _ := decision["behavior"].(string)
	return behavior
}

// SystemMessage returns the systemMessage, or "".
func (r HookResult) SystemMessage() string {
	message, _ := r.Output["systemMessage"].(string)
	return message
}

// Reason returns the permissionDecisionReason, or "".
func (r HookResult) Reason() string {
	output, _ := r.Output["hookSpecificOutput"].(map[string]any)
	if output == nil {
		return ""
	}
	reason, _ := output["permissionDecisionReason"].(string)
	return reason
}

// Silent reports whether the hook produced no output at all.
func (r HookResult) Silent() bool { return r.Output == nil }

// Deferred reports whether the hook left the decision to the agent. It may
// still have printed a systemMessage explaining what the command resolves to;
// what makes it a deferral is the absence of a permission decision (§11.3).
func (r HookResult) Deferred() bool {
	return r.PermissionDecision() == "" && r.PermissionBehavior() == ""
}

// RunHook feeds one payload to `intenter hook claude`.
func (e *Env) RunHook(payload map[string]any) HookResult {
	e.t.Helper()

	encoded, err := json.Marshal(payload)
	if err != nil {
		e.t.Fatalf("encode payload: %v", err)
	}

	cmd := exec.Command(e.Binary, "hook", "claude")
	cmd.Env = e.environ()
	cmd.Dir = e.Workspace
	cmd.Stdin = bytes.NewReader(encoded)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	timer := time.AfterFunc(hookTimeout, func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	runErr := cmd.Run()
	timer.Stop()

	result := HookResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if runErr != nil {
		var exitErr *exec.ExitError
		if ok := asExitError(runErr, &exitErr); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			e.t.Fatalf("run hook: %v", runErr)
		}
	}

	// INVARIANT I-12: the hook always exits 0, whatever happened.
	if result.ExitCode != 0 {
		e.t.Errorf("hook exited %d, want 0\nstdout: %s\nstderr: %s",
			result.ExitCode, result.Stdout, result.Stderr)
	}

	trimmed := strings.TrimSpace(result.Stdout)
	if trimmed == "" {
		return result
	}
	if err := json.Unmarshal([]byte(trimmed), &result.Output); err != nil {
		e.t.Fatalf("hook wrote invalid JSON %q: %v", trimmed, err)
	}
	return result
}

// PreToolUse builds and runs a PreToolUse payload.
func (e *Env) PreToolUse(session, toolUseID, command string) HookResult {
	return e.RunHook(map[string]any{
		"hook_event_name": "PreToolUse",
		"session_id":      session,
		"cwd":             e.Workspace,
		"permission_mode": "default",
		"tool_name":       "Bash",
		"tool_use_id":     toolUseID,
		"tool_input":      map[string]any{"command": command},
	})
}

// PreToolUseIn runs a PreToolUse payload with a different working directory.
func (e *Env) PreToolUseIn(session, toolUseID, cwd, command string) HookResult {
	return e.RunHook(map[string]any{
		"hook_event_name": "PreToolUse",
		"session_id":      session,
		"cwd":             cwd,
		"permission_mode": "default",
		"tool_name":       "Bash",
		"tool_use_id":     toolUseID,
		"tool_input":      map[string]any{"command": command},
	})
}

// PreToolUseMode runs a PreToolUse payload in a specific permission mode.
func (e *Env) PreToolUseMode(session, toolUseID, mode, command string) HookResult {
	return e.RunHook(map[string]any{
		"hook_event_name": "PreToolUse",
		"session_id":      session,
		"cwd":             e.Workspace,
		"permission_mode": mode,
		"tool_name":       "Bash",
		"tool_use_id":     toolUseID,
		"tool_input":      map[string]any{"command": command},
	})
}

// PermissionRequest builds and runs a PermissionRequest payload, which carries
// no tool_use_id.
func (e *Env) PermissionRequest(session, command string, suggestions []any) HookResult {
	payload := map[string]any{
		"hook_event_name": "PermissionRequest",
		"session_id":      session,
		"cwd":             e.Workspace,
		"permission_mode": "default",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": command},
	}
	if suggestions != nil {
		payload["permission_suggestions"] = suggestions
	}
	return e.RunHook(payload)
}

// PostToolUse builds and runs a PostToolUse payload.
func (e *Env) PostToolUse(session, toolUseID, command string) HookResult {
	return e.RunHook(map[string]any{
		"hook_event_name": "PostToolUse",
		"session_id":      session,
		"cwd":             e.Workspace,
		"permission_mode": "default",
		"tool_name":       "Bash",
		"tool_use_id":     toolUseID,
		"tool_input":      map[string]any{"command": command},
		"tool_response": map[string]any{
			"stdout":      "",
			"stderr":      "",
			"interrupted": false,
			"is_error":    false,
		},
	})
}

// SessionEnd builds and runs a SessionEnd payload.
func (e *Env) SessionEnd(session, reason string) HookResult {
	return e.RunHook(map[string]any{
		"hook_event_name":        "SessionEnd",
		"session_id":             session,
		"cwd":                    e.Workspace,
		"session_end_reason":     reason,
		"last_assistant_message": "done",
	})
}

// LatestEventID returns the id of the most recent audit event.
func (e *Env) LatestEventID() int64 {
	e.t.Helper()
	out := e.MustCLI("history", "--limit", "1", "--json")

	var events []struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &events); err != nil {
		e.t.Fatalf("parse history: %v\n%s", err, out)
	}
	if len(events) == 0 {
		e.t.Fatal("no audit events recorded")
	}
	return events[0].ID
}

// EventByCommand returns the newest audit event for a raw command.
func (e *Env) EventByCommand(command string) map[string]any {
	e.t.Helper()
	out := e.MustCLI("history", "--limit", "50", "--json")

	var events []map[string]any
	if err := json.Unmarshal([]byte(out), &events); err != nil {
		e.t.Fatalf("parse history: %v\n%s", err, out)
	}
	for _, event := range events {
		if event["raw_command"] == command {
			return event
		}
	}
	e.t.Fatalf("no audit event for %q in:\n%s", command, out)
	return nil
}

// Decision returns the decision and class recorded for a command. The listing
// shape names the class `class`; the full event names it `decision_class`.
func (e *Env) Decision(command string) (string, string) {
	e.t.Helper()
	event := e.EventByCommand(command)
	decision, _ := event["decision"].(string)
	class, _ := event["class"].(string)
	return decision, class
}

// FullEvent reads one complete audit event, which carries everything the
// listing leaves out: the resolved action, prompt and execution fields.
func (e *Env) FullEvent(id int64) map[string]any {
	e.t.Helper()
	out := e.MustCLI("history", "show", itoa(id), "--json")

	var event map[string]any
	if err := json.Unmarshal([]byte(out), &event); err != nil {
		e.t.Fatalf("parse event: %v\n%s", err, out)
	}
	return event
}

// EventIDFor returns the id of the newest audit event for a raw command.
func (e *Env) EventIDFor(command string) int64 {
	e.t.Helper()
	event := e.EventByCommand(command)
	id, ok := event["id"].(float64)
	if !ok {
		e.t.Fatalf("event for %q has no id: %+v", command, event)
	}
	return int64(id)
}

// ApprovalCount returns how many approvals exist across all projects.
func (e *Env) ApprovalCount() int {
	e.t.Helper()
	out := e.MustCLI("approvals", "--all", "--json")

	var summaries []map[string]any
	if err := json.Unmarshal([]byte(out), &summaries); err != nil {
		e.t.Fatalf("parse approvals: %v\n%s", err, out)
	}
	return len(summaries)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// asExitError unwraps an *exec.ExitError without importing errors twice.
func asExitError(err error, target **exec.ExitError) bool {
	if exitErr, ok := err.(*exec.ExitError); ok {
		*target = exitErr
		return true
	}
	return false
}

func TestHarnessBuildsAndRunsTheBinary(t *testing.T) {
	env := NewEnv(t)

	out := env.MustCLI("version")
	if !strings.Contains(out, "intenter") {
		t.Errorf("version output = %q", out)
	}

	status := env.MustCLI("daemon", "status")
	if !strings.Contains(strings.ToLower(status), "running") {
		t.Errorf("daemon status = %q, want a running daemon", status)
	}
}

func TestHarnessIsolatesState(t *testing.T) {
	// Nothing may be written outside the temporary directories.
	env := NewEnv(t)
	env.PreToolUse("session-1", "toolu_1", "git status")

	if _, err := os.Stat(filepath.Join(env.DataDir, "intenter.db")); err != nil {
		t.Errorf("the database must live in the test data directory: %v", err)
	}
}

// itoa renders an id for a CLI argument.
func itoa(value int64) string { return strconv.FormatInt(value, 10) }
