//go:build windows

package ipc

import (
	"context"
	"fmt"
	"net"
	"os/user"

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

func (l *pipeListener) Close() error { return l.inner.Close() }

func (l *pipeListener) Endpoint() string { return l.endpoint }

func dial(ctx context.Context, endpoint string) (net.Conn, error) {
	conn, err := winio.DialPipeContext(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("ipc: connect to %s: %w", endpoint, err)
	}
	return conn, nil
}
