//go:build !windows

package updater

// PolicyFix is the Windows-only remedy for a policy that blocks profile
// scripts. It is defined here so callers need no build tags of their own.
const PolicyFix = "Set-ExecutionPolicy -Scope CurrentUser RemoteSigned"

// ExecutionPolicyBlocked is always false away from Windows: no other shell has
// a policy that refuses to run start-up files.
func ExecutionPolicyBlocked() (bool, string) { return false, "" }
