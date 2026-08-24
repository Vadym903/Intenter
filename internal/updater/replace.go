package updater

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Replace installs a new binary at target, atomically.
//
// The swap is a rename rather than a write-through because a rename is atomic:
// at no instant does the path name a half-written file, and any process already
// running the old binary keeps the file it opened. Writing over the executable
// in place would corrupt the running daemon and leave a truncated binary if the
// machine lost power mid-copy.
//
// The staged copy is written into the *target's own directory*, because a
// rename cannot cross filesystems and the download lives under the data
// directory, which frequently is one.
func Replace(newBinary, target string) error {
	directory := filepath.Dir(target)
	staged := filepath.Join(directory, "."+filepath.Base(target)+".new")

	if err := copyExecutable(newBinary, staged); err != nil {
		os.Remove(staged)
		return err
	}
	if err := swap(staged, target); err != nil {
		os.Remove(staged)
		return err
	}
	return nil
}

// copyExecutable writes the staged file with the mode an executable needs.
func copyExecutable(source, staged string) error {
	in, err := os.Open(source)
	if err != nil {
		return failf(ExitDownload, "updater: read %s: %w", source, err)
	}
	defer in.Close()

	out, err := os.OpenFile(staged, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return failf(ExitNotWritable, "updater: cannot write to %s: %w", filepath.Dir(staged), err)
	}
	_, err = io.Copy(out, in)
	if syncErr := out.Sync(); err == nil {
		err = syncErr
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return failf(ExitNotWritable, "updater: write %s: %w", staged, err)
	}

	// The mode is set explicitly rather than left to the open: a restrictive
	// umask would otherwise produce a binary the user cannot run.
	if err := os.Chmod(staged, 0o755); err != nil {
		return failf(ExitNotWritable, "updater: make %s executable: %w", staged, err)
	}
	return nil
}

// CleanStaleReplacements removes the copy an earlier update left beside the
// executable. It is called at every start, and its failure is ignored: a file
// that cannot be deleted is a few megabytes, not a broken installation.
//
// Only the *displaced* binary is removed. The staged `.new` file is left alone
// on purpose: another process may be part-way through an update at this very
// moment, and deleting what it is about to rename into place would break it.
// A failed update removes its own staging file.
func CleanStaleReplacements(executable string) {
	if executable == "" {
		return
	}
	os.Remove(executable + ".old")
}

// errRename wraps a failed rename with the exit code that says the install
// location is the problem.
func errRename(target string, err error) error {
	return &Failure{
		Code: ExitNotWritable,
		Err:  fmt.Errorf("updater: cannot replace %s: %w", target, err),
	}
}
