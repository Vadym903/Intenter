package e2e

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// An update replaces the binary a user is in the middle of relying on. These
// tests exercise the whole thing against a real build of this repository,
// published by a local server: nothing here is stubbed except the release host.

// publishedVersion is what the fake release publishes. It is far ahead of any
// real version so the comparison can never be accidentally satisfied by the
// version the tests were built from.
const publishedVersion = "9.9.9"

// updateFixture is one installation the updater is allowed to replace, plus a
// release server offering it something newer.
type updateFixture struct {
	*Env
	// Installed is the copy of Intenter under test. It is a copy rather than
	// the shared build output, because these tests overwrite it.
	Installed string
	// URL is the fake release host.
	URL string
	// Requests counts what the host was asked for.
	Requests func() []string
}

func newUpdateFixture(t *testing.T) *updateFixture {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a second binary")
	}

	base := shortTempDir(t)
	installed := installCopy(t, binary(t), filepath.Join(base, "bin"))
	server, requests, signingKeyPath := publishRelease(t, publishedVersion)

	env := &Env{
		t:          t,
		Binary:     installed,
		Home:       filepath.Join(base, "home"),
		DataDir:    filepath.Join(base, "data"),
		ConfigDir:  filepath.Join(base, "config"),
		RuntimeDir: filepath.Join(base, "run"),
		ExtraEnv: map[string]string{
			"INTENTER_LATEST_URL":    server + "/releases/latest",
			"INTENTER_DOWNLOAD_BASE": server + "/releases/download",
			// The updater verifies checksums.txt.sig against this key instead
			// of the pinned release key; the override only applies under
			// INTENTER_TEST_MODE=1, which the harness always sets.
			"INTENTER_SIGNING_KEY_FILE": signingKeyPath,
			// A terminal, as far as the prompt is concerned. Go tests have no
			// pseudo-terminal; the real one is exercised by the PTY test below.
			"INTENTER_TEST_TTY": "1",
			// Set by the CI that runs this suite, and it would silence
			// everything these tests are about.
			"CI": "",
		},
	}
	for _, dir := range []string{env.Home, env.DataDir, env.ConfigDir, env.RuntimeDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	env.Workspace = env.NewWorkspace("demo")

	return &updateFixture{Env: env, Installed: installed, URL: server, Requests: requests}
}

// installedVersion asks the installed copy what it is.
func (f *updateFixture) installedVersion() string {
	f.t.Helper()
	out, _, code := f.CLI("version", "--json")
	if code != 0 {
		f.t.Fatalf("version exit %d:\n%s", code, out)
	}
	var info struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		f.t.Fatalf("parse version: %v\n%s", err, out)
	}
	return info.Version
}

// state reads the update state file.
func (f *updateFixture) state() map[string]any {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.DataDir, "update", "state.json"))
	if err != nil {
		return map[string]any{}
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		f.t.Fatalf("parse state: %v", err)
	}
	return state
}

// startupWith runs the hidden start-up path with a given answer on stdin.
func (f *updateFixture) startupWith(answer string) (string, string, int) {
	f.t.Helper()
	cmd := exec.Command(f.Installed, "update", "--startup")
	cmd.Env = f.environ()
	cmd.Dir = f.Workspace
	cmd.Stdin = strings.NewReader(answer)

	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()

	code := 0
	var exitErr *exec.ExitError
	if asExitError(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		f.t.Fatalf("run: %v", err)
	}
	return out.String(), errOut.String(), code
}

func TestUpdateStartupNothingToShow(t *testing.T) {
	// The cost every terminal pays for the feature, on every machine, forever.
	fixture := newUpdateFixture(t)

	out, errOut, code := fixture.startupWith("")
	if code != 0 {
		t.Errorf("exit code = %d, want 0 — a shell must start regardless", code)
	}
	if out != "" || errOut != "" {
		t.Errorf("nothing known yet, but it printed:\nout=%q\nerr=%q", out, errOut)
	}

	if testing.Short() {
		return
	}
	// Minimum of several runs: the budget is about the work done, and a shared
	// CI runner adds scheduling noise that is not ours.
	samples := make([]time.Duration, 0, 7)
	for i := 0; i < 7; i++ {
		start := time.Now()
		fixture.startupWith("")
		samples = append(samples, time.Since(start))
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	best := samples[0]

	t.Logf("start-up check: best %s, median %s (budget 50ms)", best, samples[len(samples)/2])
	if best > 50*time.Millisecond {
		t.Logf("WARNING: over the 50ms budget of SC-003")
	}
	// The hard failure is an order of magnitude out, which is a regression
	// rather than a busy machine.
	if best > 500*time.Millisecond {
		t.Errorf("the start-up check takes %s with nothing to show", best)
	}
}

func TestUpdateStartupPrompt(t *testing.T) {
	fixture := newUpdateFixture(t)
	fixture.MustCLI("update", "--check")

	out, _, code := fixture.startupWith("\n")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	for _, want := range []string{
		"Intenter " + publishedVersion + " is available",
		"Release notes:",
		"[y]es", "[N]ot now", "[s]kip this version",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the prompt is missing %q:\n%s", want, out)
		}
	}

	// "Not now" is recorded, and the next terminal is quiet.
	if fixture.state()["deferred_until"] == nil {
		t.Errorf("the answer was not recorded: %v", fixture.state())
	}
	second, _, _ := fixture.startupWith("\n")
	if second != "" {
		t.Errorf("a second terminal prompted within the reminder interval:\n%s", second)
	}
}

func TestUpdateStartupSkip(t *testing.T) {
	fixture := newUpdateFixture(t)
	fixture.MustCLI("update", "--check")

	if _, _, code := fixture.startupWith("s\n"); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if got := fixture.state()["skipped_version"]; got != publishedVersion {
		t.Errorf("skipped_version = %v, want %s", got, publishedVersion)
	}
	if out, _, _ := fixture.startupWith("\n"); out != "" {
		t.Errorf("a skipped version was offered again:\n%s", out)
	}
}

func TestUpdateApply(t *testing.T) {
	// The whole point: answering "y" leaves the machine running the new build,
	// with its data and its integration intact.
	fixture := newUpdateFixture(t)
	fixture.StartDaemon()
	fixture.ForgetDaemon() // the update restarts it; the harness must not fight over it
	fixture.AdoptRunningDaemon()

	// Something to lose: an evaluated command in the history.
	fixture.PreToolUse("update-session", "toolu_before", "git status")
	before := historyLength(t, fixture.Env)
	if before == 0 {
		t.Fatal("the fixture needs some history to prove it survives")
	}

	fixture.MustCLI("update", "--check")
	if got := fixture.installedVersion(); got == publishedVersion {
		t.Fatalf("the installed copy already reports %s", got)
	}

	started := time.Now()
	out, errOut, code := fixture.startupWith("y\n")
	elapsed := time.Since(started)

	if code != 0 {
		t.Fatalf("exit code = %d — a shell must start regardless\nout:\n%s\nerr:\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "Updated") {
		t.Fatalf("the update did not report success:\nout:\n%s\nerr:\n%s", out, errOut)
	}
	// SC-004.
	if elapsed > 60*time.Second {
		t.Errorf("the update took %s, over the 60s budget", elapsed)
	}

	if got := fixture.installedVersion(); got != publishedVersion {
		t.Errorf("installed version = %q, want %q", got, publishedVersion)
	}
	if got := daemonVersion(t, fixture.Env); got != publishedVersion {
		t.Errorf("the daemon reports %q, want %q — it is still the old build", got, publishedVersion)
	}
	if after := historyLength(t, fixture.Env); after < before {
		t.Errorf("history went from %d to %d entries across the update", before, after)
	}

	state := fixture.state()
	last, _ := state["last_update"].(map[string]any)
	if last == nil || last["result"] != "ok" || last["to"] != publishedVersion {
		t.Errorf("last_update = %v", state["last_update"])
	}
	if state["update_in_progress"] != nil {
		t.Errorf("the in-progress marker was left behind: %v", state["update_in_progress"])
	}
}

func TestUpdateStartupSaysNothingWithoutATerminal(t *testing.T) {
	// FR-011: scripts, cron, IDE task runners and `sh -c` all look like this.
	fixture := newUpdateFixture(t)
	fixture.MustCLI("update", "--check")
	delete(fixture.ExtraEnv, "INTENTER_TEST_TTY")

	out, errOut, code := fixture.startupWith("y\n")
	if code != 0 || out != "" || errOut != "" {
		t.Errorf("without a terminal it must be silent: exit %d\nout=%q\nerr=%q", code, out, errOut)
	}
	if fixture.state()["last_prompt_at"] != nil {
		t.Error("no prompt was shown, so none may be recorded")
	}
}

func TestUpdateStartupSaysNothingInCI(t *testing.T) {
	fixture := newUpdateFixture(t)
	fixture.MustCLI("update", "--check")
	fixture.ExtraEnv["CI"] = "true"

	if out, errOut, code := fixture.startupWith("y\n"); code != 0 || out != "" || errOut != "" {
		t.Errorf("CI must be silent: exit %d\nout=%q\nerr=%q", code, out, errOut)
	}
}

func TestUpdateStartupInARealTerminal(t *testing.T) {
	// The tests above tell the binary it has a terminal. This one gives it a
	// real pseudo-terminal, so the guard itself is proven rather than assumed.
	if runtime.GOOS == "windows" {
		t.Skip("script(1) is a POSIX tool")
	}
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script(1) is not installed")
	}

	fixture := newUpdateFixture(t)
	fixture.MustCLI("update", "--check")
	delete(fixture.ExtraEnv, "INTENTER_TEST_TTY")

	out, err := runUnderPTY(t, fixture.Env, fixture.Installed+" update --startup")
	if err != nil {
		t.Fatalf("script: %v\n%s", err, out)
	}
	if !strings.Contains(out, "is available") {
		t.Errorf("a real terminal must get the prompt:\n%s", out)
	}
}

func TestUpdateTrustChecksum(t *testing.T) {
	// SC-005 / contracts/release-and-signing.md §5: a checksums file altered
	// after it was signed is never trusted, and the copy on disk is
	// untouched. The signature is verified before the checksum even matters,
	// so a tampered checksums.txt is caught as a stale signature.
	fixture := newUpdateFixture(t)
	fixture.MustCLI("update", "--check")
	corruptChecksums(t, fixture.URL)

	before := fixture.installedVersion()
	_, errOut, code := fixture.CLI("update", "--yes")

	if code != 8 {
		t.Errorf("exit code = %d, want 8 (signature verification failed)\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "signature") {
		t.Errorf("the error must say what failed:\n%s", errOut)
	}
	if after := fixture.installedVersion(); after != before {
		t.Errorf("the binary changed from %q to %q despite a failed signature check", before, after)
	}
}

func TestUpdateTrustArchiveTamperedAfterSigning(t *testing.T) {
	// The signature vouches for checksums.txt, not for the archive by
	// itself: a mirror that hands out the wrong bytes for an otherwise
	// correctly signed release is still caught, by the checksum it no
	// longer matches.
	fixture := newUpdateFixture(t)
	fixture.MustCLI("update", "--check")
	corruptArchive(t, fixture.URL)

	before := fixture.installedVersion()
	_, errOut, code := fixture.CLI("update", "--yes")

	if code != 3 {
		t.Errorf("exit code = %d, want 3 (checksum mismatch)\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "checksum") {
		t.Errorf("the error must say what failed:\n%s", errOut)
	}
	if after := fixture.installedVersion(); after != before {
		t.Errorf("the binary changed from %q to %q despite a failed checksum", before, after)
	}
}

func TestUpdatePlanChangesNothing(t *testing.T) {
	fixture := newUpdateFixture(t)
	fixture.MustCLI("update", "--check")

	before := fixture.installedVersion()
	out := fixture.MustCLI("update", "--plan")

	for _, want := range []string{"Update plan", publishedVersion, "Nothing was changed"} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan is missing %q:\n%s", want, out)
		}
	}
	if after := fixture.installedVersion(); after != before {
		t.Errorf("--plan changed the installed version from %q to %q", before, after)
	}
}

func TestUpdateCheckNeverRunsOnTheHookPath(t *testing.T) {
	// FR-003. The hook is the safety gate Claude waits on before every command;
	// a network call there would be felt on every keystroke of a session, and a
	// slow one would look like Intenter hanging.
	fixture := newUpdateFixture(t)
	fixture.StartDaemon()

	for i := 0; i < 5; i++ {
		fixture.PreToolUse("hook-session", "toolu_"+itoa(int64(i)), "git status")
	}
	fixture.PreToolUse("hook-session", "toolu_blocked", "rm -rf ~/Documents")

	if requests := fixture.Requests(); len(requests) != 0 {
		t.Errorf("the hook path reached the release host %d time(s): %v", len(requests), requests)
	}
}

func TestNothingIsCheckedWithCheckingSwitchedOff(t *testing.T) {
	// SC-008: zero network requests from the update feature, across every path
	// that would otherwise make one unprompted.
	fixture := newUpdateFixture(t)
	fixture.WriteConfig("[updates]\ncheck = false\n")

	fixture.startupWith("y\n")
	fixture.CLI("update", "--background-check")
	fixture.StartDaemon()
	fixture.PreToolUse("off-session", "toolu_off", "git status")

	if requests := fixture.Requests(); len(requests) != 0 {
		t.Errorf("%d request(s) were made with checking switched off: %v", len(requests), requests)
	}

	// And the same through the environment variable, which is how a single
	// machine opts out without editing a file.
	fixture.ExtraEnv["INTENTER_NO_UPDATE_CHECK"] = "1"
	fixture.WriteConfig("")
	fixture.startupWith("y\n")
	fixture.CLI("update", "--background-check")
	if requests := fixture.Requests(); len(requests) != 0 {
		t.Errorf("%d request(s) survived INTENTER_NO_UPDATE_CHECK=1: %v", len(requests), requests)
	}

	// An explicit request is different from an unprompted one, and still works.
	out, _, code := fixture.CLI("update", "--check")
	if code != 0 || !strings.Contains(out, publishedVersion) {
		t.Errorf("an explicit check must still work: exit %d\n%s", code, out)
	}
}

func TestUpdateCheckIsAnonymous(t *testing.T) {
	// FR-002 / the reason a security tool is allowed to make a network request
	// at all.
	fixture := newUpdateFixture(t)
	fixture.MustCLI("update", "--check")

	requests := fixture.Requests()
	if len(requests) == 0 {
		t.Fatal("no request reached the release host")
	}
	for _, request := range requests {
		if strings.Contains(request, "?") {
			t.Errorf("a check carried a query string: %s", request)
		}
	}
}

func TestUpdateSecondApplyRejected(t *testing.T) {
	// Two terminals at once. The first is sitting at the prompt — which holds
	// the lock, because answering "y" there goes straight into an update — and
	// the second must be turned away rather than race it for the same file.
	fixture := newUpdateFixture(t)
	fixture.WriteConfig("[updates]\nprompt_timeout = \"30s\"\n")
	fixture.MustCLI("update", "--check")

	stop := fixture.promptingTerminal()
	defer stop()

	_, errOut, code := fixture.CLI("update", "--yes")
	if code != 7 {
		t.Errorf("exit code = %d, want 7 (an update is already running)\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "already running") {
		t.Errorf("the message must say what is happening:\n%s", errOut)
	}
}

// promptingTerminal starts a start-up check that stops at the prompt and stays
// there, the way a terminal nobody has answered yet does.
func (f *updateFixture) promptingTerminal() func() {
	f.t.Helper()

	cmd := exec.Command(f.Installed, "update", "--startup")
	cmd.Env = f.environ()
	cmd.Dir = f.Workspace

	// An open pipe nothing is ever written to: the prompt renders and then
	// waits for a line that never comes.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		f.t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		f.t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		f.t.Fatalf("start: %v", err)
	}

	// Wait until the prompt is actually on screen, so the lock is definitely
	// held before the second terminal tries.
	seen := make(chan struct{})
	go func() {
		buffer := make([]byte, 512)
		var text strings.Builder
		for {
			n, err := stdout.Read(buffer)
			if n > 0 {
				text.Write(buffer[:n])
				if strings.Contains(text.String(), "Update now?") {
					close(seen)
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	select {
	case <-seen:
	case <-time.After(15 * time.Second):
		cmd.Process.Kill()
		f.t.Fatal("the first terminal never reached the prompt")
	}

	return func() {
		stdin.Close()
		cmd.Process.Kill()
		cmd.Wait()
	}
}

// historyLength counts the recorded decisions.
func historyLength(t *testing.T, env *Env) int {
	t.Helper()
	out, _, code := env.CLI("history", "--json")
	if code != 0 {
		return 0
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("parse history: %v\n%s", err, out)
	}
	return len(entries)
}

// daemonVersion asks the running daemon what build it is.
func daemonVersion(t *testing.T, env *Env) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		out, _, code := env.CLI("daemon", "status", "--json")
		if code == 0 {
			var status struct {
				Version string `json:"version"`
			}
			if err := json.Unmarshal([]byte(out), &status); err == nil && status.Version != "" {
				last = status.Version
				return last
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return last
}

// runUnderPTY runs a shell command line attached to a real pseudo-terminal.
//
// The TTY guards are the whole reason the prompt is safe to have, so at least
// one test has to face a real terminal rather than the INTENTER_TEST_TTY
// override. script(1) is the way to get one without a new dependency; its BSD
// and util-linux dialects take their arguments differently, so both are tried.
func runUnderPTY(t *testing.T, env *Env, command string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script(1) is not installed")
	}

	variants := [][]string{
		{"script", "-q", "/dev/null", "sh", "-c", command}, // BSD/macOS
		{"script", "-qec", command, "/dev/null"},           // util-linux
	}
	// A pseudo-terminal without a terminal type is not one every shell will
	// use. fish refuses to set up the terminal and skips the start-up work the
	// prompt depends on, so the test fails for the environment rather than for
	// the behaviour. An interactive session always has TERM; CI runners do not,
	// which is why this only ever failed there.
	environ := env.environ()
	if os.Getenv("TERM") == "" {
		environ = append(environ, "TERM=xterm-256color")
	}

	var lastErr error
	var lastOut []byte
	for _, argv := range variants {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Env = environ
		cmd.Dir = env.Workspace
		out, err := cmd.CombinedOutput()
		if err == nil {
			return string(out), nil
		}
		lastErr, lastOut = err, out
	}
	return string(lastOut), lastErr
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func TestSetupInstallsStartupHook(t *testing.T) {
	// The block is what makes the prompt appear for a real user; without it
	// everything else in this feature is unreachable.
	if runtime.GOOS == "windows" {
		t.Skip("the PowerShell profile is covered by the unit tests")
	}
	fixture := newUpdateFixture(t)
	fixture.ExtraEnv["SHELL"] = "/bin/zsh"

	out := fixture.MustCLI("update", "startup", "enable")
	if !strings.Contains(out, ".zshrc") {
		t.Errorf("enable must say where it wrote:\n%s", out)
	}

	rc := filepath.Join(fixture.Home, ".zshrc")
	content, err := os.ReadFile(rc)
	if err != nil {
		t.Fatalf("read %s: %v", rc, err)
	}
	if !strings.Contains(string(content), "update --startup") {
		t.Errorf("%s does not call the check:\n%s", rc, content)
	}

	status := fixture.MustCLI("update", "startup", "status")
	if !strings.Contains(status, rc) {
		t.Errorf("status does not report the file:\n%s", status)
	}
}

func TestUninstallRemovesStartupHook(t *testing.T) {
	// SC-007: byte-identical outside the block.
	if runtime.GOOS == "windows" {
		t.Skip("the PowerShell profile is covered by the unit tests")
	}
	fixture := newUpdateFixture(t)
	fixture.ExtraEnv["SHELL"] = "/bin/zsh"

	rc := filepath.Join(fixture.Home, ".zshrc")
	original := "# mine\nexport PS1='> '\nalias ll='ls -la'\n"
	if err := os.WriteFile(rc, []byte(original), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	fixture.MustCLI("update", "startup", "enable")
	fixture.MustCLI("update", "startup", "disable")

	got, err := os.ReadFile(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != original {
		t.Errorf("the file did not come back:\nwant %q\ngot  %q", original, got)
	}
}

func TestUpdatePromptAppearsInRealShells(t *testing.T) {
	// The end of the chain: a real shell, a real terminal, the block the tool
	// installed, and the prompt a user would see.
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shells")
	}

	for _, shell := range []string{"zsh", "bash", "fish"} {
		t.Run(shell, func(t *testing.T) {
			path, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("%s is not installed", shell)
			}

			fixture := newUpdateFixture(t)
			fixture.ExtraEnv["SHELL"] = path
			delete(fixture.ExtraEnv, "INTENTER_TEST_TTY")
			fixture.MustCLI("update", "startup", "enable", "--shell", shell)
			fixture.MustCLI("update", "--check")

			out, err := runUnderPTY(t, fixture.Env, shellQuote(path)+" -i -c true")
			if err != nil {
				t.Fatalf("%s: %v\n%s", shell, err, out)
			}
			if !strings.Contains(out, "is available") {
				t.Errorf("no prompt in an interactive %s:\n%s", shell, out)
			}
		})
	}
}

func TestUpdateSilentNonInteractive(t *testing.T) {
	// FR-011. Every one of these is a place a stray line of output would be
	// read as data, or a prompt would look like a hang.
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shells")
	}
	fixture := newUpdateFixture(t)
	fixture.ExtraEnv["SHELL"] = "/bin/sh"
	delete(fixture.ExtraEnv, "INTENTER_TEST_TTY")
	fixture.MustCLI("update", "startup", "enable", "--shell", "zsh,bash,fish")
	fixture.MustCLI("update", "--check")

	for _, shell := range []string{"sh", "bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			path, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("%s is not installed", shell)
			}
			cmd := exec.Command(path, "-c", "true")
			cmd.Env = fixture.environ()
			cmd.Dir = fixture.Workspace
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s -c: %v\n%s", shell, err, out)
			}
			if len(bytes.TrimSpace(out)) != 0 {
				t.Errorf("a non-interactive %s printed:\n%s", shell, out)
			}
		})
	}
}

func TestUpdateConcurrentTerminals(t *testing.T) {
	// FR-008: opening a handful of terminals at once is one prompt, not five.
	fixture := newUpdateFixture(t)
	fixture.MustCLI("update", "--check")

	const terminals = 5
	outputs := make([]string, terminals)
	var wait sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < terminals; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			out, _, _ := fixture.startupWith("\n")
			outputs[index] = out
		}(i)
	}
	close(start)
	wait.Wait()

	prompted := 0
	for _, out := range outputs {
		if strings.Contains(out, "is available") {
			prompted++
		}
	}
	if prompted != 1 {
		t.Errorf("%d of %d terminals prompted, want exactly 1:\n%q", prompted, terminals, outputs)
	}
}

func TestUpdateNotNowIsForgottenAfterTheInterval(t *testing.T) {
	// "Not now" is quiet for a day, and then asks again — otherwise a single
	// Enter would silence the feature forever.
	fixture := newUpdateFixture(t)
	fixture.MustCLI("update", "--check")

	if out, _, _ := fixture.startupWith("\n"); !strings.Contains(out, "is available") {
		t.Fatalf("the first terminal did not prompt:\n%s", out)
	}
	if out, _, _ := fixture.startupWith("\n"); out != "" {
		t.Fatalf("a second terminal prompted straight away:\n%s", out)
	}

	// Two days later.
	fixture.ExtraEnv["INTENTER_TEST_NOW"] = time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	out, _, _ := fixture.startupWith("\n")
	if !strings.Contains(out, "is available") {
		t.Errorf("the prompt never came back after the interval:\n%s", out)
	}
}

func TestUpdateOfflineStartupIsStillFast(t *testing.T) {
	// A machine behind a captive portal must not pay for it on every terminal.
	fixture := newUpdateFixture(t)
	fixture.ExtraEnv["INTENTER_LATEST_URL"] = "https://releases.invalid/releases/latest"

	start := time.Now()
	out, errOut, code := fixture.startupWith("")
	elapsed := time.Since(start)

	if code != 0 || out != "" || errOut != "" {
		t.Errorf("an unreachable release host must be silent: exit %d\nout=%q\nerr=%q", code, out, errOut)
	}
	if elapsed > 2*time.Second {
		t.Errorf("start-up waited %s for a network it cannot reach", elapsed)
	}
}

// installCopy copies the built binary somewhere the updater may replace it.
func installCopy(t *testing.T, source, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	name := "intenter"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	target := filepath.Join(dir, name)

	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read %s: %v", source, err)
	}
	if err := os.WriteFile(target, data, 0o755); err != nil {
		t.Fatalf("write %s: %v", target, err)
	}
	return target
}

var (
	versionedOnce  sync.Once
	versionedPath  string
	versionedError error
	// releaseTmpDir holds the second build; TestMain removes it.
	releaseTmpDir string
)

// buildVersioned compiles this repository as a different release, which is what
// makes "the daemon is running the new build" a real assertion rather than a
// tautology.
func buildVersioned(t *testing.T, release string) string {
	t.Helper()
	versionedOnce.Do(func() {
		dir, err := os.MkdirTemp("", "intenter-release")
		if err != nil {
			versionedError = err
			return
		}
		releaseTmpDir = dir
		name := "intenter"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		versionedPath = filepath.Join(dir, name)

		cmd := exec.Command("go", "build",
			"-ldflags", "-X github.com/Vadym903/Intenter/internal/version.Version="+release,
			"-o", versionedPath, "./cmd/intenter")
		cmd.Dir = repoRoot()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			versionedError = fmt.Errorf("build %s: %v\n%s", release, err, stderr.String())
		}
	})
	if versionedError != nil {
		t.Fatalf("%v", versionedError)
	}
	return versionedPath
}

// releaseDir is where the served assets live, so a test can rewrite them.
var releaseDirs sync.Map

// publishRelease builds the release archive and serves it the way GitHub
// does, signing checksums.txt the way the release pipeline does (research
// R-05) and returning the public key's PEM path for the harness to point
// INTENTER_SIGNING_KEY_FILE at.
func publishRelease(t *testing.T, release string) (string, func() []string, string) {
	t.Helper()
	dir := t.TempDir()
	asset := assetName(release)
	archive := filepath.Join(dir, asset)

	binaryPath := buildVersioned(t, release)
	if strings.HasSuffix(asset, ".zip") {
		writeReleaseZip(t, archive, binaryPath)
	} else {
		writeReleaseTarGz(t, archive, binaryPath)
	}
	writeChecksums(t, dir, map[string]string{asset: sha256File(t, archive)})

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	signChecksums(t, dir, privateKey)
	keyPath := writeSigningPublicKey(t, dir, &privateKey.PublicKey)

	var mu sync.Mutex
	var requests []string

	tag := "v" + release
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.String())
		mu.Unlock()

		switch {
		case r.URL.Path == "/releases/latest":
			http.Redirect(w, r, "/releases/tag/"+tag, http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/releases/download/"+tag+"/"):
			file := filepath.Join(dir, filepath.Base(r.URL.Path))
			if _, err := os.Stat(file); err != nil {
				http.NotFound(w, r)
				return
			}
			http.ServeFile(w, r, file)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	releaseDirs.Store(server.URL, dir)

	return server.URL, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), requests...)
	}, keyPath
}

// signChecksums signs the checksums.txt already written to dir, the way the
// release pipeline signs whatever `goreleaser` just published.
func signChecksums(t *testing.T, dir string, priv *ecdsa.PrivateKey) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		t.Fatalf("read checksums.txt: %v", err)
	}
	digest := sha256.Sum256(data)
	signature, err := ecdsa.SignASN1(rand.Reader, priv, digest[:])
	if err != nil {
		t.Fatalf("sign checksums.txt: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(signature)
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt.sig"), []byte(encoded), 0o644); err != nil {
		t.Fatalf("write checksums.txt.sig: %v", err)
	}
}

// writeSigningPublicKey writes pub as the PEM INTENTER_SIGNING_KEY_FILE
// points at.
func writeSigningPublicKey(t *testing.T, dir string, pub *ecdsa.PublicKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	path := filepath.Join(dir, "test-signing-key.pub")
	block := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	if err := os.WriteFile(path, block, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// corruptChecksums rewrites the published checksums so nothing matches,
// without re-signing — a checksums file altered after publication, whose
// signature is now stale.
func corruptChecksums(t *testing.T, serverURL string) {
	t.Helper()
	value, ok := releaseDirs.Load(serverURL)
	if !ok {
		t.Fatalf("no release directory for %s", serverURL)
	}
	writeChecksums(t, value.(string), map[string]string{
		assetName(publishedVersion): strings.Repeat("0", 64),
	})
}

// corruptArchive rewrites the published archive after checksums.txt (and its
// signature) were already produced for the original bytes — what a mirror
// serving the wrong file looks like.
func corruptArchive(t *testing.T, serverURL string) {
	t.Helper()
	value, ok := releaseDirs.Load(serverURL)
	if !ok {
		t.Fatalf("no release directory for %s", serverURL)
	}
	path := filepath.Join(value.(string), assetName(publishedVersion))
	if err := os.WriteFile(path, []byte("not the archive that was signed for"), 0o644); err != nil {
		t.Fatalf("corrupt archive: %v", err)
	}
}

func assetName(release string) string {
	extension := ".tar.gz"
	if runtime.GOOS == "windows" {
		extension = ".zip"
	}
	return fmt.Sprintf("intenter_%s_%s_%s%s", release, runtime.GOOS, runtime.GOARCH, extension)
}

func writeReleaseTarGz(t *testing.T, archivePath, binaryPath string) {
	t.Helper()
	content, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer file.Close()

	gz := gzip.NewWriter(file)
	archive := tar.NewWriter(gz)
	header := &tar.Header{Name: "intenter", Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}
	if err := archive.WriteHeader(header); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := archive.Write(content); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
}

func writeReleaseZip(t *testing.T, archivePath, binaryPath string) {
	t.Helper()
	content, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer file.Close()

	archive := zip.NewWriter(file)
	writer, err := archive.Create("intenter.exe")
	if err != nil {
		t.Fatalf("zip entry: %v", err)
	}
	if _, err := writer.Write(content); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
}

func writeChecksums(t *testing.T, dir string, sums map[string]string) {
	t.Helper()
	var builder strings.Builder
	for name, sum := range sums {
		fmt.Fprintf(&builder, "%s  %s\n", sum, name)
	}
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(builder.String()), 0o644); err != nil {
		t.Fatalf("write checksums: %v", err)
	}
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer file.Close()

	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		t.Fatalf("hash: %v", err)
	}
	return hex.EncodeToString(digest.Sum(nil))
}
