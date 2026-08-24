package daemon

import (
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
)

// These are end-to-end regressions for the security-review area 1 (Windows
// command surface, `specs/005-make-product-usable`): each was a way to get a
// dangerous cmd.exe/PowerShell command past the gate. They run the whole
// pipeline through the real daemon, the same path a hook takes. The cmd and
// PowerShell dialects parse text only, so they run on every host OS (§14.4);
// only the named-pipe transport and pathrules_windows.go are Windows-kernel
// dependent and could not be exercised from this machine (see the review
// report for what that leaves unverified).

// cmdRequest builds an ActionRequest parsed under the cmd.exe dialect.
func cmdRequest(w *workspace, command, toolUseID string) action.ActionRequest {
	req := bashRequest(w, command, toolUseID)
	req.Dialect = action.DialectCmd
	return req
}

// AG-120 (already fixed by a previous reviewer): a caret has no effect
// inside a double-quoted string, so it cannot be used to keep a quote open
// past its real close and hide a second command after `&`.
func TestCmdCaretInsideQuotesDoesNotHideASecondCommand(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	client := startDaemon(t, p)

	result := evaluate(t, client, cmdRequest(w, `echo "a^"&rd /s /q %USERPROFILE%`, ""))
	if result.Decision != action.OutcomeBlock {
		t.Fatalf("decision = %s (%s), want BLOCK — the caret must not hide the delete after &", result.Decision, result.Reason)
	}
	if result.HardRule != "R2" {
		t.Errorf("hard rule = %q, want R2", result.HardRule)
	}
}

// AG-121 (already fixed by a previous reviewer): New-Item -ItemType
// SymbolicLink/Junction/HardLink aliases a path rather than creating an
// ordinary file, so it must not auto-allow like a plain file create.
func TestPowerShellNewItemSymlinkIsNotAutoAllowed(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	client := startDaemon(t, p)

	req := bashRequest(w, `New-Item -Path ./dist/evil -ItemType SymbolicLink -Value ~/Documents`, "")
	req.Dialect = action.DialectPowerShell
	result := evaluate(t, client, req)
	if result.Decision == action.OutcomeAllow {
		t.Errorf("a link creation must not be auto-allowed (%s)", result.Class)
	}
}

// AG-122: cmd.exe treats `<`/`>` as metacharacters regardless of
// surrounding whitespace, so `echo hi>>file` redirects exactly like
// `echo hi >> file`. Before the fix the unspaced write was invisible to
// applyRedirections, and `echo` is a modeled no-op — so the write sailed
// through the read-only baseline with zero prompts (the AG-01 bypass class).
// Appending to a sensitive path must be blocked (R5), the same as the fully
// spaced form already is.
func TestCmdRedirectionWithoutWhitespaceIsNotAutoAllowed(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	client := startDaemon(t, p)

	for _, command := range []string{
		`echo evil >> %USERPROFILE%\.ssh\authorized_keys`,
		`echo evil>>%USERPROFILE%\.ssh\authorized_keys`,
	} {
		result := evaluate(t, client, cmdRequest(w, command, ""))
		if result.Decision != action.OutcomeBlock {
			t.Errorf("%q: decision = %s (%s), want BLOCK", command, result.Decision, result.Reason)
		}
		if result.HardRule != "R5" {
			t.Errorf("%q: hard rule = %q, want R5 (sensitive write)", command, result.HardRule)
		}
	}
}

// AG-122: the same tokenizer gap existed in the PowerShell dialect —
// `Write-Output evil>>file` redirects exactly like the fully spaced form.
// Appending to a sensitive path must be blocked (R5) regardless of spacing.
func TestPowerShellRedirectionWithoutWhitespaceIsNotAutoAllowed(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	client := startDaemon(t, p)

	for _, command := range []string{
		`Write-Output evil >> $env:USERPROFILE\.ssh\authorized_keys`,
		`Write-Output evil>>$env:USERPROFILE\.ssh\authorized_keys`,
	} {
		req := bashRequest(w, command, "")
		req.Dialect = action.DialectPowerShell
		result := evaluate(t, client, req)
		if result.Decision != action.OutcomeBlock {
			t.Errorf("%q: decision = %s (%s), want BLOCK", command, result.Decision, result.Reason)
		}
		if result.HardRule != "R5" {
			t.Errorf("%q: hard rule = %q, want R5 (sensitive write)", command, result.HardRule)
		}
	}
}

// AG-122: `2>&1` duplicates a stream handle; it must not be misread as
// cmd's `&` sequence operator, which used to drop the preceding command from
// the parsed output entirely. `2>&1` is a near-universal batch-script idiom
// for merging stderr into stdout, so a catastrophic delete suffixed with it
// must still be blocked, exactly like the plain form.
func TestCmdStreamDuplicationDoesNotDropACatastrophicDelete(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	client := startDaemon(t, p)

	result := evaluate(t, client, cmdRequest(w, `rd /s /q %USERPROFILE% 2>&1`, ""))
	if result.Decision != action.OutcomeBlock {
		t.Fatalf("decision = %s (%s), want BLOCK — 2>&1 must not hide the delete from the hard rules", result.Decision, result.Reason)
	}
	if result.HardRule != "R2" {
		t.Errorf("hard rule = %q, want R2", result.HardRule)
	}
}
