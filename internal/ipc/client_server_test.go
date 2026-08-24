package ipc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/logging"
	"github.com/Vadym903/Intenter/internal/version"
)

// startTestServer binds an endpoint, registers handlers and serves until the
// test ends.
func startTestServer(t *testing.T, register func(*Server)) *Client {
	t.Helper()

	endpoint := testEndpoint(t)
	listener, err := Listen(endpoint)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	server := NewServer(listener, logging.Discard(), 2*time.Second)
	register(server)

	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx) }()

	t.Cleanup(func() {
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
		if err := <-served; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})

	return NewClient(endpoint)
}

func TestClientServerPing(t *testing.T) {
	client := startTestServer(t, func(s *Server) {
		s.Handle(MethodPing, func(ctx context.Context, req *Request) (any, error) {
			return PingResult{
				Version:         version.Version,
				EngineVersion:   version.EngineVersion,
				ProtocolVersion: version.ProtocolVersion,
				UptimeS:         3,
				PID:             os.Getpid(),
			}, nil
		})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := client.Ping(ctx)
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if result.PID != os.Getpid() || result.EngineVersion != version.EngineVersion {
		t.Errorf("ping = %+v", result)
	}
}

func TestServerReturnsStructuredErrors(t *testing.T) {
	client := startTestServer(t, func(s *Server) {
		s.Handle(MethodGetApproval, func(ctx context.Context, req *Request) (any, error) {
			return nil, Errorf(CodeNotFound, "approval 42 not found")
		})
		s.Handle(MethodStatus, func(ctx context.Context, req *Request) (any, error) {
			return nil, errors.New("disk on fire")
		})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.Call(ctx, MethodGetApproval, GetApprovalParams{ID: 42}, nil)
	if !IsNotFound(err) {
		t.Errorf("error = %v, want NOT_FOUND", err)
	}
	if IsUnavailable(err) {
		t.Error("a daemon-reported error must not look like an unreachable daemon")
	}

	err = client.Call(ctx, MethodStatus, nil, nil)
	var protocolErr *Error
	if !errors.As(err, &protocolErr) || protocolErr.Code != CodeInternal {
		t.Errorf("error = %v, want INTERNAL", err)
	}
}

func TestServerRejectsUnknownMethod(t *testing.T) {
	client := startTestServer(t, func(s *Server) {})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.Call(ctx, "does_not_exist", nil, nil)
	if !IsBadRequest(err) {
		t.Errorf("error = %v, want BAD_REQUEST (contracts/ipc-protocol.md)", err)
	}
}

func TestServerRejectsUnsupportedProtocolVersion(t *testing.T) {
	endpoint := testEndpoint(t)
	listener, err := Listen(endpoint)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	server := NewServer(listener, logging.Discard(), time.Second)
	server.Handle(MethodPing, func(ctx context.Context, req *Request) (any, error) {
		return PingResult{}, nil
	})
	go server.Serve(context.Background())
	defer server.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := Dial(ctx, endpoint)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	framer := NewFramer(conn)
	if err := framer.Write(&Request{ProtocolVersion: 99, RequestID: "x", Method: MethodPing}); err != nil {
		t.Fatalf("write: %v", err)
	}
	var response Response
	if err := framer.Read(&response); err != nil {
		t.Fatalf("read: %v", err)
	}
	if response.OK || response.Error.Code != CodeUnsupportedProtocol {
		t.Errorf("response = %+v, want UNSUPPORTED_PROTOCOL", response)
	}
}

func TestClientTreatsProtocolMismatchAsUnavailable(t *testing.T) {
	endpoint := testEndpoint(t)
	listener, err := Listen(endpoint)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		framer := NewFramer(conn)
		var req Request
		if err := framer.Read(&req); err != nil {
			return
		}
		_ = framer.Write(&Response{ProtocolVersion: 99, RequestID: req.RequestID, OK: true})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = NewClient(endpoint).Call(ctx, MethodPing, nil, nil)
	if !IsUnavailable(err) {
		t.Errorf("error = %v, want ErrDaemonUnavailable (I-3)", err)
	}
}

func TestServerRecoversFromHandlerPanic(t *testing.T) {
	client := startTestServer(t, func(s *Server) {
		s.Handle(MethodEvaluate, func(ctx context.Context, req *Request) (any, error) {
			panic("boom")
		})
		s.Handle(MethodPing, func(ctx context.Context, req *Request) (any, error) {
			return PingResult{Version: "alive"}, nil
		})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.Call(ctx, MethodEvaluate, EvaluateParams{}, nil)
	var protocolErr *Error
	if !errors.As(err, &protocolErr) || protocolErr.Code != CodeInternal {
		t.Fatalf("error = %v, want INTERNAL after a panic", err)
	}

	// The daemon must still serve other requests.
	result, err := client.Ping(ctx)
	if err != nil || result.Version != "alive" {
		t.Errorf("daemon did not survive the panic: %v / %+v", err, result)
	}
}

func TestClientReportsUnreachableDaemon(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	client := NewClient(testEndpoint(t)).WithTimeouts(200*time.Millisecond, time.Second)
	if err := client.Call(ctx, MethodPing, nil, nil); !IsUnavailable(err) {
		t.Errorf("error = %v, want ErrDaemonUnavailable", err)
	}
}

func TestClientHonorsRequestTimeout(t *testing.T) {
	client := startTestServer(t, func(s *Server) {
		s.Handle(MethodEvaluate, func(ctx context.Context, req *Request) (any, error) {
			select {
			case <-time.After(3 * time.Second):
				return EvaluateResult{}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})
	})
	client.WithTimeouts(time.Second, 300*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	err := client.Call(ctx, MethodEvaluate, EvaluateParams{}, nil)
	if !IsUnavailable(err) {
		t.Errorf("error = %v, want ErrDaemonUnavailable on timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("client waited %v, want the request timeout to apply", elapsed)
	}
}

func TestConcurrentClientCalls(t *testing.T) {
	client := startTestServer(t, func(s *Server) {
		s.Handle(MethodPing, func(ctx context.Context, req *Request) (any, error) {
			return PingResult{Version: req.RequestID}, nil
		})
	})

	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := client.Ping(ctx); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent ping: %v", err)
	}
}

func TestShutdownWaitsForInFlightRequests(t *testing.T) {
	endpoint := testEndpoint(t)
	listener, err := Listen(endpoint)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	started := make(chan struct{})
	finished := make(chan struct{})
	server := NewServer(listener, logging.Discard(), 2*time.Second)
	server.Handle(MethodPing, func(ctx context.Context, req *Request) (any, error) {
		close(started)
		time.Sleep(200 * time.Millisecond)
		close(finished)
		return PingResult{}, nil
	})
	go server.Serve(context.Background())

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = NewClient(endpoint).Ping(ctx)
	}()

	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case <-finished:
	default:
		t.Error("Shutdown returned before the in-flight request finished")
	}
}

func TestDaemonInfoRoundTripAndDiscovery(t *testing.T) {
	dataDir := t.TempDir()
	info := DaemonInfo{
		Endpoint:        "/tmp/ag/intenter.sock",
		PID:             4242,
		Version:         "0.1.0",
		ProtocolVersion: version.ProtocolVersion,
		StartedAt:       time.Now().UTC().Truncate(time.Second),
	}
	if err := WriteDaemonInfo(dataDir, info); err != nil {
		t.Fatalf("WriteDaemonInfo: %v", err)
	}

	got, err := ReadDaemonInfo(dataDir)
	if err != nil {
		t.Fatalf("ReadDaemonInfo: %v", err)
	}
	if got.Endpoint != info.Endpoint || got.PID != info.PID {
		t.Errorf("daemon info = %+v", got)
	}
	if entries, _ := filepath.Glob(filepath.Join(dataDir, "daemon-*.json")); len(entries) != 0 {
		t.Errorf("atomic write left temp files behind: %v", entries)
	}

	// Discovery order: env wins, then daemon.json, then the platform default.
	if endpoint := DiscoverEndpoint("/env/endpoint.sock", dataDir, "/default.sock"); endpoint != "/env/endpoint.sock" {
		t.Errorf("env endpoint must win, got %q", endpoint)
	}
	if endpoint := DiscoverEndpoint("", dataDir, "/default.sock"); endpoint != info.Endpoint {
		t.Errorf("daemon.json endpoint must be used, got %q", endpoint)
	}
	if endpoint := DiscoverEndpoint("", t.TempDir(), "/default.sock"); endpoint != "/default.sock" {
		t.Errorf("platform default must be the fallback, got %q", endpoint)
	}

	if err := RemoveDaemonInfo(dataDir); err != nil {
		t.Fatalf("RemoveDaemonInfo: %v", err)
	}
	if _, err := ReadDaemonInfo(dataDir); err == nil {
		t.Error("daemon.json must be gone after shutdown")
	}
	if err := RemoveDaemonInfo(dataDir); err != nil {
		t.Errorf("removing a missing daemon.json must be a no-op: %v", err)
	}
}
