package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/ipc"
	"github.com/Vadym903/Intenter/internal/platform"
	"github.com/Vadym903/Intenter/internal/storage"
	"github.com/Vadym903/Intenter/internal/version"
)

// handlePing answers liveness and version checks (§10.4).
func (d *Daemon) handlePing(context.Context, *ipc.Request) (any, error) {
	return ipc.PingResult{
		Version:         version.Version,
		EngineVersion:   version.EngineVersion,
		ProtocolVersion: version.ProtocolVersion,
		UptimeS:         int64(d.Uptime().Seconds()),
		PID:             currentPID(),
	}, nil
}

// handleStatus reports daemon state, counters and integration state
// (contracts/ipc-protocol.md `status`).
func (d *Daemon) handleStatus(ctx context.Context, _ *ipc.Request) (any, error) {
	ctx = ctxOrBackground(ctx)

	approvals, err := d.store.Approvals.CountByState(ctx)
	if err != nil {
		return nil, err
	}
	events, err := d.store.Audit.CountByDecisionSince(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		return nil, err
	}
	meta, err := d.store.Meta.All(ctx)
	if err != nil {
		return nil, err
	}

	serviceMode := meta[storage.MetaServiceMode]
	if serviceMode == "" {
		serviceMode = "unmanaged"
	}

	result := ipc.StatusResult{
		Daemon: ipc.StatusDaemon{
			Version:     version.Version,
			UptimeS:     int64(d.Uptime().Seconds()),
			Endpoint:    d.endpoint,
			DBPath:      d.store.DB.Path(),
			ServiceMode: serviceMode,
			PID:         currentPID(),
		},
		Counts: ipc.StatusCounts{
			Approvals: map[string]int{
				"active":   approvals[action.ApprovalActive],
				"disabled": approvals[action.ApprovalDisabled],
				"revoked":  approvals[action.ApprovalRevoked],
			},
			Events24h: map[string]int{
				"allow": events[action.OutcomeAllow],
				"ask":   events[action.OutcomeAsk],
				"block": events[action.OutcomeBlock],
			},
		},
		Integration: map[string]ipc.StatusAgent{
			"claude": {
				HooksInstalled: meta[storage.MetaHooksVersion] != "",
				SettingsPath:   meta[storage.MetaClaudeSettingsPath],
				AgentVersion:   meta[storage.MetaClaudeVersion],
			},
		},
	}
	return result, nil
}

// handleShutdown stops the daemon after the response has been written
// (§9.2, used by `daemon stop` in unmanaged mode).
func (d *Daemon) handleShutdown(context.Context, *ipc.Request) (any, error) {
	go func() {
		// Let the response reach the client before the listener closes.
		time.Sleep(50 * time.Millisecond)
		d.RequestShutdown()
	}()
	return map[string]any{}, nil
}

// Ping asks a running daemon whether it is alive, used by the CLI and setup.
func Ping(ctx context.Context, p platform.Platform, endpointOverride string) (ipc.PingResult, error) {
	endpoint := ipc.DiscoverEndpoint(endpointOverride, p.DataDir(), p.IPCEndpoint())
	return ipc.NewClient(endpoint).Ping(ctx)
}

// WaitForPing polls until the daemon answers or the deadline passes (§9.2:
// `daemon start` waits up to 5 s).
func WaitForPing(ctx context.Context, p platform.Platform, endpointOverride string, timeout time.Duration) (ipc.PingResult, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		result, err := Ping(ctx, p, endpointOverride)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return ipc.PingResult{}, lastErr
		}
		select {
		case <-ctx.Done():
			return ipc.PingResult{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Running reports whether a daemon answers on the discovered endpoint.
func Running(ctx context.Context, p platform.Platform, endpointOverride string) bool {
	_, err := Ping(ctx, p, endpointOverride)
	return err == nil
}

// ErrLockHeld reports that another instance holds the single-instance lock.
var ErrLockHeld = errors.New("daemon: single-instance lock is held")
