package resolver

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// MaxConfigFileBytes bounds one workspace-supplied configuration file Intenter
// reads to decide behavior: package.json, .npmrc, the git metadata files, the
// agent's settings files. None of them is legitimately large.
const MaxConfigFileBytes = 4 << 20

// ErrNotRegularFile marks a path that exists but is not an ordinary file.
var ErrNotRegularFile = errors.New("resolver: not a regular file")

// ErrFileTooLarge marks a file over the read cap.
var ErrFileTooLarge = errors.New("resolver: file exceeds the size limit")

// readConfigFile reads a file the workspace controls, refusing anything that is
// not a regular file within the cap.
//
// Every one of these files is attacker-influenced — a repository the user pulled
// can contain whatever it likes under any name — and the read happens on the
// daemon's request path. A FIFO named package.json blocks os.ReadFile forever,
// which parks the request goroutine and its descriptor for good; a multi-gigabyte
// one exhausts memory. Either takes the gate down for the session, and a hook
// that gets no answer defers to the agent's native flow (§26). The file is opened
// without blocking, checked by descriptor rather than by path so a swap between
// stat and open cannot change the answer, and read through a limit.
func readConfigFile(path string) ([]byte, error) {
	file, err := openNoFollowBlocking(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s", ErrNotRegularFile, path)
	}
	if info.Size() > MaxConfigFileBytes {
		return nil, fmt.Errorf("%w: %s is %d bytes", ErrFileTooLarge, path, info.Size())
	}

	content, err := io.ReadAll(io.LimitReader(file, MaxConfigFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > MaxConfigFileBytes {
		return nil, fmt.Errorf("%w: %s", ErrFileTooLarge, path)
	}
	return content, nil
}

// isRegularFile reports whether path names an ordinary file. Directories,
// FIFOs, sockets and devices are not marker files, and must never be read as
// configuration.
func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
