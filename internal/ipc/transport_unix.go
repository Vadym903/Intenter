//go:build darwin || linux

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// socketMode keeps the socket readable only by its owner; the containing
// directory is 0700 (§10.1).
const socketMode os.FileMode = 0o600

// unixListener adds peer verification and endpoint cleanup to net.Listener.
type unixListener struct {
	inner    net.Listener
	endpoint string
	ownerUID int
}

func listen(endpoint string) (Listener, error) {
	if len(endpoint) > maxSocketPathLen {
		return nil, fmt.Errorf(
			"ipc: socket path %q is %d bytes, over the %d byte platform limit; set INTENTER_RUNTIME_DIR to a shorter directory",
			endpoint, len(endpoint), maxSocketPathLen)
	}

	dir := filepath.Dir(endpoint)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("ipc: create %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("ipc: chmod %s: %w", dir, err)
	}
	// A socket file left behind by a crashed daemon would block bind; the
	// single-instance lock guarantees no live daemon owns it.
	if err := os.Remove(endpoint); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("ipc: remove stale socket %s: %w", endpoint, err)
	}

	inner, err := net.Listen("unix", endpoint)
	if err != nil {
		return nil, fmt.Errorf("ipc: listen on %s: %w", endpoint, err)
	}
	if err := os.Chmod(endpoint, socketMode); err != nil {
		inner.Close()
		return nil, fmt.Errorf("ipc: chmod socket %s: %w", endpoint, err)
	}

	return &unixListener{inner: inner, endpoint: endpoint, ownerUID: os.Getuid()}, nil
}

// Accept returns the next connection whose peer runs as the same user. Local
// sockets are already restricted by directory permissions; the peer check is
// defense in depth (§10.1).
func (l *unixListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.inner.Accept()
		if err != nil {
			return nil, err
		}
		uid, err := peerUID(conn)
		if err != nil {
			// Peer credentials are unavailable on some kernels; the filesystem
			// permissions still restrict access, so serve the connection.
			return conn, nil
		}
		if uid != l.ownerUID {
			conn.Close()
			continue
		}
		return conn, nil
	}
}

func (l *unixListener) Close() error {
	err := l.inner.Close()
	if removeErr := os.Remove(l.endpoint); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) && err == nil {
		err = removeErr
	}
	return err
}

func (l *unixListener) Endpoint() string { return l.endpoint }

func dial(ctx context.Context, endpoint string) (net.Conn, error) {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", endpoint)
	if err != nil {
		return nil, fmt.Errorf("ipc: connect to %s: %w", endpoint, err)
	}
	return conn, nil
}
