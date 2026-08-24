package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/platform"
	"github.com/Vadym903/Intenter/internal/updater"
	"github.com/Vadym903/Intenter/internal/version"
)

// releaseHost starts a fake release host publishing one tag, and points the
// update commands at it.
func releaseHost(t *testing.T, tag string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases/latest" {
			http.Redirect(w, r, "/releases/tag/"+tag, http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	t.Setenv(updater.EnvLatestURL, server.URL+"/releases/latest")
	t.Setenv(updater.EnvDownloadBase, server.URL+"/releases/download")
	return server.URL
}

// updateStore is the state store for the isolated data directory.
func updateStore(t *testing.T) *updater.Store {
	t.Helper()
	p, err := platform.New()
	if err != nil {
		t.Fatalf("platform: %v", err)
	}
	return updater.NewStore(p.DataDir())
}

func TestCheckReportsWhatIsAvailable(t *testing.T) {
	isolate(t)
	releaseHost(t, "v9.9.9")

	out, _, code := runCLI(t, "update", "--check")
	if code != ExitOK {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	for _, want := range []string{"installed", "latest", "9.9.9", "channel", "intenter update"} {
		if !strings.Contains(out, want) {
			t.Errorf("status is missing %q:\n%s", want, out)
		}
	}
}

func TestCheckJSONShape(t *testing.T) {
	isolate(t)
	releaseHost(t, "v9.9.9")

	out, _, code := runCLI(t, "update", "--check", "--json")
	if code != ExitOK {
		t.Fatalf("exit code = %d\n%s", code, out)
	}

	var status UpdateStatus
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	if status.Installed != version.Version {
		t.Errorf("installed = %q, want %q", status.Installed, version.Version)
	}
	if status.Latest != "9.9.9" || !status.UpdateDue {
		t.Errorf("latest = %q, update_available = %v", status.Latest, status.UpdateDue)
	}
	if !status.PromptDue {
		t.Error("prompt_due must be true when a newer release is known and nothing suppresses it")
	}
	if !status.State.LastCheckOK {
		t.Error("the state must record the check that just succeeded")
	}
	if len(status.History) == 0 {
		t.Error("the last checks must be included so support can see them")
	}
}

func TestAFailedCheckStillPrintsTheStatus(t *testing.T) {
	// The reason a check failed is exactly what a user runs `--check` to find
	// out, so the status has to survive the failure.
	isolate(t)
	t.Setenv(updater.EnvLatestURL, "http://127.0.0.1:1/releases/latest")

	out, _, code := runCLI(t, "update", "--check")
	if code != updater.ExitDownload {
		t.Errorf("exit code = %d, want %d", code, updater.ExitDownload)
	}
	if !strings.Contains(out, "installed") {
		t.Errorf("the status must be printed even when the check failed:\n%s", out)
	}
}

func TestSkipAndUnskip(t *testing.T) {
	isolate(t)
	releaseHost(t, "v9.9.9")

	if _, _, code := runCLI(t, "update", "--skip", "9.9.9"); code != ExitOK {
		t.Fatalf("--skip exit code = %d", code)
	}
	if got := updateStore(t).LoadOrZero().SkippedVersion; got != "9.9.9" {
		t.Errorf("skipped_version = %q, want 9.9.9", got)
	}

	out, _, code := runCLI(t, "update", "--check", "--json")
	if code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	var status UpdateStatus
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if status.PromptDue {
		t.Error("a skipped version must not be prompted for")
	}
	if !status.UpdateDue {
		t.Error("a skipped version is still an available update; --check should say so")
	}

	if _, _, code := runCLI(t, "update", "--unskip"); code != ExitOK {
		t.Fatalf("--unskip exit code = %d", code)
	}
	if got := updateStore(t).LoadOrZero().SkippedVersion; got != "" {
		t.Errorf("skipped_version = %q, want it cleared", got)
	}
}

func TestSkipRejectsSomethingThatIsNotAVersion(t *testing.T) {
	isolate(t)
	if _, errOut, code := runCLI(t, "update", "--skip", "latest"); code == ExitOK {
		t.Errorf("exit code = %d, want a failure\n%s", code, errOut)
	}
}

func TestPlanChangesNothing(t *testing.T) {
	isolate(t)
	releaseHost(t, "v9.9.9")
	if _, _, code := runCLI(t, "update", "--check"); code != ExitOK {
		t.Fatal("the check must succeed first")
	}

	out, _, code := runCLI(t, "update", "--plan")
	if code != ExitOK {
		t.Fatalf("exit code = %d\n%s", code, out)
	}
	for _, want := range []string{"Update plan", "installed", "target", "9.9.9", "actions", "Nothing was changed"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan is missing %q:\n%s", want, out)
		}
	}
	if updateStore(t).LoadOrZero().LastUpdate != nil {
		t.Error("--plan must not record an update")
	}
}

func TestUpdateWithoutAnyReleaseInformationSaysSo(t *testing.T) {
	isolate(t)
	out, errOut, code := runCLI(t, "update", "--plan")
	if code == ExitOK {
		t.Fatalf("exit code = %d, want a failure\n%s", code, out)
	}
	if !strings.Contains(errOut, "--check") {
		t.Errorf("the error must say what to run:\n%s", errOut)
	}
}

func TestUpdateRefusesToRunUnattendedWithoutATerminal(t *testing.T) {
	// Go tests have no terminal, which is exactly the situation a script is in.
	isolate(t)
	releaseHost(t, "v9.9.9")
	if _, _, code := runCLI(t, "update", "--check"); code != ExitOK {
		t.Fatal("the check must succeed first")
	}

	_, errOut, code := runCLI(t, "update")
	if code != updater.ExitUsage {
		t.Errorf("exit code = %d, want %d", code, updater.ExitUsage)
	}
	if !strings.Contains(errOut, "--yes") {
		t.Errorf("the error must name the flag that would work:\n%s", errOut)
	}
}

func TestAnUnknownChannelIsRejected(t *testing.T) {
	isolate(t)
	if _, _, code := runCLI(t, "update", "--check", "--channel", "nightly"); code == ExitOK {
		t.Error("an unknown channel must be refused rather than silently ignored")
	}
}

func TestTheStartupCheckIsSilentAndFast(t *testing.T) {
	// SC-003: the whole feature is only acceptable if opening a terminal costs
	// nothing when there is nothing to say.
	isolate(t)

	start := time.Now()
	out, errOut, code := runCLI(t, "update", "--startup")
	elapsed := time.Since(start)

	if code != ExitOK {
		t.Errorf("exit code = %d, want 0", code)
	}
	if out != "" || errOut != "" {
		t.Errorf("the start-up check printed something with nothing to show:\nout=%q\nerr=%q", out, errOut)
	}
	// Generous next to the 50 ms budget, because this measures the command
	// rather than the process: a regression here is an order of magnitude, not
	// a few milliseconds.
	if elapsed > 250*time.Millisecond {
		t.Errorf("the start-up check took %s with nothing to show", elapsed)
	}
}

func TestTheStartupCheckMakesNoNetworkRequestWhenCheckingIsOff(t *testing.T) {
	// SC-008.
	base := isolate(t)
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.NotFound(w, r)
	}))
	defer server.Close()
	t.Setenv(updater.EnvLatestURL, server.URL+"/releases/latest")
	writeConfigFile(t, base, "[updates]\ncheck = false\n")

	if _, _, code := runCLI(t, "update", "--startup"); code != ExitOK {
		t.Errorf("exit code = %d", code)
	}
	if _, _, code := runCLI(t, "update", "--background-check"); code != ExitOK {
		t.Errorf("exit code = %d", code)
	}
	if requests != 0 {
		t.Errorf("%d requests were made with checking switched off", requests)
	}
}

func TestTheExplicitCheckWorksEvenWhenCheckingIsOff(t *testing.T) {
	// The switch governs what happens without being asked. Asking is different.
	base := isolate(t)
	releaseHost(t, "v9.9.9")
	writeConfigFile(t, base, "[updates]\ncheck = false\n")

	out, _, code := runCLI(t, "update", "--check")
	if code != ExitOK {
		t.Fatalf("exit code = %d\n%s", code, out)
	}
	if !strings.Contains(out, "9.9.9") {
		t.Errorf("an explicit check must still report what it found:\n%s", out)
	}
}

func TestTheStartupCheckIsSilentInCI(t *testing.T) {
	isolate(t)
	releaseHost(t, "v9.9.9")
	if _, _, code := runCLI(t, "update", "--check"); code != ExitOK {
		t.Fatal("the check must succeed first")
	}

	t.Setenv("CI", "true")
	t.Setenv(updater.EnvTestTTY, "1")

	out, errOut, code := runCLI(t, "update", "--startup")
	if code != ExitOK || out != "" || errOut != "" {
		t.Errorf("CI must silence the prompt: exit %d\nout=%q\nerr=%q", code, out, errOut)
	}
}

func TestTheBackgroundCheckWritesTheState(t *testing.T) {
	isolate(t)
	releaseHost(t, "v9.9.9")

	if _, _, code := runCLI(t, "update", "--background-check"); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	state := updateStore(t).LoadOrZero()
	if state.LatestKnown == nil || state.LatestKnown.Version != "9.9.9" {
		t.Errorf("latest_known = %+v, want 9.9.9", state.LatestKnown)
	}
}

// writeConfigFile writes a config.toml into the isolated configuration
// directory.
func writeConfigFile(t *testing.T, base, content string) {
	t.Helper()
	dir := filepath.Join(base, "config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
