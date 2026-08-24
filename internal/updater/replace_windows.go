//go:build windows

package updater

import "os"

// swap installs the staged binary over a target that may be running.
//
// Windows refuses to delete or overwrite a file that is mapped as a running
// image, but it does allow *renaming* one. So the running executable is moved
// aside first and the new file takes its name; the leftover is deleted at the
// next start (CleanStaleReplacements), because it cannot be deleted while the
// process using it is alive.
//
// If the second rename fails the first is undone, so a failure leaves the
// installation exactly as it was rather than with no executable at all.
func swap(staged, target string) error {
	aside := target + ".old"
	os.Remove(aside)

	movedAside := false
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, aside); err != nil {
			return errRename(target, err)
		}
		movedAside = true
	}

	if err := os.Rename(staged, target); err != nil {
		if movedAside {
			// Put the old binary back under its own name. Leaving the machine
			// with no `intenter.exe` would break the Claude hooks and the
			// service, which is far worse than a failed update.
			os.Rename(aside, target)
		}
		return errRename(target, err)
	}

	// Best effort: it usually fails while the old image is still running, and
	// the next start cleans it up.
	os.Remove(aside)
	return nil
}
