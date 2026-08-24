package parser

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
)

// Operator joins two commands in a command line. All commands of an action are
// evaluated regardless of the operator: an action's effects are the union over
// all branches (PROTOTYPE_SPEC.md §14.2).
type Operator string

const (
	OpSequence Operator = ";"
	OpAnd      Operator = "&&"
	OpOr       Operator = "||"
	OpPipe     Operator = "|"
)

// Word is one argument after quoting and supported expansion.
type Word struct {
	Text                  string `json:"text"`
	Quoted                bool   `json:"quoted,omitempty"`
	ContainsGlob          bool   `json:"contains_glob,omitempty"`
	ContainsUnexpandedVar bool   `json:"contains_unexpanded_var,omitempty"`
}

// EnvAssignment is a `KEY=value` prefix of a simple command.
type EnvAssignment struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Redirection is one input/output redirection of a simple command.
type Redirection struct {
	// Op is the redirection operator as written, e.g. ">", ">>", "<", "2>".
	Op string `json:"op"`
	// Target is the file the redirection points at; ignored devices such as
	// /dev/null and NUL are dropped by the parser.
	Target Word `json:"target"`
}

// SimpleCommand is one executable invocation with its arguments.
type SimpleCommand struct {
	Argv           []Word          `json:"argv"`
	EnvAssignments []EnvAssignment `json:"env_assignments,omitempty"`
	Redirections   []Redirection   `json:"redirections,omitempty"`
	// EffectiveCwd is the cwd this command runs in, after `cd` tracking (§14.2).
	EffectiveCwd string `json:"effective_cwd"`
	RawText      string `json:"raw_text"`
	// Operator is how this command was joined to the previous one.
	Operator Operator `json:"operator,omitempty"`
	// PipedInto is true when the command's output feeds the next stage.
	PipedInto bool `json:"piped_into,omitempty"`
	// PipedFrom is true when the command reads a previous stage's output; a
	// stage that executes streamed content becomes UNRESOLVED (§14.2).
	PipedFrom bool `json:"piped_from,omitempty"`
	// Elevated marks a command parsed out of sudo/doas/su/runas (§14.3).
	Elevated bool `json:"elevated,omitempty"`
	// Builtin marks commands handled by the parser itself, such as `cd`.
	Builtin bool `json:"builtin,omitempty"`
}

// Name is the executable name of the command, or "" when the command is empty.
func (c *SimpleCommand) Name() string {
	if len(c.Argv) == 0 {
		return ""
	}
	return c.Argv[0].Text
}

// Args returns the arguments after the executable name.
func (c *SimpleCommand) Args() []Word {
	if len(c.Argv) <= 1 {
		return nil
	}
	return c.Argv[1:]
}

// ArgTexts returns the argument texts after the executable name.
func (c *SimpleCommand) ArgTexts() []string {
	out := make([]string, 0, len(c.Argv))
	for _, word := range c.Args() {
		out = append(out, word.Text)
	}
	return out
}

// HasUnexpandedVar reports whether any word carries an unsupported variable,
// which makes its targets AMBIGUOUS (§16.1 step 1).
func (c *SimpleCommand) HasUnexpandedVar() bool {
	for _, word := range c.Argv {
		if word.ContainsUnexpandedVar {
			return true
		}
	}
	return false
}

// UnsupportedKind names a construct Intenter refuses to interpret (§14.3).
type UnsupportedKind string

const (
	UnsupportedCommandSubstitution UnsupportedKind = "command_substitution"
	UnsupportedProcessSubstitution UnsupportedKind = "process_substitution"
	UnsupportedArithmetic          UnsupportedKind = "arithmetic_expansion"
	UnsupportedControlFlow         UnsupportedKind = "control_flow"
	UnsupportedFunction            UnsupportedKind = "function_definition"
	UnsupportedEval                UnsupportedKind = "eval"
	UnsupportedSource              UnsupportedKind = "source"
	UnsupportedExec                UnsupportedKind = "exec"
	UnsupportedXargs               UnsupportedKind = "xargs"
	UnsupportedAlias               UnsupportedKind = "alias"
	UnsupportedExport              UnsupportedKind = "export"
	UnsupportedDirStack            UnsupportedKind = "dir_stack"
	UnsupportedTrap                UnsupportedKind = "trap"
	UnsupportedBackground          UnsupportedKind = "background"
	UnsupportedScriptBlock         UnsupportedKind = "script_block"
	UnsupportedInvokeExpression    UnsupportedKind = "invoke_expression"
	UnsupportedStartProcess        UnsupportedKind = "start_process"
	UnsupportedShellWrapper        UnsupportedKind = "shell_command_wrapper"
	UnsupportedElevation           UnsupportedKind = "elevation"
	UnsupportedSyntaxError         UnsupportedKind = "syntax_error"
	UnsupportedNode                UnsupportedKind = "unsupported_node"
	UnsupportedRedirection         UnsupportedKind = "unsupported_redirection"
	UnsupportedCdDash              UnsupportedKind = "cd_dash"
	UnsupportedTooManyCommands     UnsupportedKind = "too_many_commands"
	UnsupportedCommandTooLong      UnsupportedKind = "command_too_long"
	// UnsupportedTruncated means commands beyond MaxParsedCommands were not
	// emitted at all: the tail of the line is unseen, not merely unresolved.
	UnsupportedTruncated UnsupportedKind = "truncated"
)

// UnsupportedConstruct records where and why parsing gave up on a construct.
type UnsupportedConstruct struct {
	Kind     UnsupportedKind `json:"kind"`
	Position int             `json:"position"`
	Text     string          `json:"text"`
}

// String renders an unsupported construct for explanations.
func (u UnsupportedConstruct) String() string {
	if u.Text == "" {
		return string(u.Kind)
	}
	return fmt.Sprintf("%s (%s)", u.Kind, u.Text)
}

// ParsedCommand is the shared parser output for every dialect (§13.3).
type ParsedCommand struct {
	Dialect     action.Dialect         `json:"dialect"`
	Commands    []SimpleCommand        `json:"commands"`
	Operators   []Operator             `json:"operators,omitempty"`
	Unsupported []UnsupportedConstruct `json:"unsupported,omitempty"`
	// FinalCwd is the cwd after the last `cd` of the line.
	FinalCwd string `json:"final_cwd,omitempty"`
}

// OK reports whether the whole command line was understood. Any unsupported
// construct makes the action PARSE_FAILED (INVARIANT I-2).
func (p *ParsedCommand) OK() bool { return len(p.Unsupported) == 0 }

// Truncated reports whether the parser stopped emitting commands before the end
// of the line, so part of it was never examined. This is stronger than "too
// long to approve": the tail is unseen, so the resolver marks the action
// incomplete and the safety floor forces a prompt (§15.1, hard rule R13).
func (p *ParsedCommand) Truncated() bool {
	for _, item := range p.Unsupported {
		if item.Kind == UnsupportedTruncated {
			return true
		}
	}
	return false
}

// UnsupportedSummary renders the unsupported constructs for the audit log.
func (p *ParsedCommand) UnsupportedSummary() []string {
	out := make([]string, 0, len(p.Unsupported))
	for _, item := range p.Unsupported {
		out = append(out, item.String())
	}
	return out
}

// AddUnsupported records a construct Intenter will not interpret.
func (p *ParsedCommand) AddUnsupported(kind UnsupportedKind, position int, text string) {
	p.Unsupported = append(p.Unsupported, UnsupportedConstruct{Kind: kind, Position: position, Text: text})
}

// Dialect parses one shell syntax into the shared model. Implementations are
// pure functions of their input and MUST NOT execute anything (§14.1).
type Dialect interface {
	// Name is the dialect identifier used in requests and approvals.
	Name() action.Dialect
	// Parse converts a command line into the shared model. It returns an error
	// only for internal failures; refused constructs are reported through
	// ParsedCommand.Unsupported so hard rules can still run over what parsed.
	Parse(input Input) (*ParsedCommand, error)
}

// Input is everything a parser is allowed to know.
type Input struct {
	// Command is the raw command line.
	Command string
	// Cwd is the working directory the first command runs in.
	Cwd string
	// Home is the user's home directory, for ~ and $HOME expansion.
	Home string
	// TempDir is the platform temp directory, for $TMPDIR/%TEMP% expansion.
	TempDir string
	// Env supplies additional variables a dialect may expand; anything absent
	// makes the word ambiguous rather than being guessed.
	Env map[string]string
}

// Registry maps dialect names to implementations (§14.4).
type Registry struct {
	dialects map[action.Dialect]Dialect
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{dialects: make(map[action.Dialect]Dialect)}
}

// Register adds a dialect, replacing any previous registration.
func (r *Registry) Register(dialect Dialect) {
	r.dialects[dialect.Name()] = dialect
}

// Get returns the dialect, or an error naming the known ones.
func (r *Registry) Get(name action.Dialect) (Dialect, error) {
	dialect, ok := r.dialects[name]
	if !ok {
		return nil, fmt.Errorf("parser: unknown dialect %q (known: %s)", name, strings.Join(r.Names(), ", "))
	}
	return dialect, nil
}

// Names lists the registered dialect names, sorted.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.dialects))
	for name := range r.dialects {
		out = append(out, string(name))
	}
	sort.Strings(out)
	return out
}

// MaxCommandBytes is the largest command line the parsers accept (§15.1).
const MaxCommandBytes = 64 * 1024

// MaxSimpleCommands caps how many simple commands one action may contain and
// still be approvable (§15.1). Exceeding it makes the action UNRESOLVED — but
// every command is still emitted, so the hard rules run over all of them. A
// parser that dropped the tail would let `true; …(32×); rm -rf ~` hide its
// delete from the safety floor.
const MaxSimpleCommands = 32

// MaxParsedCommands is the hard cap on commands the walkers emit, a memory
// bound rather than a policy. It cannot be reached under MaxCommandBytes (the
// shortest command line form is two bytes per command); should a walker ever hit
// it, it reports UnsupportedTruncated so the resolver knows the tail is unseen.
const MaxParsedCommands = 1 << 16
