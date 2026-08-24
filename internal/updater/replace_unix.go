//go:build !windows

package updater

import "os"

// swap renames the staged binary over the target.
//
// On POSIX a rename over a running executable is allowed: the running process
// holds the inode, not the name, so it keeps working until it exits and the
// next start picks up the new file. That is exactly what the daemon restart
// after this is for.
func swap(staged, target string) error {
	if err := os.Rename(staged, target); err != nil {
		return errRename(target, err)
	}
	return nil
}
