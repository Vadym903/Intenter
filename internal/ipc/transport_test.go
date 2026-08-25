package ipc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// testEndpoint returns a per-test endpoint for the platform's transport.
//
// Unix sockets have a ~104 byte path limit, and the default temp directory on
// macOS is already close to it, so socket tests use a short directory under
// /tmp — the same advice §10.1 gives users via INTENTER_RUNTIME_DIR.
func testEndpoint(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		sum := sha256.Sum256([]byte(t.Name()))
		return `\\.\pipe\intenter-test-` + hex.EncodeToString(sum[:])[:16]
	}
	return filepath.Join(shortTempDir(t), "intenter.sock")
}

// shortTempDir creates a temporary directory with a path short enough for a
// unix socket.
func shortTempDir(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return t.TempDir()
	}
	dir, err := os.MkdirTemp("/tmp", "ag")
	if err != nil {
		t.Fatalf("create short temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestListenAndDialRoundTrip(t *testing.T) {
	endpoint := testEndpoint(t)

	listener, err := Listen(endpoint)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()

	if listener.Endpoint() != endpoint {
		t.Errorf("Endpoint = %q, want %q", listener.Endpoint(), endpoint)
	}

	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		framer := NewFramer(conn)
		var req Request
		if err := framer.Read(&req); err != nil {
			done <- err
			return
		}
		resp, err := NewResponse(req.RequestID, PingResult{Version: "test"})
		if err != nil {
			done <- err
			return
		}
		done <- framer.Write(resp)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := Dial(ctx, endpoint)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	framer := NewFramer(conn)
	req, _ := NewRequest("req-1", MethodPing, nil)
	if err := framer.Write(req); err != nil {
		t.Fatalf("write: %v", err)
	}
	var resp Response
	if err := framer.Read(&resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !resp.OK || resp.RequestID != "req-1" {
		t.Errorf("response = %+v", resp)
	}
	if err := <-done; err != nil {
		t.Fatalf("server side: %v", err)
	}
}

func TestDialFailsWhenNothingListens(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := Dial(ctx, testEndpoint(t)); err == nil {
		t.Error("dialing an unbound endpoint must fail")
	}
	if EndpointExists(ctx, testEndpoint(t)) {
		t.Error("EndpointExists must be false for an unbound endpoint")
	}
}

func TestEndpointExistsWhenListening(t *testing.T) {
	endpoint := testEndpoint(t)
	listener, err := Listen(endpoint)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !EndpointExists(ctx, endpoint) {
		t.Error("EndpointExists must be true while the daemon listens")
	}
}

func TestCloseReleasesTheEndpoint(t *testing.T) {
	endpoint := testEndpoint(t)

	listener, err := Listen(endpoint)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Re-binding the same endpoint must work after a clean shutdown.
	again, err := Listen(endpoint)
	if err != nil {
		t.Fatalf("re-Listen: %v", err)
	}
	again.Close()
}

// TestCloseReturnsDuringDialStorm pins the go-winio workaround in
// pipeListener.Close: a client that connects and instantly disconnects while
// Close races an in-flight Accept can swallow winio's one close signal and
// leave Close blocked forever — the claude adapter suite hit this as a
// 30-minute CI hang. The storm makes that race likely; the deadline turns a
// hang into a failure instead of a package timeout. On unix the storm just
// exercises an uncontroversial Close.
func TestCloseReturnsDuringDialStorm(t *testing.T) {
	for round := range 10 {
		listener, err := Listen(testEndpoint(t))
		if err != nil {
			t.Fatalf("round %d: Listen: %v", round, err)
		}

		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				conn.Close()
			}
		}()

		stormCtx, stopStorm := context.WithCancel(context.Background())
		var storm sync.WaitGroup
		for range 4 {
			storm.Add(1)
			go func() {
				defer storm.Done()
				for stormCtx.Err() == nil {
					dialCtx, cancel := context.WithTimeout(stormCtx, time.Second)
					if conn, err := Dial(dialCtx, listener.Endpoint()); err == nil {
						conn.Close()
					}
					cancel()
				}
			}()
		}

		time.Sleep(10 * time.Millisecond) // let the storm reach the listener
		closed := make(chan error, 1)
		go func() { closed <- listener.Close() }()
		select {
		case err := <-closed:
			if err != nil {
				t.Fatalf("round %d: Close: %v", round, err)
			}
		case <-time.After(30 * time.Second):
			stopStorm()
			t.Fatalf("round %d: Close did not return: the winio close race is back", round)
		}
		stopStorm()
		storm.Wait()
	}
}

func TestConcurrentConnections(t *testing.T) {
	endpoint := testEndpoint(t)
	listener, err := Listen(endpoint)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c interface{ Close() error }) {
				defer c.Close()
				framer := NewFramer(c.(interface {
					Read([]byte) (int, error)
					Write([]byte) (int, error)
				}))
				var req Request
				if err := framer.Read(&req); err != nil {
					return
				}
				resp, _ := NewResponse(req.RequestID, PingResult{Version: req.RequestID})
				_ = framer.Write(resp)
			}(conn)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for i := range 5 {
		id := fmt.Sprintf("req-%d", i)
		conn, err := Dial(ctx, endpoint)
		if err != nil {
			t.Fatalf("Dial %d: %v", i, err)
		}
		framer := NewFramer(conn)
		req, _ := NewRequest(id, MethodPing, nil)
		if err := framer.Write(req); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		var resp Response
		if err := framer.Read(&resp); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		var ping PingResult
		if err := resp.DecodeResult(&ping); err != nil {
			t.Fatalf("decode %d: %v", i, err)
		}
		if ping.Version != id {
			t.Errorf("response %d = %q, want %q", i, ping.Version, id)
		}
		conn.Close()
	}
}
