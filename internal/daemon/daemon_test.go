package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/ipc"
	"github.com/Vadym903/Intenter/internal/logging"
	"github.com/Vadym903/Intenter/internal/platform"
	"github.com/Vadym903/Intenter/internal/storage"
	"github.com/Vadym903/Intenter/internal/version"
)

// shortDir creates a directory with a path short enough for a unix socket
// (§10.1); the default macOS temp dir already exceeds the limit.
func shortDir(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return t.TempDir()
	}
	dir, err := os.MkdirTemp("/tmp", "agd")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// testPlatform builds a platform rooted entirely in temp directories.
func testPlatform(t *testing.T) platform.Platform {
	t.Helper()
	base := shortDir(t)

	// The home directory must exist before the platform resolves it: scope
	// classification compares canonical paths, and on macOS /tmp is a symlink
	// to /private/tmp.
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}

	t.Setenv(platform.EnvTestMode, "1")
	t.Setenv(platform.EnvTestHome, home)
	t.Setenv(platform.EnvDataDir, filepath.Join(base, "data"))
	t.Setenv(platform.EnvConfigDir, filepath.Join(base, "config"))
	t.Setenv(platform.EnvRuntimeDir, filepath.Join(base, "run"))
	t.Setenv(platform.EnvEndpoint, "")

	p, err := platform.New()
	if err != nil {
		t.Fatalf("platform.New: %v", err)
	}
	return p
}

// startDaemon runs a daemon until the test ends and returns a connected client.
func startDaemon(t *testing.T, p platform.Platform) *ipc.Client {
	t.Helper()
	client, _ := startDaemonInstance(t, p)
	return client
}

// startDaemonInstance is startDaemon for a test that also needs the daemon
// itself, to inspect state the protocol does not expose.
func startDaemonInstance(t *testing.T, p platform.Platform) (*ipc.Client, *Daemon) {
	t.Helper()
	return startDaemonInstanceWith(t, p, nil)
}

// startDaemonInstanceWith runs a daemon after letting a test adjust it.
//
// The hook runs before Run, which is the only safe moment: once the daemon is
// serving, its fields are read by a connection goroutine per request, and a
// test that writes one is racing the thing it is testing.
func startDaemonInstanceWith(t *testing.T, p platform.Platform, configure func(*Daemon)) (*ipc.Client, *Daemon) {
	t.Helper()

	ready := make(chan struct{})
	d, err := newDaemon(p, ready)
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	if configure != nil {
		configure(d)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon exited during startup: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not become ready")
	}

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("daemon.Run: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("daemon did not stop")
		}
	})

	return ipc.NewClient(d.Endpoint()), d
}

func newDaemon(p platform.Platform, ready chan struct{}) (*Daemon, error) {
	var readyCh chan<- struct{}
	if ready != nil {
		readyCh = ready
	}
	return New(Options{
		Platform: p,
		Config:   config.Default(),
		Logger:   logging.Discard(),
		Ready:    readyCh,
	})
}

func TestDaemonStartupSequence(t *testing.T) {
	p := testPlatform(t)
	client := startDaemon(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Step 5: it serves.
	ping, err := client.Ping(ctx)
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if ping.PID != os.Getpid() || ping.ProtocolVersion != version.ProtocolVersion {
		t.Errorf("ping = %+v", ping)
	}

	// Step 2: lock and pid file exist.
	if pid := ReadPidFile(platform.PidFilePath(p)); pid != os.Getpid() {
		t.Errorf("pid file = %d, want %d", pid, os.Getpid())
	}

	// Step 3: the database exists and is migrated.
	db, err := storage.OpenReadOnly(platform.DatabasePath(p))
	if err != nil {
		t.Fatalf("database not created: %v", err)
	}
	defer db.Close()
	schema, err := storage.SchemaVersion(ctx, db)
	if err != nil || schema != version.SchemaVersion {
		t.Errorf("schema version = %d (%v), want %d", schema, err, version.SchemaVersion)
	}

	// Step 4: daemon.json describes the running daemon.
	info, err := ipc.ReadDaemonInfo(p.DataDir())
	if err != nil {
		t.Fatalf("daemon.json: %v", err)
	}
	if info.PID != os.Getpid() || info.Endpoint == "" || info.ProtocolVersion != version.ProtocolVersion {
		t.Errorf("daemon.json = %+v", info)
	}
	if info.StartedAt.IsZero() {
		t.Error("daemon.json must record the start time")
	}
}

func TestDaemonCleansUpOnShutdown(t *testing.T) {
	p := testPlatform(t)

	ready := make(chan struct{})
	d, err := newDaemon(p, ready)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon exited during startup: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not become ready")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not stop")
	}

	// Step 6: endpoint, pid file and daemon.json are removed.
	if _, err := ipc.ReadDaemonInfo(p.DataDir()); err == nil {
		t.Error("daemon.json must be removed on shutdown")
	}
	if _, err := os.Stat(platform.PidFilePath(p)); !errors.Is(err, os.ErrNotExist) {
		t.Error("pid file must be removed on shutdown")
	}
	if runtime.GOOS != "windows" {
		if _, err := os.Stat(p.IPCEndpoint()); !errors.Is(err, os.ErrNotExist) {
			t.Error("socket must be removed on shutdown")
		}
	}
}

func TestSecondInstanceRefusesToStart(t *testing.T) {
	p := testPlatform(t)
	startDaemon(t, p)

	second, err := newDaemon(p, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = second.Run(ctx)
	var alreadyRunning *ErrAlreadyRunning
	if !errors.As(err, &alreadyRunning) {
		t.Fatalf("second instance error = %v, want ErrAlreadyRunning (§9.1)", err)
	}
	if alreadyRunning.PID != os.Getpid() {
		t.Errorf("reported pid = %d, want %d", alreadyRunning.PID, os.Getpid())
	}
}

func TestLockIsReleasedAfterShutdown(t *testing.T) {
	p := testPlatform(t)

	lock, err := AcquireLock(platform.LockFilePath(p), platform.PidFilePath(p))
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if _, err := AcquireLock(platform.LockFilePath(p), platform.PidFilePath(p)); err == nil {
		t.Fatal("a second lock must fail while the first is held")
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	again, err := AcquireLock(platform.LockFilePath(p), platform.PidFilePath(p))
	if err != nil {
		t.Fatalf("lock must be re-acquirable after release: %v", err)
	}
	again.Release()
}

func TestStatusReportsCountersAndIntegration(t *testing.T) {
	p := testPlatform(t)
	client := startDaemon(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var status ipc.StatusResult
	if err := client.Call(ctx, ipc.MethodStatus, nil, &status); err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Daemon.Version != version.Version || status.Daemon.Endpoint == "" {
		t.Errorf("daemon status = %+v", status.Daemon)
	}
	if status.Daemon.DBPath != platform.DatabasePath(p) {
		t.Errorf("db path = %q", status.Daemon.DBPath)
	}
	if status.Daemon.ServiceMode != "unmanaged" {
		t.Errorf("service mode = %q, want unmanaged before setup", status.Daemon.ServiceMode)
	}
	for _, key := range []string{"active", "disabled", "revoked"} {
		if _, ok := status.Counts.Approvals[key]; !ok {
			t.Errorf("missing approval counter %q", key)
		}
	}
	for _, key := range []string{"allow", "ask", "block"} {
		if _, ok := status.Counts.Events24h[key]; !ok {
			t.Errorf("missing event counter %q", key)
		}
	}
	claude, ok := status.Integration["claude"]
	if !ok {
		t.Fatal("status must report the claude integration")
	}
	if claude.HooksInstalled {
		t.Error("hooks must not be reported as installed before setup")
	}
}

func TestShutdownMethodStopsTheDaemon(t *testing.T) {
	p := testPlatform(t)

	ready := make(chan struct{})
	d, err := newDaemon(p, ready)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- d.Run(context.Background()) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon exited during startup: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not become ready")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := ipc.NewClient(d.Endpoint())
	if err := client.Call(ctx, ipc.MethodShutdown, nil, nil); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the shutdown method did not stop the daemon")
	}
}

func TestUnknownMethodIsRejected(t *testing.T) {
	client := startDaemon(t, testPlatform(t))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Call(ctx, "no_such_method", nil, nil); !ipc.IsBadRequest(err) {
		t.Errorf("error = %v, want BAD_REQUEST", err)
	}
}

func TestWaitForPingAndRunning(t *testing.T) {
	p := testPlatform(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if Running(ctx, p, "") {
		t.Error("Running must be false before the daemon starts")
	}

	startDaemon(t, p)

	if _, err := WaitForPing(ctx, p, "", 5*time.Second); err != nil {
		t.Errorf("WaitForPing: %v", err)
	}
	if !Running(ctx, p, "") {
		t.Error("Running must be true while the daemon serves")
	}
}

func TestMigrationFailureIsFatal(t *testing.T) {
	p := testPlatform(t)
	if err := platform.EnsureDirs(p); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	// A database written by a newer build must stop the daemon (§9.3 step 3).
	db, err := storage.OpenAndMigrate(context.Background(), platform.DatabasePath(p))
	if err != nil {
		t.Fatalf("prepare db: %v", err)
	}
	if _, err := db.SQL().Exec(
		`INSERT INTO schema_version (version, applied_at) VALUES (?, ?)`,
		version.SchemaVersion+1, "2026-01-01T00:00:00.000000000Z"); err != nil {
		t.Fatalf("insert future schema version: %v", err)
	}
	db.Close()

	d, err := newDaemon(p, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var tooNew *storage.ErrSchemaTooNew
	if err := d.Run(ctx); !errors.As(err, &tooNew) {
		t.Fatalf("Run error = %v, want ErrSchemaTooNew", err)
	}
	// The failed start must not leave a daemon.json behind.
	if _, err := ipc.ReadDaemonInfo(p.DataDir()); err == nil {
		t.Error("a failed startup must not publish daemon.json")
	}
}
