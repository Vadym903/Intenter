package claude

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/adapter"
	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/ipc"
	"github.com/Vadym903/Intenter/internal/logging"
	"github.com/Vadym903/Intenter/internal/platform"
)

// DaemonWarningInterval is how rarely the "daemon unavailable" notice may
// repeat for one session (contracts/claude-hooks.md).
const DaemonWarningInterval = time.Hour

// AdapterActionTimeout bounds the audit annotation the hook writes after
// answering. It is deliberately far below the default IPC timeouts: nothing is
// waiting on this write, so a daemon that has stopped answering must cost the
// session a fraction of a second rather than several.
const AdapterActionTimeout = 500 * time.Millisecond

// Adapter implements the Claude Code hook protocol.
//
// Its contract with the agent is narrow on purpose: read one event, write at
// most one JSON object, always exit 0. Anything Intenter cannot decide —
// unparsable input, an unreachable daemon, a panic — produces silence, so the
// session behaves exactly as it would without Intenter installed
// (INVARIANT I-2, I-12).
type Adapter struct {
	platform platform.Platform
	config   config.Config
	logger   *slog.Logger
	settings *SettingsReader
	// client is the daemon connection; nil means build one per invocation.
	client *ipc.Client
	now    func() time.Time
	// lazyStart is called when the daemon is unreachable; nil disables it.
	lazyStart func(context.Context) error
}

// New builds the Claude adapter.
func New(p platform.Platform, cfg config.Config, logger *slog.Logger) *Adapter {
	if logger == nil {
		logger = logging.Discard()
	}
	return &Adapter{
		platform: p,
		config:   cfg,
		logger:   logger,
		settings: NewSettingsReader(p, cfg.Claude.SettingsPath),
		now:      time.Now,
	}
}

// WithClient pins the daemon client, used by tests and by setup's self-test.
func (a *Adapter) WithClient(client *ipc.Client) *Adapter {
	a.client = client
	return a
}

// WithLazyStart installs the hook-side daemon start (§9.5).
func (a *Adapter) WithLazyStart(start func(context.Context) error) *Adapter {
	a.lazyStart = start
	return a
}

// Name identifies the agent.
func (a *Adapter) Name() string { return Agent }

// Run handles one hook invocation. It returns an error only for the caller's
// logs; the caller still exits 0.
func (a *Adapter) Run(ctx context.Context, env adapter.IO) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			// A crash in the gate must not become a crash in the session.
			a.logger.Error("claude: hook panicked", "panic", recovered)
			err = fmt.Errorf("claude: hook panicked: %v", recovered)
		}
	}()

	event, decodeErr := DecodeEvent(env.Stdin)
	if decodeErr != nil {
		a.logger.Warn("claude: could not read the hook event", "error", decodeErr)
		return decodeErr
	}
	if !event.Gated() {
		return nil
	}

	response, after, handleErr := a.handle(ctx, event, env)
	if response != nil {
		if writeErr := writeResponse(env.Stdout, response); writeErr != nil {
			a.logger.Error("claude: could not write the hook response", "error", writeErr)
			return writeErr
		}
	}
	// Bookkeeping runs after the agent has its answer, so a slow or dead daemon
	// delays an audit annotation rather than the session.
	if after != nil {
		after(ctx)
	}
	return handleErr
}

// handle dispatches one event. It returns what to print and, optionally, work
// to do once that has been written.
func (a *Adapter) handle(ctx context.Context, event *Event, env adapter.IO) (*Response, func(context.Context), error) {
	switch event.HookEventName {
	case EventPreToolUse:
		return a.handlePreToolUse(ctx, event, env)
	case EventPermissionRequest:
		return a.handlePermissionRequest(ctx, event, env)
	case EventPostToolUse:
		return nil, nil, a.handlePostToolUse(ctx, event, env)
	case EventConfigChange:
		return nil, nil, a.handleConfigChange(ctx, event)
	case EventSessionEnd:
		return a.handleSessionEnd(ctx, event)
	}
	return nil, nil, nil
}

// handlePreToolUse evaluates the command and maps the decision.
func (a *Adapter) handlePreToolUse(ctx context.Context, event *Event, env adapter.IO) (*Response, func(context.Context), error) {
	result, err := a.evaluate(ctx, event, env)
	if err != nil {
		return a.daemonUnavailable(event, err), nil, err
	}

	response, adapterAction := MapPreToolUse(result, event.Bypassing())
	return response, a.adapterActionReporter(result, adapterAction), nil
}

// handlePermissionRequest re-checks the command and records that Claude showed
// its own dialog (§11.4).
func (a *Adapter) handlePermissionRequest(ctx context.Context, event *Event, env adapter.IO) (*Response, func(context.Context), error) {
	result, err := a.evaluate(ctx, event, env)
	if err != nil {
		// The dialog is already on screen; a silent failure is the right
		// outcome, and the warning belongs to PreToolUse.
		return nil, nil, err
	}

	client, clientErr := a.daemonClient()
	if clientErr == nil {
		var promptResult ipc.RecordPromptResult
		callErr := client.Call(ctx, ipc.MethodRecordPrompt, ipc.RecordPromptParams{
			Agent:       Agent,
			SessionID:   event.SessionID,
			Tool:        event.ToolName,
			RawCommand:  event.ToolInput.Command,
			Suggestions: event.PermissionSuggestions,
		}, &promptResult)
		if callErr != nil {
			a.logger.Warn("claude: could not record the prompt", "error", callErr)
		}
	}

	response, adapterAction := MapPermissionRequest(result)
	return response, a.adapterActionReporter(result, adapterAction), nil
}

// handlePostToolUse reports the execution and, with it, any persistent consent
// the user granted in Claude's own dialog (§11.5, §19.5 path b).
func (a *Adapter) handlePostToolUse(ctx context.Context, event *Event, env adapter.IO) error {
	client, err := a.daemonClient()
	if err != nil {
		return err
	}

	var result ipc.ReportExecutionResult
	return client.Call(ctx, ipc.MethodReportExecution, ipc.ReportExecutionParams{
		Agent:           Agent,
		SessionID:       event.SessionID,
		ToolUseID:       event.ToolUseID,
		Status:          event.ExecutionStatus(),
		ResponseSummary: event.ResponseSummary(),
		AgentConsent:    a.consent(event, env),
	}, &result)
}

// handleSessionEnd reports what Intenter did across the session that is
// closing.
//
// A failure here is silent, and deliberately so. The user is shutting a
// terminal; a notice saying the daemon could not be reached would arrive too
// late to act on, at the one moment it cannot be acted on. Nothing was gated by
// this event, so nothing is lost by saying nothing.
func (a *Adapter) handleSessionEnd(ctx context.Context, event *Event) (*Response, func(context.Context), error) {
	client, err := a.daemonClient()
	if err != nil {
		a.logger.Warn("claude: no session summary", "error", err)
		return nil, nil, nil
	}

	var summary action.ActivitySummary
	if err := client.Call(ctx, ipc.MethodSummarize, ipc.SummarizeParams{
		SessionID: &event.SessionID,
	}, &summary); err != nil {
		a.logger.Warn("claude: could not summarize the session", "error", err)
		return nil, nil, nil
	}

	message := SessionSummaryMessage(summary)
	if message == "" {
		return nil, nil, nil
	}
	return &Response{SystemMessage: message}, nil, nil
}

// handleConfigChange tells the daemon its view of the agent's settings is stale.
func (a *Adapter) handleConfigChange(ctx context.Context, event *Event) error {
	client, err := a.daemonClient()
	if err != nil {
		return err
	}
	var result struct{}
	return client.Call(ctx, ipc.MethodAgentConfigChanged, ipc.AgentConfigChangedParams{
		Agent:    Agent,
		Source:   event.Source,
		FilePath: event.FilePath,
	}, &result)
}

// evaluate asks the daemon what to do, starting it lazily if it is not running.
func (a *Adapter) evaluate(ctx context.Context, event *Event, env adapter.IO) (action.EvaluationResult, error) {
	request := event.ActionRequest(ProjectHint(env.Env), a.consent(event, env), a.now())

	// Setup's self-test runs the real hook command line, so the evaluation has
	// to be a dry run: proving the integration works must not leave a decision
	// in the user's history (§12.2 step 7).
	dryRun := strings.TrimSpace(env.Lookup(EnvSelfTest)) == "1"

	result, err := a.callEvaluate(ctx, request, dryRun)
	if err == nil {
		return result, nil
	}

	if a.lazyStart != nil {
		if startErr := a.lazyStart(ctx); startErr != nil {
			a.logger.Warn("claude: could not start the daemon", "error", startErr)
			return action.EvaluationResult{}, err
		}
		return a.callEvaluate(ctx, request, dryRun)
	}
	return action.EvaluationResult{}, err
}

func (a *Adapter) callEvaluate(ctx context.Context, request action.ActionRequest, dryRun bool) (action.EvaluationResult, error) {
	client, err := a.daemonClient()
	if err != nil {
		return action.EvaluationResult{}, err
	}
	var result action.EvaluationResult
	params := ipc.EvaluateParams{DryRun: dryRun, Request: request}
	if err := client.Call(ctx, ipc.MethodEvaluate, params, &result); err != nil {
		return action.EvaluationResult{}, err
	}
	return result, nil
}

// consent computes the persistent permission Claude already holds, from its own
// settings files. The adapter never turns this into an approval itself (I-8).
func (a *Adapter) consent(event *Event, env adapter.IO) *action.AgentConsent {
	if event.ToolInput.Command == "" {
		return nil
	}
	projectDir := ProjectHint(env.Env)
	if projectDir == "" {
		projectDir = event.Cwd
	}
	return Consent(event.ToolName, event.ToolInput.Command, a.settings.Discover(projectDir))
}

// adapterActionReporter returns the work that records what the hook emitted, so
// a decision and its delivery can be told apart afterwards (§11.3, §23.2).
//
// It is best effort by design, and runs after the response has been written.
// Losing an audit annotation is a smaller harm than disturbing the session over
// one, so every failure here is logged and swallowed, and none of them can
// change what the agent was told (I-12).
func (a *Adapter) adapterActionReporter(result action.EvaluationResult, adapterAction action.AdapterAction) func(context.Context) {
	if result.AuditEventID == nil {
		// A dry run writes no audit row, so there is nothing to annotate.
		return nil
	}
	eventID := *result.AuditEventID

	return func(ctx context.Context) {
		a.logger.Debug("claude: hook decision",
			"event", eventID,
			"decision", result.Decision,
			"class", result.Class,
			"adapter_action", adapterAction)

		client, err := a.daemonClient()
		if err != nil {
			a.logger.Warn("claude: could not record the adapter action", "error", err)
			return
		}

		// The daemon answered on this socket milliseconds ago, so a round trip
		// that takes longer than this means it has gone away — and waiting out
		// the default timeouts for an annotation nobody is waiting on would
		// hold the hook process open for seconds.
		ctx, cancel := context.WithTimeout(orBackground(ctx), AdapterActionTimeout)
		defer cancel()

		var ack struct{}
		err = client.Call(ctx, ipc.MethodRecordAdapterAction, ipc.RecordAdapterActionParams{
			AuditEventID: eventID,
			Agent:        Agent,
			Action:       string(adapterAction),
		}, &ack)
		if err != nil {
			a.logger.Warn("claude: could not record the adapter action",
				"event", eventID, "error", err)
		}
	}
}

// orBackground guards against a nil context reaching the transport.
func orBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// daemonUnavailable returns the one-line notice, at most once per session per
// hour, and silence otherwise.
func (a *Adapter) daemonUnavailable(event *Event, cause error) *Response {
	a.logger.Warn("claude: daemon unavailable", "error", cause)
	if !a.shouldWarn(event.SessionID) {
		return nil
	}
	return DaemonUnavailableResponse()
}

// shouldWarn rate limits the daemon-down notice using a marker file in the
// runtime directory, because each hook is a separate short-lived process and
// has no memory of the last one.
func (a *Adapter) shouldWarn(sessionID string) bool {
	path := a.warningMarkerPath(sessionID)
	if path == "" {
		return true
	}

	if info, err := os.Stat(path); err == nil {
		if a.now().Sub(info.ModTime()) < DaemonWarningInterval {
			return false
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), platform.DirMode); err != nil {
		return true
	}
	if err := os.WriteFile(path, []byte(a.now().Format(time.RFC3339)), 0o600); err != nil {
		return true
	}
	return true
}

// warningMarkerPath names the per-session marker. The session id is hashed so a
// surprising id can never escape the runtime directory.
func (a *Adapter) warningMarkerPath(sessionID string) string {
	runtimeDir := a.platform.RuntimeDir()
	if runtimeDir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(sessionID))
	return filepath.Join(runtimeDir, "warned-"+hex.EncodeToString(sum[:8])+".json")
}

// daemonClient connects to the daemon, discovering the endpoint the same way
// the CLI does.
func (a *Adapter) daemonClient() (*ipc.Client, error) {
	if a.client != nil {
		return a.client, nil
	}
	endpoint := ipc.DiscoverEndpoint("", a.platform.DataDir(), a.platform.IPCEndpoint())
	if endpoint == "" {
		return nil, fmt.Errorf("claude: no daemon endpoint could be discovered")
	}
	return ipc.NewClient(endpoint), nil
}

// writeResponse emits exactly one JSON object followed by a newline.
func writeResponse(stdout any, response *Response) error {
	writer, ok := stdout.(interface{ Write([]byte) (int, error) })
	if !ok || writer == nil {
		return fmt.Errorf("claude: no output stream")
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("claude: encode hook response: %w", err)
	}
	if _, err := writer.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("claude: write hook response: %w", err)
	}
	return nil
}
