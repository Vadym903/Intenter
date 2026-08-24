//go:build windows

package updater

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// policyProbeTimeout bounds asking PowerShell about its execution policy.
// Starting a PowerShell host is slow, and this runs during `setup claude`,
// where a hang would look like a broken installation.
const policyProbeTimeout = 10 * time.Second

// PolicyFix is what lifts a policy that stops profile scripts running.
const PolicyFix = "Set-ExecutionPolicy -Scope CurrentUser RemoteSigned"

// ExecutionPolicyBlocked reports whether PowerShell will refuse to run the
// profile that holds our block.
//
// Under `Restricted` — the default on Windows client editions for a long time —
// no profile script runs at all, so the block is written and simply never
// executes. Saying so is the difference between "Intenter is broken" and "one
// command turns this on".
func ExecutionPolicyBlocked() (bool, string) {
	for _, host := range []string{"pwsh", "powershell"} {
		policy, ok := readExecutionPolicy(host)
		if !ok {
			continue
		}
		switch strings.ToLower(policy) {
		case "restricted", "allsigned":
			return true, PolicyFix
		}
		// One host that will run scripts is enough for the prompt to appear.
		return false, ""
	}
	return false, ""
}

func readExecutionPolicy(host string) (string, bool) {
	if _, err := exec.LookPath(host); err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), policyProbeTimeout)
	defer cancel()

	output, err := exec.CommandContext(ctx, host, "-NoProfile", "-NonInteractive",
		"-Command", "Get-ExecutionPolicy").Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(output)), true
}
