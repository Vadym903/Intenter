package resolver

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/parser"
)

// Limits bound one resolution (§15.1). Exceeding any of them makes the action
// UNRESOLVED rather than being decided on partial evidence.
type Limits struct {
	// MaxDepth is how many wrapper hops may be followed.
	MaxDepth int
	// MaxCommands caps the simple commands one action may contain.
	MaxCommands int
	// Budget is the wall-clock time one resolution may take.
	Budget time.Duration
}

// DefaultLimits are the values §15.1 mandates.
func DefaultLimits() Limits {
	return Limits{MaxDepth: 4, MaxCommands: parser.MaxSimpleCommands, Budget: 5 * time.Second}
}

// Resolver turns a raw command into a ResolvedAction: parse, recognize, resolve
// wrappers, normalize targets, aggregate (§15.1).
type Resolver struct {
	parsers       *parser.Registry
	recognizers   *Registry
	contexts      *ContextBuilder
	limits        Limits
	engineVersion int
	// now is injectable so the budget can be exercised in tests.
	now func() time.Time
}

// New builds a resolver with the standard parser and recognizer sets.
func New(contexts *ContextBuilder, engineVersion int) *Resolver {
	return &Resolver{
		parsers:       NewParserRegistry(),
		recognizers:   NewRecognizerRegistry(),
		contexts:      contexts,
		limits:        DefaultLimits(),
		engineVersion: engineVersion,
		now:           time.Now,
	}
}

// WithLimits returns a resolver using different limits.
func (r *Resolver) WithLimits(limits Limits) *Resolver {
	clone := *r
	clone.limits = limits
	return &clone
}

// Resolve establishes the context and resolves one request. It never returns an
// error: everything Intenter cannot model becomes a status the policy engine
// turns into ASK (INVARIANT I-2).
func (r *Resolver) Resolve(request action.ActionRequest) (*action.ResolvedAction, *Context) {
	ctx := r.contexts.Build(request.Cwd, request.ProjectHint)
	return r.ResolveInContext(request, ctx), ctx
}

// ResolveInContext resolves a request against an already established context.
func (r *Resolver) ResolveInContext(request action.ActionRequest, ctx *Context) *action.ResolvedAction {
	dialect := DialectFor(request.Dialect, action.DialectPosix)
	out := &action.ResolvedAction{
		RawCommand: request.RawCommand,
		Dialect:    dialect,
		Status:     action.StatusResolved,
	}
	if ctx != nil && ctx.Action != nil {
		out.ProjectID = ctx.Action.ProjectID
	}

	if ctx == nil || ctx.Action == nil {
		out.Status = action.StatusContextFailed
		out.StatusReason = "no request context could be established"
		return out
	}
	if ctx.Action.Status != action.ContextOK {
		out.Status = action.StatusContextFailed
		out.StatusReason = ctx.Action.StatusReason
	}

	deadline := r.now().Add(r.limits.Budget)
	run := &resolveRun{resolver: r, ctx: ctx, deadline: deadline}

	parsed := run.parse(dialect, request.RawCommand, request.Cwd)
	if parsed == nil {
		out.Status = action.WeakerStatus(out.Status, action.StatusParseFailed)
		out.StatusReason = fmt.Sprintf("no parser is available for the %s dialect", dialect)
		return out
	}
	out.Unsupported = parsed.UnsupportedSummary()
	if !parsed.OK() {
		out.Status = action.WeakerStatus(out.Status, action.StatusParseFailed)
		if out.StatusReason == "" {
			out.StatusReason = "the command uses syntax Intenter does not interpret: " +
				strings.Join(out.Unsupported, ", ")
		}
	}

	if parsed.Truncated() {
		// The parser stopped emitting commands before the end of the line, so a
		// tail Intenter never saw may hold anything (§15.1). This is not the
		// same as an unresolved command it looked at: force a prompt (R13).
		out.MarkIncomplete("the command line was too long to examine in full")
	}

	for i := range parsed.Commands {
		if run.exhausted() {
			out.Status = action.WeakerStatus(out.Status, action.StatusUnresolved)
			if out.StatusReason == "" {
				out.StatusReason = "resolution exceeded its time budget"
			}
			// Commands past this point were never examined, so the safety floor
			// cannot vouch for them: force a prompt rather than deferring (R13).
			out.MarkIncomplete("resolution exceeded its time budget before every command was examined")
			break
		}
		out.Commands = append(out.Commands, run.recognize(Request{
			Command: parsed.Commands[i],
			Context: ctx,
			Dialect: dialect,
			Script:  run.resolveScript,
		}))
	}

	r.aggregate(out)
	return out
}

// resolveRun carries the per-resolution state: the deadline and the context.
type resolveRun struct {
	resolver *Resolver
	ctx      *Context
	deadline time.Time
}

func (run *resolveRun) exhausted() bool { return run.resolver.now().After(run.deadline) }

// parse runs the dialect's parser, returning nil when the dialect has no
// implementation.
func (run *resolveRun) parse(dialect action.Dialect, command, cwd string) *parser.ParsedCommand {
	implementation, err := run.resolver.parsers.Get(dialect)
	if err != nil {
		return nil
	}
	parsed, err := implementation.Parse(parser.Input{
		Command: command,
		Cwd:     cwd,
		Home:    run.ctx.Action.HomeDir,
		TempDir: run.ctx.Action.TempDir,
	})
	if err != nil {
		// A parser reports refusals through Unsupported; an error here means an
		// internal failure, which must still not stop the gate answering.
		refused := &parser.ParsedCommand{Dialect: dialect}
		refused.AddUnsupported(parser.UnsupportedSyntaxError, 0, err.Error())
		return refused
	}
	return parsed
}

// recognize dispatches one command, guarding the depth limit.
func (run *resolveRun) recognize(req Request) action.ResolvedCommand {
	if req.Depth > run.resolver.limits.MaxDepth {
		return Unresolved(req, action.OpUnknown, fmt.Sprintf(
			"wrapper resolution went deeper than %d levels", run.resolver.limits.MaxDepth))
	}
	return run.resolver.recognizers.Recognize(req)
}

// resolveScript resolves the text of a script a wrapper executes, under every
// plausible dialect, and combines the results conservatively (I-13).
func (run *resolveRun) resolveScript(parent Request, script Script) ScriptResult {
	result := ScriptResult{Status: action.StatusResolved}

	if parent.Depth+1 > run.resolver.limits.MaxDepth {
		return ScriptResult{
			Status: action.StatusUnresolved,
			StatusReason: fmt.Sprintf("wrapper resolution went deeper than %d levels",
				run.resolver.limits.MaxDepth),
		}
	}
	if run.exhausted() {
		return ScriptResult{Status: action.StatusUnresolved, StatusReason: "resolution exceeded its time budget"}
	}

	chain := append(append([]string(nil), parent.Chain...), script.Key)

	for _, dialect := range script.Dialects {
		parsed := run.parse(dialect, script.Text, script.Cwd)
		if parsed == nil {
			result.Status = action.WeakerStatus(result.Status, action.StatusUnresolved)
			if result.StatusReason == "" {
				result.StatusReason = fmt.Sprintf("no parser is available for the %s dialect", dialect)
			}
			continue
		}
		if !parsed.OK() {
			result.Status = action.WeakerStatus(result.Status, action.StatusParseFailed)
			if result.StatusReason == "" {
				result.StatusReason = fmt.Sprintf("%s uses syntax Intenter does not interpret: %s",
					script.Label, strings.Join(parsed.UnsupportedSummary(), ", "))
			}
		}

		for i := range parsed.Commands {
			if run.exhausted() {
				result.Status = action.WeakerStatus(result.Status, action.StatusUnresolved)
				if result.StatusReason == "" {
					result.StatusReason = "resolution exceeded its time budget"
				}
				return result
			}
			child := run.recognize(Request{
				Command: parsed.Commands[i],
				Context: run.ctx,
				Dialect: dialect,
				Depth:   parent.Depth + 1,
				Chain:   chain,
				Script:  run.resolveScript,
			})
			child.ResolvedFrom = append([]string{script.Label}, child.ResolvedFrom...)
			result.Commands = append(result.Commands, child)
		}
	}
	return result
}

// aggregate folds the commands into the action: weakest status, union of
// effects and fingerprints, ordered semantic ops, explanation and action key
// (§13.6, §15.1).
func (r *Resolver) aggregate(out *action.ResolvedAction) {
	if len(out.Commands) > r.limits.MaxCommands {
		// The commands past the cap are still examined by the hard rules — they
		// stay in the slice — but the action cannot be approved, and its status
		// is UNRESOLVED. Trimming them here would hide a padded delete from the
		// safety floor, which is exactly the evasion the cap invites.
		out.Status = action.WeakerStatus(out.Status, action.StatusUnresolved)
		if out.StatusReason == "" {
			out.StatusReason = fmt.Sprintf("the command line runs more than %d commands", r.limits.MaxCommands)
		}
	}

	for i := range out.Commands {
		command := &out.Commands[i]
		out.Status = action.WeakerStatus(out.Status, command.Status)
		if out.StatusReason == "" && command.Status != action.StatusResolved {
			out.StatusReason = command.StatusReason
		}
		out.SemanticOps = append(out.SemanticOps, command.SemanticOp)
		out.Effects = append(out.Effects, command.Effects...)
		out.Fingerprints = action.MergeFingerprints(out.Fingerprints, command.Fingerprints...)
	}
	out.Effects = dedupeEffects(out.Effects)
	out.Explanation = buildExplanation(out)

	// A key is only meaningful for an action whose behavior is fully modeled;
	// an unresolved action can never be approved anyway (I-11).
	if out.Status.Approvable() {
		if key, err := action.ActionKey(out, out.ProjectID, r.engineVersion); err == nil {
			out.ActionKey = key
		}
	}
}

// buildExplanation renders the resolution chain, targets and effects in the
// deterministic form §18.4 requires.
func buildExplanation(out *action.ResolvedAction) []string {
	explanation := make([]string, 0, len(out.Commands)+2)

	for i := range out.Commands {
		command := &out.Commands[i]
		line := command.RawText
		if line == "" {
			line = command.Executable
		}
		if chain := commandChain(command); chain != "" {
			line += " -> " + chain
		}
		explanation = append(explanation, line)
	}

	if targets := out.DisplayTargets(); len(targets) > 0 {
		explanation = append(explanation, "targets: "+strings.Join(describeTargets(out), ", "))
	}
	if envelope := out.Envelope(); len(envelope) > 0 {
		entries := make([]string, 0, len(envelope))
		for _, entry := range envelope {
			entries = append(entries, entry.String())
		}
		explanation = append(explanation, "effects: "+strings.Join(entries, ", "))
	}
	if len(out.Fingerprints) > 0 {
		keys := make([]string, 0, len(out.Fingerprints))
		for _, fingerprint := range out.Fingerprints {
			keys = append(keys, fingerprint.Key)
		}
		sort.Strings(keys)
		explanation = append(explanation, "fingerprints: "+strings.Join(keys, ", "))
	}
	return explanation
}

// commandChain renders what a wrapper resolved to, e.g. "rm -rf ./dist".
func commandChain(command *action.ResolvedCommand) string {
	parts := make([]string, 0, len(command.Children))
	for i := range command.Children {
		child := &command.Children[i]
		text := child.RawText
		if text == "" {
			text = child.Executable
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "; ")
}

// describeTargets renders each target with its scope, e.g. "./dist
// (WORKSPACE_GENERATED)".
func describeTargets(out *action.ResolvedAction) []string {
	seen := make(map[string]bool)
	described := make([]string, 0)
	for _, target := range out.Targets() {
		if target.Display == "" || seen[target.Display] {
			continue
		}
		seen[target.Display] = true
		described = append(described, fmt.Sprintf("%s (%s)", target.Display, target.Scope))
	}
	sort.Strings(described)
	return described
}
