package ipc

import (
	"context"
	"net"
	"time"
)

// Default client timeouts (§10.2, contracts/ipc-protocol.md).
const (
	ConnectTimeout = 1 * time.Second
	RequestTimeout = 5 * time.Second
)

// Listener accepts local connections. One implementation per transport keeps
// the protocol code identical on every platform (§10.1).
type Listener interface {
	// Accept returns the next connection, already peer-verified.
	Accept() (net.Conn, error)
	// Close stops accepting and releases the endpoint.
	Close() error
	// Endpoint is the address clients connect to.
	Endpoint() string
}

// Listen binds the daemon endpoint. The endpoint is a socket path on
// macOS/Linux and a named pipe name on Windows.
func Listen(endpoint string) (Listener, error) { return listen(endpoint) }

// Dial connects to a daemon endpoint, honoring the context deadline.
func Dial(ctx context.Context, endpoint string) (net.Conn, error) { return dial(ctx, endpoint) }

// EndpointExists reports whether something is listening at the endpoint, used
// by `doctor` and by stale-socket cleanup.
func EndpointExists(ctx context.Context, endpoint string) bool {
	conn, err := Dial(ctx, endpoint)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
