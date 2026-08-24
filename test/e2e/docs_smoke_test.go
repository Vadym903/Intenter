package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Documentation that shows a command which no longer exists is worse than no
// documentation, because the reader has no way to tell. The getting-started
// walkthrough is the page a newcomer types verbatim, so the commands in it are
// run here against the real binary.
//
// Blocks opt in. A fenced ```console block preceded by `<!-- smoke -->` is
// executed; each `<!-- expect: "text" -->` before it names a substring the
// output must contain. `<!-- manual -->` marks the steps that need a real
// Claude Code session — a prompt, an answer in a dialog — which cannot be
// driven from a test. Those are not executed, and the harness stands in for
// them, so the commands that depend on their result can still be checked.

const smokeDoc = "getting-started.md"

// demoPath is the throwaway directory the walkthrough tells the reader to make.
// A test must not write there — it is a real shared path and two runs would
// collide — so it is redirected into the test's own workspace.
const demoPath = "/tmp/ag-demo"

// smokeBlock is one runnable example from the documentation.
type smokeBlock struct {
	// Line is where the block starts, for a failure message that points at it.
	Line int
	// Script is the block's `$ `-prefixed lines, joined so that a `cd` in one
	// applies to the next exactly as it would for a reader.
	Script string
	// Expect lists substrings the output must contain.
	Expect []string
}

func TestDocsSmokeCommandsStillWork(t *testing.T) {
	path := filepath.Join(repoRoot(), "docs", smokeDoc)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	blocks := parseSmokeBlocks(string(content))
	if len(blocks) == 0 {
		t.Fatalf("%s has no <!-- smoke --> blocks; either the markers were lost "+
			"or the walkthrough no longer shows any commands", smokeDoc)
	}

	env := newDocsEnv(t)
	t.Logf("running %d documented command blocks from %s", len(blocks), smokeDoc)

	// In document order and against one environment, because that is how the
	// page is read: each step assumes the previous one happened.
	for _, block := range blocks {
		output, code := env.runScript(t, block.Script)
		if code != 0 {
			t.Fatalf("%s:%d: exited %d\n--- commands ---\n%s\n--- output ---\n%s",
				smokeDoc, block.Line, code, block.Script, output)
		}
		for _, want := range block.Expect {
			if !strings.Contains(output, want) {
				t.Errorf("%s:%d: output does not contain %q\n--- commands ---\n%s\n--- output ---\n%s",
					smokeDoc, block.Line, want, block.Script, output)
			}
		}
	}
}

// docsEnv is a machine set up as far through the walkthrough as a test can get
// on its own.
type docsEnv struct {
	*Env
	demo string
	// path is what a documented command sees as PATH: this build's binary and
	// a Claude shim, and nothing from the machine running the tests.
	path string
}

func newDocsEnv(t *testing.T) *docsEnv {
	t.Helper()

	env := NewEnv(t)
	env.HideRealClaude()
	shims := env.claudeShim(t)

	demo := env.NewWorkspace("ag-demo")
	if err := os.MkdirAll(filepath.Join(demo, "dist"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// The project the walkthrough's step 3 builds. It is created up front as
	// well because the step after it depends on an evaluation, and an
	// evaluation of `npm run cleanup` only resolves if the script exists.
	// Step 3 then writes the same file again, which is how a reader following
	// along would leave it.
	manifest := `{"name":"demo","scripts":{"cleanup":"rm -rf ./dist"}}`
	if err := os.WriteFile(filepath.Join(demo, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	// Standing in for step 4's `<!-- manual -->` block: a real session where
	// the user asks Claude to run the cleanup script. Without it there is no
	// audit event, and `intenter approve 1` has nothing to approve.
	env.PreToolUseIn("docs-session", "toolu_docs_1", demo, "npm run cleanup")

	return &docsEnv{
		Env:  env,
		demo: demo,
		path: strings.Join([]string{filepath.Dir(env.Binary), shims, "/usr/bin", "/bin"},
			string(os.PathListSeparator)),
	}
}

// claudeShim puts a fake `claude` on PATH so `setup claude` has something to
// detect. The walkthrough's own text assumes Claude Code is installed.
func (e *Env) claudeShim(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the shim relies on a POSIX shebang")
	}

	dir := filepath.Join(e.Home, "shims")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "claude"),
		[]byte("#!/bin/sh\necho '2.1.233'\n"), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	if e.ExtraEnv == nil {
		e.ExtraEnv = map[string]string{}
	}
	return dir
}

// runScript runs one block's commands the way a reader would: in one shell, in
// the demo directory, with this build's binary as `intenter`.
func (d *docsEnv) runScript(t *testing.T, script string) (string, int) {
	t.Helper()

	// The reader's throwaway directory becomes the test's own, so nothing is
	// written to a shared path.
	script = strings.ReplaceAll(script, demoPath, d.demo)

	cmd := exec.Command("sh", "-c", script)
	cmd.Dir = d.demo
	cmd.Env = append(d.environ(), "PATH="+d.path)
	out, err := cmd.CombinedOutput()

	if err != nil {
		var exitErr *exec.ExitError
		if ok := asExitError(err, &exitErr); ok {
			return string(out), exitErr.ExitCode()
		}
		t.Fatalf("run script: %v\n%s", err, out)
	}
	return string(out), 0
}

// parseSmokeBlocks extracts the marked, runnable examples from a page.
func parseSmokeBlocks(content string) []smokeBlock {
	var blocks []smokeBlock
	lines := strings.Split(content, "\n")

	marked := false
	var expect []string

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])

		switch {
		case line == "<!-- smoke -->":
			marked = true
		case line == "<!-- manual -->":
			// Explicitly not runnable, and it must not inherit a stray marker.
			marked, expect = false, nil
		case strings.HasPrefix(line, "<!-- expect:"):
			if value := betweenQuotes(line); value != "" {
				expect = append(expect, value)
			}
		case strings.HasPrefix(line, "```console") && marked:
			block := smokeBlock{Line: i + 1, Expect: expect}
			var commands []string
			for i++; i < len(lines) && strings.TrimSpace(lines[i]) != "```"; i++ {
				if command, ok := strings.CutPrefix(lines[i], "$ "); ok {
					commands = append(commands, command)
				}
			}
			if len(commands) > 0 {
				block.Script = strings.Join(commands, "\n")
				blocks = append(blocks, block)
			}
			marked, expect = false, nil
		case strings.HasPrefix(line, "```"):
			// An unmarked block: reset, so a marker cannot drift onto a later
			// example and quietly stop testing the one it was written for.
			marked, expect = false, nil
		}
	}
	return blocks
}

// betweenQuotes returns the text inside the first pair of double quotes.
func betweenQuotes(line string) string {
	start := strings.Index(line, `"`)
	if start < 0 {
		return ""
	}
	end := strings.Index(line[start+1:], `"`)
	if end < 0 {
		return ""
	}
	return line[start+1 : start+1+end]
}
