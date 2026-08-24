package resolver

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/parser"
	"github.com/Vadym903/Intenter/internal/scope"
)

// Request is everything a recognizer is given about one simple command
// (PROTOTYPE_SPEC.md §15.3).
type Request struct {
	Command parser.SimpleCommand
	Context *Context
	Dialect action.Dialect
	// Depth is the wrapper resolution depth of this command; the pipeline
	// refuses to go deeper than §15.1 allows.
	Depth int
	// Chain is the wrapper chain that led here, e.g. ["npm run cleanup"]. It
	// feeds the explanation and detects scripts that call each other in a loop.
	Chain []string
	// Script resolves the text of a script a wrapper would execute. The
	// pipeline supplies it, so recognizers never parse text themselves; a nil
	// value leaves every wrapper UNRESOLVED.
	Script ScriptResolver
}

// ScriptResolver resolves one script text a wrapper executes (§15.5). It
// returns the commands the script runs, already recognized.
type ScriptResolver func(req Request, script Script) ScriptResult

// Script is one script text a wrapper would run.
type Script struct {
	// Text is the command line the wrapper executes.
	Text string
	// Cwd is the directory the script runs in.
	Cwd string
	// Dialects are the shells the script may run under. When more than one is
	// plausible every one is evaluated and the effects are combined (I-13).
	Dialects []action.Dialect
	// Label names the script in the resolution chain, e.g. "npm run cleanup".
	Label string
	// Key identifies the script for cycle detection.
	Key string
}

// ScriptResult is what a script resolved to.
type ScriptResult struct {
	Commands []action.ResolvedCommand
	// Status is the weakest status over every dialect and command.
	Status action.ResolutionStatus
	// StatusReason explains a non-RESOLVED status.
	StatusReason string
}

// ResolveScript runs the pipeline's script resolver, reporting a clear refusal
// when no resolver was supplied.
func (r Request) ResolveScript(script Script) ScriptResult {
	if r.Script == nil {
		return ScriptResult{
			Status:       action.StatusUnresolved,
			StatusReason: "script resolution is unavailable",
		}
	}
	return r.Script(r, script)
}

// InChain reports whether a script is already being resolved, which means the
// scripts call each other in a loop (§15.5.1 step 4).
func (r Request) InChain(key string) bool {
	for _, seen := range r.Chain {
		if seen == key {
			return true
		}
	}
	return false
}

// Recognizer models one executable, or one family of executables, and turns a
// parsed command into effects. Implementations MUST default to UNRESOLVED for
// arguments outside their grammar (§15.3).
type Recognizer interface {
	// Names lists the executable names this recognizer claims, without any
	// directory prefix or Windows extension.
	Names() []string
	// Recognize converts one simple command. It reports its own resolution
	// status rather than an error, so an unrecognized invocation degrades to
	// UNRESOLVED instead of failing the request (INVARIANT I-2).
	Recognize(req Request) action.ResolvedCommand
}

// Registry dispatches simple commands to recognizers by executable name and
// applies the rules that hold for every command regardless of the executable.
type Registry struct {
	byName map[string]Recognizer
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{byName: make(map[string]Recognizer)}
}

// Register adds a recognizer under each of its names.
func (r *Registry) Register(recognizers ...Recognizer) {
	for _, recognizer := range recognizers {
		for _, name := range recognizer.Names() {
			r.byName[strings.ToLower(name)] = recognizer
		}
	}
}

// Names lists the registered executable names, sorted.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.byName))
	for name := range r.byName {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Lookup finds the recognizer for an executable as written, matching on the
// base name so `/bin/rm`, `rm` and `rm.exe` reach the same recognizer.
func (r *Registry) Lookup(executable string) (Recognizer, bool) {
	if executable == "" {
		return nil, false
	}
	for _, candidate := range executableKeys(executable) {
		if recognizer, ok := r.byName[candidate]; ok {
			return recognizer, true
		}
	}
	return nil, false
}

// Recognize resolves one simple command. Dispatch happens by executable name;
// the rules that apply to every command — redirections, elevation, a pipeline
// stage that executes streamed content, and environment overrides that change
// what runs — are applied here so no recognizer can forget them.
func (r *Registry) Recognize(req Request) action.ResolvedCommand {
	out := r.dispatch(req)
	out.Executable = req.Command.Name()
	if out.RawText == "" {
		out.RawText = req.Command.RawText
	}

	r.applyRedirections(&out, req)
	applyEnvironmentOverrides(&out, req)
	applyStreamedInput(&out, req)
	applyElevation(&out, req)

	out.Effects = dedupeEffects(out.Effects)
	return out
}

// dispatch routes to the recognizer for the executable, or reports an unknown
// program (§15.4: anything not listed is UNRESOLVED).
func (r *Registry) dispatch(req Request) action.ResolvedCommand {
	name := req.Command.Name()
	recognizer, ok := r.Lookup(name)
	if !ok {
		return Unresolved(req, action.OpUnknown,
			fmt.Sprintf("%s is not a program Intenter models", displayName(name)))
	}
	return recognizer.Recognize(req)
}

// applyRedirections adds the effects of a command's shell redirections. They are
// independent of the executable, so `unknown-tool > ~/.ssh/authorized_keys`
// still produces the write (§14.2).
func (r *Registry) applyRedirections(out *action.ResolvedCommand, req Request) {
	for _, redirection := range req.Command.Redirections {
		for _, target := range req.TargetsFor(redirection.Target) {
			pinned := target
			switch {
			case strings.HasSuffix(redirection.Op, "<"):
				out.Effects = append(out.Effects, action.Effect{Type: action.EffectRead, Target: &pinned})
			case strings.HasSuffix(redirection.Op, ">>"):
				out.Effects = append(out.Effects, action.Effect{Type: action.EffectWrite, Target: &pinned})
			default:
				out.Effects = append(out.Effects,
					action.Effect{Type: action.EffectCreate, Target: &pinned},
					action.Effect{Type: action.EffectWrite, Target: &pinned})
			}
			out.Targets = append(out.Targets, pinned)
		}
	}
}

// applyEnvironmentOverrides degrades a command whose environment prefix can
// change what the name resolves to or how it behaves (§14.2).
func applyEnvironmentOverrides(out *action.ResolvedCommand, req Request) {
	for _, assignment := range req.Command.EnvAssignments {
		if !parser.IsDangerousEnvAssignment(assignment.Name) {
			continue
		}
		degrade(out, fmt.Sprintf("the %s environment override changes what this command does", assignment.Name))
		return
	}
}

// applyStreamedInput marks a pipeline stage that executes whatever it is fed
// (`curl … | sh`). The content is never known, so the stage executes an
// unresolved program (§14.2, hard rule R12).
func applyStreamedInput(out *action.ResolvedCommand, req Request) {
	if !req.Command.PipedFrom || !parser.IsStreamInterpreter(req.Command.Name()) {
		return
	}
	name := displayName(req.Command.Name())

	// An unknown executable already produced an EXECUTE effect for this
	// program; marking that one keeps a single effect and stops the marker from
	// being lost to deduplication, which would silently disable R12.
	marked := false
	for i := range out.Effects {
		effect := &out.Effects[i]
		if effect.Type == action.EffectExecute && effect.Program != nil && effect.Program.Name == name {
			effect.Program.Streamed = true
			marked = true
		}
	}
	if !marked {
		out.Effects = append(out.Effects, action.Effect{
			Type:    action.EffectExecute,
			Program: &action.ProgramRef{Name: name, Resolution: action.ProgramUnresolved, Streamed: true},
		})
	}
	degrade(out, fmt.Sprintf("%s executes whatever the previous command writes to it", name))
}

// applyElevation flags every effect of a command that runs with raised
// privileges and refuses to resolve it (§15.2).
func applyElevation(out *action.ResolvedCommand, req Request) {
	if !req.Command.Elevated {
		return
	}
	for i := range out.Effects {
		out.Effects[i].AddFlags(action.EffectFlagElevated)
		if out.Effects[i].Program != nil {
			out.Effects[i].Program.Elevated = true
		}
	}
	degrade(out, "the command runs with elevated privileges")
}

// degrade weakens a command to UNRESOLVED, keeping the first reason recorded so
// explanations name the root cause rather than the last check that fired.
func degrade(out *action.ResolvedCommand, reason string) {
	out.Status = action.WeakerStatus(out.Status, action.StatusUnresolved)
	if out.StatusReason == "" {
		out.StatusReason = reason
	}
}

// Unresolved builds a command Intenter declines to model.
func Unresolved(req Request, op action.SemanticOp, reason string) action.ResolvedCommand {
	return action.ResolvedCommand{
		Executable:   req.Command.Name(),
		SemanticOp:   op,
		Status:       action.StatusUnresolved,
		StatusReason: reason,
		RawText:      req.Command.RawText,
		Effects: []action.Effect{{
			Type:    action.EffectExecute,
			Program: &action.ProgramRef{Name: displayName(req.Command.Name()), Resolution: action.ProgramUnresolved},
		}},
	}
}

// TargetsFor normalizes one word into targets, including the extra targets a
// wildcard can reach outside its own directory (§16.1 step 7).
func (r Request) TargetsFor(word parser.Word) []action.Target {
	if r.Context == nil || r.Context.Scope == nil {
		return nil
	}
	return r.Context.Scope.NormalizeWord(r.scopeInput(word))
}

// TargetFor normalizes one word into a single target, used where a wildcard
// cannot apply, such as a copy destination.
func (r Request) TargetFor(word parser.Word) (action.Target, bool) {
	if r.Context == nil || r.Context.Scope == nil {
		return action.Target{}, false
	}
	return r.Context.Scope.Normalize(r.scopeInput(word)), true
}

// PathTarget normalizes a path Intenter derived itself, such as a repository
// directory, rather than a word the agent wrote.
func (r Request) PathTarget(path string) (action.Target, bool) {
	if r.Context == nil || r.Context.Scope == nil || path == "" {
		return action.Target{}, false
	}
	return r.Context.Scope.Normalize(scope.Input{
		Raw:          path,
		Text:         path,
		Cwd:          r.Command.EffectiveCwd,
		WindowsStyle: r.windowsPaths(),
	}), true
}

func (r Request) scopeInput(word parser.Word) scope.Input {
	return scope.Input{
		Raw:          word.Text,
		Text:         word.Text,
		Cwd:          r.Command.EffectiveCwd,
		Ambiguous:    word.ContainsUnexpandedVar,
		Glob:         word.ContainsGlob,
		WindowsStyle: r.windowsPaths(),
	}
}

// windowsPaths reports whether backslash separators, drive letters, MSYS paths
// and UNC forms must be understood. The Windows dialects always use them; a
// POSIX shell needs them too when it runs on Windows, where Git Bash writes
// `/c/Users/…` for a Windows path (§16.1 step 2).
func (r Request) windowsPaths() bool {
	if r.Dialect == action.DialectPowerShell || r.Dialect == action.DialectCmd {
		return true
	}
	return r.Context != nil && r.Context.Action != nil && r.Context.Action.Platform == "windows"
}

// Workspace returns the workspace root, or "" when none was established.
func (r Request) Workspace() string {
	if r.Context == nil || r.Context.Action == nil {
		return ""
	}
	return r.Context.Action.WorkspaceRoot
}

// ArgClass is how a recognizer's grammar treats one option (§15.3). UNKNOWN is
// the default for anything the grammar does not list, and makes the command
// UNRESOLVED.
type ArgClass string

const (
	ArgSafe     ArgClass = "SAFE"
	ArgSemantic ArgClass = "SEMANTIC"
	ArgUnknown  ArgClass = "UNKNOWN"
)

// Grammar is one recognizer's option table. Implementers may extend the safe
// lists but MUST keep UNKNOWN as the default (§15.3).
type Grammar struct {
	// Safe options never change the operation, targets or flags.
	Safe []string
	// SafeValue options are safe and consume the following word.
	SafeValue []string
	// SafeOptionalValue options are safe and take a value only in the inline
	// `--name=value` form, as `--color[=WHEN]` does. Listing them separately
	// keeps a bare `--color` from swallowing the path that follows it.
	SafeOptionalValue []string
	// Semantic options change the operation, targets or flags.
	Semantic []string
	// SemanticValue options change meaning and consume the following word.
	SemanticValue []string
	// SemanticOptionalValue options change meaning and take a value only in
	// the inline form, as `--force-with-lease[=<ref>]` does.
	SemanticOptionalValue []string
	// SafeNumericShort admits `-5`-style count options, which several
	// read-only git subcommands accept.
	SafeNumericShort bool
	// SlashSwitches enables cmd.exe's `/x` option form, matched
	// case-insensitively. It is opt-in because a leading `/` is an ordinary
	// absolute path in every other dialect — and the cmd dialect is parsed on
	// POSIX hosts too, for the dual-dialect union of §15.5.4.
	SlashSwitches bool
	// SafePrefixes admits option families such as "--reporter" whose members
	// are all read-only, including the "--reporter=x" form.
	SafePrefixes []string
	// Cluster enables POSIX short-option clustering, so `-rf` reads as `-r -f`.
	// Tools whose single-dash options are words, such as find, leave it off.
	Cluster bool
	// PermissiveUnknown accepts unlisted options as SAFE. It exists for tools
	// that are read-only by construction, where the argument grammar cannot
	// widen the effects (§15.4, grep).
	PermissiveUnknown bool

	compiled map[string]argSpec
}

type argSpec struct {
	class ArgClass
	// takesArgs marks an option that consumes the following word when no
	// inline value is given.
	takesArgs bool
	// inlineOnly marks an option whose value may only be written inline.
	inlineOnly bool
	// listed distinguishes an option the grammar declares from one admitted by
	// a safe prefix or a permissive grammar.
	listed bool
}

// Args is the result of scanning a command's arguments against a grammar.
type Args struct {
	// Operands are the positional words, in order.
	Operands []parser.Word
	// Options maps each recognized option that was present to its value; the
	// value is the zero Word for boolean options. A repeated option keeps its
	// last value here; every value is in Values.
	Options map[string]parser.Word
	// Values records every value a repeatable value-taking option was given, in
	// order — `-H a -H b` yields both, where Options keeps only b.
	Values map[string][]parser.Word
	// Unknown lists options outside the grammar. Any entry makes the command
	// UNRESOLVED (§15.3).
	Unknown []string
	// leadingOperands counts the operands that appeared before the first
	// option, which is how tools like find separate paths from predicates.
	leadingOperands int
}

// setValue records an option's value under both indexes.
func (a *Args) setValue(name string, value parser.Word) {
	a.Options[name] = value
	if a.Values == nil {
		a.Values = make(map[string][]parser.Word)
	}
	a.Values[name] = append(a.Values[name], value)
}

// All returns every value the named options were given, in command order.
func (a Args) All(names ...string) []parser.Word {
	var out []parser.Word
	for _, name := range names {
		out = append(out, a.Values[name]...)
	}
	return out
}

// OK reports whether every option was inside the grammar.
func (a Args) OK() bool { return len(a.Unknown) == 0 }

// Has reports whether an option was present.
func (a Args) Has(name string) bool {
	_, ok := a.Options[name]
	return ok
}

// HasAny reports whether at least one of the options was present.
func (a Args) HasAny(names ...string) bool {
	for _, name := range names {
		if a.Has(name) {
			return true
		}
	}
	return false
}

// Value returns the word an option consumed, or the zero Word.
func (a Args) Value(name string) parser.Word { return a.Options[name] }

// LeadingOperands returns the operands that appeared before the first option.
func (a Args) LeadingOperands() []parser.Word { return a.Operands[:a.leadingOperands] }

// UnknownReason renders the refused options for an explanation.
func (a Args) UnknownReason(executable string) string {
	return fmt.Sprintf("%s was called with %s, which Intenter does not model",
		displayName(executable), strings.Join(a.Unknown, " "))
}

// compile builds the option index on first use.
func (g *Grammar) compile() {
	if g.compiled != nil {
		return
	}
	g.compiled = make(map[string]argSpec)
	for _, name := range g.Safe {
		g.compiled[name] = argSpec{class: ArgSafe, listed: true}
	}
	for _, name := range g.SafeOptionalValue {
		g.compiled[name] = argSpec{class: ArgSafe, inlineOnly: true, listed: true}
	}
	for _, name := range g.SafeValue {
		g.compiled[name] = argSpec{class: ArgSafe, takesArgs: true, listed: true}
	}
	for _, name := range g.Semantic {
		g.compiled[name] = argSpec{class: ArgSemantic, listed: true}
	}
	for _, name := range g.SemanticOptionalValue {
		g.compiled[name] = argSpec{class: ArgSemantic, inlineOnly: true, listed: true}
	}
	for _, name := range g.SemanticValue {
		g.compiled[name] = argSpec{class: ArgSemantic, takesArgs: true, listed: true}
	}
}

// lookup finds an option's spec, normalizing the case of a cmd-style switch.
func (g *Grammar) lookup(option string) (argSpec, bool) {
	spec, _, ok := g.resolveOption(option)
	return spec, ok
}

// resolveOption finds an option's spec and the name it is recorded under, so a
// recognizer asking for `/s` finds a switch the user wrote as `/S`.
func (g *Grammar) resolveOption(option string) (argSpec, string, bool) {
	g.compile()
	if spec, ok := g.compiled[option]; ok {
		return spec, option, true
	}
	if g.SlashSwitches && strings.HasPrefix(option, "/") {
		lowered := strings.ToLower(option)
		if spec, ok := g.compiled[lowered]; ok {
			return spec, lowered, true
		}
	}
	return argSpec{}, option, false
}

// isOption reports whether a word is written as an option in this grammar. A
// bare "-" means standard input for the tools Intenter models, so it stays an
// operand.
func (g *Grammar) isOption(text string) bool {
	if len(text) < 2 {
		return false
	}
	if strings.HasPrefix(text, "-") {
		return true
	}
	// Only a short, separator-free token is a cmd switch; `/usr/bin` is a path.
	return g.SlashSwitches && text[0] == '/' &&
		!strings.ContainsAny(text[1:], `/\`) && len(text) <= 8
}

// Classify reports how the grammar treats one option.
func (g *Grammar) Classify(option string) ArgClass {
	g.compile()
	if spec, ok := g.lookup(option); ok {
		return spec.class
	}
	for _, prefix := range g.SafePrefixes {
		if strings.HasPrefix(option, prefix) {
			return ArgSafe
		}
	}
	if g.SafeNumericShort && isNumericShortOption(option) {
		return ArgSafe
	}
	if g.PermissiveUnknown {
		return ArgSafe
	}
	return ArgUnknown
}

// isNumericShortOption reports whether an option is the `-5` count form.
func isNumericShortOption(option string) bool {
	if len(option) < 2 || option[0] != '-' {
		return false
	}
	for i := 1; i < len(option); i++ {
		if option[i] < '0' || option[i] > '9' {
			return false
		}
	}
	return true
}

// Scan walks a command's arguments, separating operands from options and
// collecting the options the grammar refuses.
func (g *Grammar) Scan(args []parser.Word) Args {
	g.compile()

	out := Args{Options: make(map[string]parser.Word)}
	sawOption := false
	endOfOptions := false

	for i := 0; i < len(args); i++ {
		word := args[i]

		if endOfOptions || !g.isOption(word.Text) {
			if !sawOption {
				out.leadingOperands++
			}
			out.Operands = append(out.Operands, word)
			continue
		}
		sawOption = true

		if word.Text == "--" {
			endOfOptions = true
			continue
		}
		if strings.HasPrefix(word.Text, "--") || !g.Cluster {
			i += g.scanLongOption(word, args, i, &out)
			continue
		}
		i += g.scanCluster(word, args, i, &out)
	}
	return out
}

// scanLongOption handles a `--name`, `--name=value` or single-dash word option,
// returning how many extra words it consumed.
func (g *Grammar) scanLongOption(word parser.Word, args []parser.Word, i int, out *Args) int {
	name, inline, hasInline := strings.Cut(word.Text, "=")

	spec, name, known := g.resolveOption(name)
	if !known {
		if g.Classify(name) != ArgSafe {
			out.Unknown = append(out.Unknown, name)
			return 0
		}
		spec = argSpec{class: ArgSafe}
	}

	if hasInline {
		// A grammar that declares an option boolean means it: `--recursive=1`
		// is refused rather than guessed at. An option admitted by a safe
		// prefix or a permissive grammar carries no such claim.
		if spec.listed && !spec.takesArgs && !spec.inlineOnly {
			out.Unknown = append(out.Unknown, word.Text)
			return 0
		}
		out.setValue(name, parser.Word{
			Text:                  inline,
			Quoted:                word.Quoted,
			ContainsGlob:          parser.ContainsGlob(inline),
			ContainsUnexpandedVar: word.ContainsUnexpandedVar,
		})
		return 0
	}

	if !spec.takesArgs {
		out.Options[name] = parser.Word{}
		return 0
	}
	if i+1 >= len(args) {
		out.Unknown = append(out.Unknown, name)
		return 0
	}
	out.setValue(name, args[i+1])
	return 1
}

// scanCluster expands a POSIX short-option cluster such as `-rf`, returning how
// many extra words it consumed.
func (g *Grammar) scanCluster(word parser.Word, args []parser.Word, i int, out *Args) int {
	letters := word.Text[1:]

	// `-r=1` is not a form clustered short options take. Refusing the word as
	// written explains the refusal better than reporting `-=` and `-1`.
	if strings.Contains(letters, "=") {
		out.Unknown = append(out.Unknown, word.Text)
		return 0
	}
	for j := 0; j < len(letters); j++ {
		option := "-" + string(letters[j])
		spec, known := g.lookup(option)
		if !known {
			if g.Classify(option) != ArgSafe {
				out.Unknown = append(out.Unknown, option)
				continue
			}
			spec = argSpec{class: ArgSafe}
		}
		if !spec.takesArgs {
			out.Options[option] = parser.Word{}
			continue
		}
		// A value-taking letter takes the rest of the cluster, or the next word.
		if rest := letters[j+1:]; rest != "" {
			out.setValue(option, parser.Word{
				Text:                  rest,
				ContainsGlob:          parser.ContainsGlob(rest),
				ContainsUnexpandedVar: word.ContainsUnexpandedVar,
			})
			return 0
		}
		if i+1 >= len(args) {
			out.Unknown = append(out.Unknown, option)
			return 0
		}
		out.setValue(option, args[i+1])
		return 1
	}
	return 0
}

// isOption reports whether a word is written as an option. A bare "-" means
// standard input for the tools Intenter models, so it stays an operand.
func isOption(text string) bool {
	return len(text) > 1 && strings.HasPrefix(text, "-")
}

// executableKeys lists the lookup keys for an executable as written, most
// specific first.
func executableKeys(executable string) []string {
	lowered := strings.ToLower(executable)
	keys := []string{lowered}

	base := path.Base(strings.ReplaceAll(lowered, `\`, "/"))
	if base != lowered {
		keys = append(keys, base)
	}
	for _, extension := range []string{".exe", ".cmd", ".bat", ".ps1"} {
		if trimmed := strings.TrimSuffix(base, extension); trimmed != base {
			keys = append(keys, trimmed)
			break
		}
	}
	return keys
}

// displayName renders an executable for explanations, keeping the form the
// agent wrote so `./gradlew` does not read as `gradlew`.
func displayName(executable string) string {
	if executable == "" {
		return "the command"
	}
	return executable
}

// dedupeEffects removes effects that are identical in type, flags and target,
// which redirection and recognizer output can otherwise produce twice.
func dedupeEffects(effects []action.Effect) []action.Effect {
	if len(effects) < 2 {
		return effects
	}
	seen := make(map[string]bool, len(effects))
	out := effects[:0]
	for _, effect := range effects {
		key := effectKey(effect)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, effect)
	}
	return out
}

func effectKey(effect action.Effect) string {
	key := effect.EnvelopeEntry().Key()
	if effect.Target != nil {
		key += "@" + effect.Target.Canonical
	}
	if effect.Network != nil {
		key += "@" + effect.Network.Key()
	}
	if effect.Program != nil {
		key += "@" + effect.Program.Name
		// Whether the program's input is piped decides hard rule R12, so two
		// executions that differ only in that are not the same effect.
		if effect.Program.Streamed {
			key += "@streamed"
		}
	}
	return key
}
