//go:build windows

package ipc

import (
	"context"
	"fmt"
	"net"
	"os/user"
	"time"

	"github.com/Microsoft/go-winio"
)

// pipeBufferSize sizes the named pipe buffers; messages are capped at 1 MiB by
// the framing layer.
const pipeBufferSize = 64 * 1024

type pipeListener struct {
	inner    net.Listener
	endpoint string
}

func listen(endpoint string) (Listener, error) {
	descriptor, err := currentUserSecurityDescriptor()
	if err != nil {
		return nil, err
	}
	inner, err := winio.ListenPipe(endpoint, &winio.PipeConfig{
		SecurityDescriptor: descriptor,
		MessageMode:        false,
		InputBufferSize:    pipeBufferSize,
		OutputBufferSize:   pipeBufferSize,
	})
	if err != nil {
		return nil, fmt.Errorf("ipc: listen on %s: %w", endpoint, err)
	}
	return &pipeListener{inner: inner, endpoint: endpoint}, nil
}

// currentUserSecurityDescriptor builds an SDDL granting full access to the
// current user only, so no other account can reach the daemon (§10.1).
func currentUserSecurityDescriptor() (string, error) {
	current, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("ipc: determine current user: %w", err)
	}
	// D:P             — DACL, protected from inheritance
	// (A;;GA;;;<SID>) — allow generic-all to this user
	// (A;;GA;;;SY)    — allow generic-all to LocalSystem, which owns services
	return fmt.Sprintf("D:P(A;;GA;;;%s)(A;;GA;;;SY)", current.Uid), nil
}

func (l *pipeListener) Accept() (net.Conn, error) {
	// The pipe ACL restricts the peer, so no extra credential check is needed.
	return l.inner.Accept()
}

// Close works around a go-winio deadlock (present through v0.6.2 and main as
// of Jan 2026): when Close races with an in-flight Accept whose client
// connected and instantly disconnected, the listener goroutine consumes the
// one close signal but sees ERROR_NO_DATA instead of its closed marker, keeps
// running, and winio's Close blocks on its done channel forever. Every resting
// state of that goroutine still honors a fresh close signal, so if Close has
// not returned after a grace period, sending another one — which is exactly
// what a repeated winio Close does — unwedges it.
func (l *pipeListener) Close() error {
	done := make(chan error, 1)
	go func() { done <- l.inner.Close() }()
	for {
		select {
		case err := <-done:
			return err
		case <-time.After(100 * time.Millisecond):
			// Once the listener really is closed, this extra Close returns
			// via winio's done channel, so no goroutine outlives the retry.
			go func() { _ = l.inner.Close() }()
		}
	}
}

func (l *pipeListener) Endpoint() string { return l.endpoint }

func dial(ctx context.Context, endpoint string) (net.Conn, error) {
	conn, err := winio.DialPipeContext(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("ipc: connect to %s: %w", endpoint, err)
	}
	return conn, nil
}
