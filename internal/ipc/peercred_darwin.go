//go:build darwin

package ipc

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// maxSocketPathLen is the sun_path limit on macOS (§10.1).
const maxSocketPathLen = 104

// peerUID reads the connecting process's user id via LOCAL_PEERCRED.
func peerUID(conn net.Conn) (int, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("ipc: not a unix connection")
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return 0, err
	}

	var (
		uid       int
		innerErr  error
		controlOK bool
	)
	err = raw.Control(func(fd uintptr) {
		cred, credErr := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if credErr != nil {
			innerErr = credErr
			return
		}
		uid = int(cred.Uid)
		controlOK = true
	})
	if err != nil {
		return 0, err
	}
	if innerErr != nil {
		return 0, innerErr
	}
	if !controlOK {
		return 0, fmt.Errorf("ipc: peer credentials unavailable")
	}
	return uid, nil
}
