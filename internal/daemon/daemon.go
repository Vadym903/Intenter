package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Vadym903/Intenter/internal/approval"
	"github.com/Vadym903/Intenter/internal/audit"
	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/ipc"
	"github.com/Vadym903/Intenter/internal/logging"
	"github.com/Vadym903/Intenter/internal/platform"
	"github.com/Vadym903/Intenter/internal/policy"
	"github.com/Vadym903/Intenter/internal/resolver"
	"github.com/Vadym903/Intenter/internal/storage"
	"github.com/Vadym903/Intenter/internal/version"
)

// shutdownGrace is how long in-flight requests may finish after a stop signal
// (§9.3 step 6).
const shutdownGrace = 2 * time.Second

// Options configure a daemon instance.
type Options struct {
	Platform platform.Platform
	Config   config.Config
	Logger   *slog.Logger
	// Endpoint overrides the platform endpoint; empty means the platform default.
	Endpoint string
	// Ready is closed once the daemon is serving; tests use it to synchronize.
	Ready chan<- struct{}
}

// Daemon is the single per-user background service: the only component that
// evaluates policy, matches approvals and writes audit rows (§9.1).
type Daemon struct {
	platform platform.Platform
	config   config.Config
	logger   *slog.Logger
	endpoint string
	ready    chan<- struct{}

	store     *storage.Store
	server    *ipc.Server
	lock      *Lock
	startedAt time.Time

	// The evaluation pipeline, built once the store is open.
	contexts      *resolver.ContextBuilder
	resolver      *resolver.Resolver
	engine        *policy.Engine
	importer      *approval.Importer
	approvals     *approval.Creator
	auditor       *audit.Recorder
	cache         *resultCache
	engineVersion int
	// now is injectable so stored timestamps are deterministic in tests.
	now func() time.Time

	// refresh notices when a newer binary has been installed underneath this
	// running daemon (contracts/release-artifacts.md).
	refresh     *refreshWatch
	refreshOnce sync.Once
	// updates keeps the cached release information fresh, off the request path.
	updates *updateChecker
	// exitCode is what Run reports to the caller; ExitCodeRefresh asks the
	// service manager to start the daemon again.
	//
	// It is written by whichever connection goroutine noticed the newer client
	// and read by the caller of Run, so it is atomic rather than relying on the
	// shutdown channel to order them.
	exitCode atomic.Int32

	shutdownRequested chan struct{}
}

// ExitCode is the status the process should exit with once Run returns. It is
// zero for an ordinary shutdown and ExitCodeRefresh when the daemon stopped to
// be restarted into a newer binary.
func (d *Daemon) ExitCode() int { return int(d.exitCode.Load()) }

// New builds a daemon; nothing is opened or bound until Run.
func New(opts Options) (*Daemon, error) {
	if opts.Platform == nil {
		return nil, errors.New("daemon: platform is required")
	}
	logger := opts.Logger
	if logger == nil {
		logger = logging.Discard()
	}
	endpoint := opts.Endpoint
	if endpoint == "" {
		endpoint = opts.Platform.IPCEndpoint()
	}
	return &Daemon{
		platform:          opts.Platform,
		config:            opts.Config,
		logger:            logger,
		endpoint:          endpoint,
		ready:             opts.Ready,
		engineVersion:     version.EngineVersion,
		now:               time.Now,
		shutdownRequested: make(chan struct{}),
	}, nil
}

// buildPipeline wires resolution, policy, approvals and audit together. It runs
// once the store is open, because every collaborator writes through it.
func (d *Daemon) buildPipeline() {
	d.contexts = resolver.NewContextBuilder(d.platform, d.config)
	d.resolver = resolver.New(d.contexts, d.engineVersion)
	d.approvals = approval.NewCreator(d.store)
	d.importer = approval.NewImporter(d.store, d.engineVersion)
	d.engine = policy.NewEngine(
		approval.NewMatcher(d.store, d.engineVersion),
		d.importer,
		d.engineVersion,
	)
	d.auditor = audit.NewRecorder(d.store)
	d.cache = newResultCache(CacheTTL)
}

// Run executes the startup sequence of §9.3 and serves until the context is
// canceled or a `shutdown` request arrives, then shuts down gracefully.
func (d *Daemon) Run(ctx context.Context) (err error) {
	if err := platform.EnsureDirs(d.platform); err != nil {
		return err
	}

	// Step 2: single-instance lock and pid file.
	lock, err := AcquireLock(platform.LockFilePath(d.platform), platform.PidFilePath(d.platform))
	if err != nil {
		return err
	}
	d.lock = lock
	defer func() {
		if releaseErr := d.lock.Release(); releaseErr != nil && err == nil {
			err = releaseErr
		}
	}()

	// Step 3: storage and migrations; a migration failure is fatal (§9.3).
	store, err := d.openStore(ctx)
	if err != nil {
		return err
	}
	d.store = store
	defer func() {
		if closeErr := d.store.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	d.buildPipeline()

	// Step 4: bind the endpoint and publish daemon.json.
	listener, err := ipc.Listen(d.endpoint)
	if err != nil {
		return err
	}
	d.startedAt = time.Now()

	info := ipc.DaemonInfo{
		Endpoint:        listener.Endpoint(),
		PID:             currentPID(),
		Version:         version.Version,
		ProtocolVersion: version.ProtocolVersion,
		StartedAt:       d.startedAt,
	}
	if err := ipc.WriteDaemonInfo(d.platform.DataDir(), info); err != nil {
		listener.Close()
		return err
	}
	defer func() {
		if removeErr := ipc.RemoveDaemonInfo(d.platform.DataDir()); removeErr != nil && err == nil {
			err = removeErr
		}
	}()

	// Step 5: serve, one goroutine per connection, per-request budget.
	d.server = ipc.NewServer(listener, d.logger, d.requestTimeout())
	if d.refresh == nil {
		d.refresh = newRefreshWatch(selfExecutablePath())
	}
	d.server.Observe(d.observeClient)
	d.registerHandlers()

	d.logger.Info("daemon started",
		"endpoint", listener.Endpoint(),
		"pid", info.PID,
		"version", version.Version,
		"db", d.store.DB.Path())
	if d.ready != nil {
		close(d.ready)
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- d.server.Serve(ctx) }()

	// Keeping the cached release information fresh is the daemon's job because
	// it is the only part of Intenter that is already running; the terminal
	// start-up path must never do it, and must never wait for it.
	checkCtx, stopChecks := context.WithCancel(ctx)
	defer stopChecks()
	if d.updates == nil {
		d.updates = newUpdateChecker(d.platform, d.config, d.logger)
	}
	go d.updates.run(checkCtx)

	// Step 6: graceful shutdown on signal (context) or `shutdown` request.
	select {
	case err := <-serveErr:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		d.logger.Info("daemon stopping", "reason", "context canceled")
	case <-d.shutdownRequested:
		d.logger.Info("daemon stopping", "reason", "shutdown requested")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if shutdownErr := d.server.Shutdown(shutdownCtx); shutdownErr != nil {
		d.logger.Warn("daemon: in-flight requests did not finish in time", "error", shutdownErr)
	}
	d.logger.Info("daemon stopped")
	return nil
}

func (d *Daemon) openStore(ctx context.Context) (*storage.Store, error) {
	// A prior installation under the product's old name is migrated into
	// place, and anything it left in the runtime directory is cleared, before
	// the database opens (contracts/identity-and-rename.md). Neither failure
	// is fatal to the daemon starting: worst case the old directory or files
	// are left for `doctor` to report and this is retried on the next start.
	if _, err := platform.MigrateLegacyDataDir(d.platform, d.logger); err != nil {
		d.logger.Warn("could not migrate the previous installation's data directory", "error", err)
	}
	if err := platform.RemoveStaleLegacyRuntimeFiles(d.platform); err != nil {
		d.logger.Warn("could not remove stale files from the previous installation's runtime directory", "error", err)
	}

	db, err := storage.OpenAndMigrate(ctx, platform.DatabasePath(d.platform))
	if err != nil {
		return nil, err
	}
	return storage.NewStore(db), nil
}

func (d *Daemon) requestTimeout() time.Duration {
	if d.config.Daemon.RequestTimeoutMS > 0 {
		return time.Duration(d.config.Daemon.RequestTimeoutMS) * time.Millisecond
	}
	return ipc.RequestTimeout
}

// RequestShutdown asks the daemon to stop; used by the `shutdown` method.
func (d *Daemon) RequestShutdown() {
	select {
	case <-d.shutdownRequested:
	default:
		close(d.shutdownRequested)
	}
}

// Endpoint is the address the daemon serves on.
func (d *Daemon) Endpoint() string { return d.endpoint }

// Store exposes the repositories to handlers and tests.
func (d *Daemon) Store() *storage.Store { return d.store }

// Uptime is how long the daemon has been serving.
func (d *Daemon) Uptime() time.Duration {
	if d.startedAt.IsZero() {
		return 0
	}
	return time.Since(d.startedAt)
}

// Stop is used by tests and by `daemon stop` in-process; it is equivalent to a
// shutdown request.
func (d *Daemon) Stop() { d.RequestShutdown() }

// ErrNotRunning is returned by helpers that need a live daemon.
var ErrNotRunning = fmt.Errorf("daemon is not running")
