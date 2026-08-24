package ipc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DaemonInfo is written to <DataDir>/daemon.json at startup and read by clients
// during endpoint discovery (§9.3 step 4, §10.1).
type DaemonInfo struct {
	Endpoint        string    `json:"endpoint"`
	PID             int       `json:"pid"`
	Version         string    `json:"version"`
	ProtocolVersion int       `json:"protocol_version"`
	StartedAt       time.Time `json:"started_at"`
}

// ErrDaemonUnavailable marks every failure to reach or speak to the daemon.
// The adapter turns it into a deferral, never an allow (INVARIANT I-3).
var ErrDaemonUnavailable = errors.New("intenter daemon unavailable")

// Client is a one-request-per-connection IPC client.
type Client struct {
	endpoint       string
	connectTimeout time.Duration
	requestTimeout time.Duration
}

// NewClient builds a client for an explicit endpoint.
func NewClient(endpoint string) *Client {
	return &Client{
		endpoint:       endpoint,
		connectTimeout: ConnectTimeout,
		requestTimeout: RequestTimeout,
	}
}

// WithTimeouts overrides the default connect and request timeouts.
func (c *Client) WithTimeouts(connect, request time.Duration) *Client {
	if connect > 0 {
		c.connectTimeout = connect
	}
	if request > 0 {
		c.requestTimeout = request
	}
	return c
}

// Endpoint is the address this client talks to.
func (c *Client) Endpoint() string { return c.endpoint }

// DiscoverEndpoint resolves the daemon endpoint in the documented order:
// INTENTER_ENDPOINT, then daemon.json in the data directory, then the
// platform default (§10.1).
func DiscoverEndpoint(envEndpoint, dataDir, platformDefault string) string {
	if endpoint := strings.TrimSpace(envEndpoint); endpoint != "" {
		return endpoint
	}
	if info, err := ReadDaemonInfo(dataDir); err == nil && info.Endpoint != "" {
		return info.Endpoint
	}
	return platformDefault
}

// ReadDaemonInfo loads <dataDir>/daemon.json.
func ReadDaemonInfo(dataDir string) (DaemonInfo, error) {
	var info DaemonInfo
	if dataDir == "" {
		return info, fmt.Errorf("ipc: no data directory")
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, "daemon.json"))
	if err != nil {
		return info, err
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return info, fmt.Errorf("ipc: parse daemon.json: %w", err)
	}
	return info, nil
}

// WriteDaemonInfo atomically writes <dataDir>/daemon.json.
func WriteDaemonInfo(dataDir string, info DaemonInfo) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("ipc: create %s: %w", dataDir, err)
	}
	encoded, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("ipc: encode daemon.json: %w", err)
	}
	encoded = append(encoded, '\n')

	final := filepath.Join(dataDir, "daemon.json")
	temp, err := os.CreateTemp(dataDir, "daemon-*.json")
	if err != nil {
		return fmt.Errorf("ipc: create temp daemon.json: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)

	if _, err := temp.Write(encoded); err != nil {
		temp.Close()
		return fmt.Errorf("ipc: write daemon.json: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("ipc: close daemon.json: %w", err)
	}
	if err := os.Chmod(tempName, 0o600); err != nil {
		return fmt.Errorf("ipc: chmod daemon.json: %w", err)
	}
	if err := os.Rename(tempName, final); err != nil {
		return fmt.Errorf("ipc: replace daemon.json: %w", err)
	}
	return nil
}

// RemoveDaemonInfo deletes daemon.json during shutdown.
func RemoveDaemonInfo(dataDir string) error {
	err := os.Remove(filepath.Join(dataDir, "daemon.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Call performs one request/response exchange. Transport, timeout and protocol
// failures are wrapped in ErrDaemonUnavailable; a daemon-reported error is
// returned as *Error so callers can distinguish NOT_FOUND from a dead daemon.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	requestID, err := NewRequestID()
	if err != nil {
		return err
	}
	request, err := NewRequest(requestID, method, params)
	if err != nil {
		return err
	}

	connectCtx, cancelConnect := context.WithTimeout(ctx, c.connectTimeout)
	defer cancelConnect()

	conn, err := Dial(connectCtx, c.endpoint)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrDaemonUnavailable, err)
	}
	defer conn.Close()

	deadline := time.Now().Add(c.requestTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("%w: %s", ErrDaemonUnavailable, err)
	}

	framer := NewFramer(conn)
	if err := framer.Write(request); err != nil {
		return fmt.Errorf("%w: %s", ErrDaemonUnavailable, err)
	}

	var response Response
	if err := framer.Read(&response); err != nil {
		return fmt.Errorf("%w: %s", ErrDaemonUnavailable, err)
	}
	if !SupportedProtocol(response.ProtocolVersion) {
		return fmt.Errorf("%w: daemon speaks protocol v%d, this build speaks v%d",
			ErrDaemonUnavailable, response.ProtocolVersion, request.ProtocolVersion)
	}
	if response.RequestID != request.RequestID {
		return fmt.Errorf("%w: response id %q does not match request id %q",
			ErrDaemonUnavailable, response.RequestID, request.RequestID)
	}

	if response.Error != nil {
		if response.Error.Code == CodeUnsupportedProtocol {
			return fmt.Errorf("%w: %s", ErrDaemonUnavailable, response.Error.Message)
		}
		return response.Error
	}
	return response.DecodeResult(result)
}

// Ping checks that the daemon answers and reports its versions.
func (c *Client) Ping(ctx context.Context) (PingResult, error) {
	var result PingResult
	err := c.Call(ctx, MethodPing, nil, &result)
	return result, err
}

// NewRequestID returns a random request identifier.
func NewRequestID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("ipc: generate request id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// IsUnavailable reports whether an error means the daemon could not be reached
// or understood — the condition that must always lead to deferral (I-3).
func IsUnavailable(err error) bool { return errors.Is(err, ErrDaemonUnavailable) }

// IsNotFound reports whether the daemon answered with NOT_FOUND.
func IsNotFound(err error) bool { return hasCode(err, CodeNotFound) }

// IsBadRequest reports whether the daemon rejected the request as invalid.
func IsBadRequest(err error) bool { return hasCode(err, CodeBadRequest) }

func hasCode(err error, code string) bool {
	var protocolErr *Error
	return errors.As(err, &protocolErr) && protocolErr.Code == code
}
