package posix

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/parser"
)

// Dialect parses POSIX/bash command lines into the shared parser model. It is a
// pure function of its input: nothing is executed and no file is read
// (PROTOTYPE_SPEC.md §14.1).
type Dialect struct{}

// New builds the POSIX dialect.
func New() Dialect { return Dialect{} }

// Name identifies the dialect in requests and approvals.
func (Dialect) Name() action.Dialect { return action.DialectPosix }

// Parse walks the shell AST with a node whitelist. Constructs outside the
// whitelist are reported through ParsedCommand.Unsupported rather than as an
// error, so hard rules still run over whatever did parse (INVARIANT I-2).
//
// Environment prefixes are recorded verbatim; mapping a dangerous override such
// as PATH= onto status UNRESOLVED is the resolver's job (§14.2), because status
// is a resolution property and the parser only reports syntax.
func (d Dialect) Parse(in parser.Input) (*parser.ParsedCommand, error) {
	out := &parser.ParsedCommand{Dialect: action.DialectPosix, FinalCwd: in.Cwd}

	if len(in.Command) > parser.MaxCommandBytes {
		out.AddUnsupported(parser.UnsupportedCommandTooLong, 0,
			fmt.Sprintf("%d bytes exceeds the %d byte limit", len(in.Command), parser.MaxCommandBytes))
		return out, nil
	}

	file, err := parseShell(in.Command)
	if err != nil {
		// Reading from a string never fails, so every error here is the shell
		// grammar rejecting the input. Reporting it as refused syntax rather
		// than an internal failure keeps the decision at ASK (INVARIANT I-2).
		position, text := parseFailure(err)
		out.AddUnsupported(parser.UnsupportedSyntaxError, position, text)
		return out, nil
	}

	w := &walker{out: out, vars: parser.FromInput(in), src: in.Command, cwd: in.Cwd}
	w.stmts(file.Stmts, "")
	out.FinalCwd = w.cwd
	return out, nil
}

// parseShell runs the third-party shell grammar over a command line.
//
// mvdan.cc/sh v3.8.0 panics on some malformed inputs (for example a backtick
// followed by `$\` and a NUL byte), and a command line is attacker-influenced
// text. A crash inside the grammar is turned into an ordinary rejection here so
// the gate degrades to ASK instead of unwinding (INVARIANT I-2). The recover is
// deliberately scoped to the library call: a panic in Intenter's own walker
// stays loud in tests and is caught by the daemon's handler recovery.
func parseShell(command string) (file *syntax.File, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			file, err = nil, fmt.Errorf("shell grammar failed: %v", recovered)
		}
	}()
	return syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
}

// parseFailure describes a rejected command line: its byte offset and a short
// reason. Both mvdan.cc/sh error types carry a position; anything else falls
// back to the start of the line.
func parseFailure(err error) (int, string) {
	var parseErr syntax.ParseError
	if errors.As(err, &parseErr) {
		return int(parseErr.Pos.Offset()), parseErr.Text
	}
	var langErr syntax.LangError
	if errors.As(err, &langErr) {
		return int(langErr.Pos.Offset()), langErr.Feature
	}
	return 0, truncate(err.Error())
}

// walker turns the shell AST into the flat command list of the shared model.
type walker struct {
	out  *parser.ParsedCommand
	vars parser.VarContext
	src  string
	// cwd is the effective working directory, updated by `cd` (§14.2).
	cwd string
	// pendingOp joins the next emitted command to the previous one.
	pendingOp parser.Operator
	// truncated records that the command limit was already reported.
	truncated bool
	// hardCapped records that the memory bound was already reported.
	hardCapped bool
}

// stmts walks a statement list; the first statement inherits the operator that
// joined the enclosing construct to what came before it.
func (w *walker) stmts(list []*syntax.Stmt, op parser.Operator) {
	for i, stmt := range list {
		if i == 0 {
			w.stmt(stmt, op)
			continue
		}
		w.stmt(stmt, parser.OpSequence)
	}
}

// stmt dispatches one statement on the node whitelist. op is how the statement
// is joined to the previous command.
func (w *walker) stmt(stmt *syntax.Stmt, op parser.Operator) {
	if stmt == nil || stmt.Cmd == nil {
		return
	}

	// A backgrounded statement outlives the request, so its effects can no
	// longer be attributed to this decision (§14.3).
	if stmt.Background {
		w.unsupported(parser.UnsupportedBackground, stmt, "&")
	}

	switch cmd := stmt.Cmd.(type) {
	case *syntax.CallExpr:
		w.pendingOp = op
		w.call(cmd, stmt)

	case *syntax.BinaryCmd:
		before := len(w.out.Commands)
		w.stmt(cmd.X, op)
		mid := len(w.out.Commands)
		w.stmt(cmd.Y, binaryOperator(cmd.Op))
		after := len(w.out.Commands)
		if isPipe(cmd.Op) && mid > before && after > mid {
			w.out.Commands[mid-1].PipedInto = true
			w.out.Commands[mid].PipedFrom = true
		}

	case *syntax.Block:
		w.redirsUnsupported(stmt)
		w.stmts(cmd.Stmts, op)

	case *syntax.Subshell:
		w.redirsUnsupported(stmt)
		w.stmts(cmd.Stmts, op)

	case *syntax.IfClause, *syntax.ForClause, *syntax.WhileClause, *syntax.CaseClause:
		w.unsupported(parser.UnsupportedControlFlow, stmt, w.text(stmt))

	case *syntax.FuncDecl:
		w.unsupported(parser.UnsupportedFunction, stmt, w.text(stmt))

	case *syntax.DeclClause:
		w.unsupported(parser.UnsupportedExport, stmt, declVariant(cmd))

	case *syntax.LetClause, *syntax.ArithmCmd:
		w.unsupported(parser.UnsupportedArithmetic, stmt, w.text(stmt))

	default:
		w.unsupported(parser.UnsupportedNode, stmt, w.text(stmt))
	}
}

// call converts a simple command, then hands it to dispatch for the name-based
// rules (elevation, shell wrappers, refused builtins, `cd`).
func (w *walker) call(call *syntax.CallExpr, stmt *syntax.Stmt) {
	cmd := parser.SimpleCommand{EffectiveCwd: w.cwd, RawText: w.text(stmt)}

	for _, assign := range call.Assigns {
		if assign.Array != nil || assign.Index != nil || assign.Name == nil {
			w.unsupported(parser.UnsupportedNode, stmt, w.text(assign))
			continue
		}
		value := ""
		if assign.Value != nil {
			value = w.word(assign.Value).Text
		}
		cmd.EnvAssignments = append(cmd.EnvAssignments, parser.EnvAssignment{Name: assign.Name.Value, Value: value})
	}

	// Assignments without a command mutate the shell for later commands, which
	// Intenter cannot model any better than `export` (§14.3).
	if len(call.Args) == 0 {
		if len(call.Assigns) > 0 {
			w.unsupported(parser.UnsupportedExport, stmt, w.text(stmt))
		}
		return
	}

	for _, arg := range call.Args {
		cmd.Argv = append(cmd.Argv, w.word(arg))
	}
	cmd.Redirections = w.redirections(stmt)

	w.dispatch(cmd, stmt)
}

// dispatch applies the rules that depend on the executable name.
func (w *walker) dispatch(cmd parser.SimpleCommand, stmt *syntax.Stmt) {
	name := cmd.Name()
	if name == "" {
		return
	}
	base := path.Base(name)

	if kind, ok := refusedBuiltins[base]; ok {
		w.unsupported(kind, stmt, base)
		w.emit(cmd)
		return
	}

	// An elevation wrapper is refused, but its inner command is still parsed so
	// hard rules apply to what would actually run (§14.3).
	if parser.IsElevationWrapper(base) && !cmd.Elevated {
		w.unsupported(parser.UnsupportedElevation, stmt, base)
		cmd.Elevated = true
		if base == "sudo" || base == "doas" {
			if inner := stripElevationOptions(cmd.Argv[1:]); len(inner) > 0 {
				cmd.Argv = inner
				w.dispatch(cmd, stmt)
				return
			}
		}
		w.emit(cmd)
		return
	}

	// `sh -c "…"` hides its payload in a string the parser will not re-enter.
	// A bare interpreter reading a pipeline is different: it stays a normal
	// command and becomes UNRESOLVED through PipedFrom (§14.2, hard rule R12).
	if parser.IsStreamInterpreter(base) && hasInlineScriptFlag(cmd) {
		w.unsupported(parser.UnsupportedShellWrapper, stmt, base)
		w.emit(cmd)
		return
	}

	if base == "cd" || base == "chdir" {
		w.changeDir(cmd, stmt)
		return
	}

	w.emit(cmd)
}

// changeDir emits a `cd` and moves the effective cwd for the commands after it.
func (w *walker) changeDir(cmd parser.SimpleCommand, stmt *syntax.Stmt) {
	cmd.Builtin = true

	args := cmd.ArgTexts()
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}

	switch {
	case len(args) == 0:
		w.emit(cmd)
		if w.vars.Home != "" {
			w.cwd = w.vars.Home
		}
	case args[0] == "-":
		// The previous directory is shell state Intenter does not track.
		w.unsupported(parser.UnsupportedCdDash, stmt, "cd -")
		w.emit(cmd)
	default:
		w.emit(cmd)
		w.cwd = joinCwd(w.cwd, args[0])
	}
}

// emit appends a command, recording how it joins the previous one.
//
// Past MaxSimpleCommands the line is refused as too long to approve, but the
// commands are still emitted: the safety floor has to see a delete that was
// padded to the end of the line (§15.1). Only the memory bound stops emission,
// and that is reported as truncation so the resolver knows the tail is unseen.
func (w *walker) emit(cmd parser.SimpleCommand) {
	if len(w.out.Commands) >= parser.MaxSimpleCommands && !w.truncated {
		w.out.AddUnsupported(parser.UnsupportedTooManyCommands, 0,
			fmt.Sprintf("more than %d commands", parser.MaxSimpleCommands))
		w.truncated = true
	}
	if len(w.out.Commands) >= parser.MaxParsedCommands {
		if !w.hardCapped {
			w.out.AddUnsupported(parser.UnsupportedTruncated, 0,
				fmt.Sprintf("more than %d commands were not examined", parser.MaxParsedCommands))
			w.hardCapped = true
		}
		return
	}
	if len(w.out.Commands) > 0 {
		cmd.Operator = w.pendingOp
		w.out.Operators = append(w.out.Operators, w.pendingOp)
	}
	w.out.Commands = append(w.out.Commands, cmd)
	w.pendingOp = parser.OpSequence
}

// redirections converts a statement's redirections, dropping the ones with no
// filesystem effect: file-descriptor duplication, here-documents and the
// ignored devices (§14.2).
func (w *walker) redirections(stmt *syntax.Stmt) []parser.Redirection {
	var out []parser.Redirection
	for _, redir := range stmt.Redirs {
		if !fileRedirection(redir.Op) {
			continue
		}
		if redir.Word == nil {
			continue
		}
		target := w.word(redir.Word)
		if parser.IsIgnoredRedirectionTarget(target.Text) {
			continue
		}
		op := redir.Op.String()
		if redir.N != nil {
			op = redir.N.Value + op
		}
		out = append(out, parser.Redirection{Op: op, Target: target})
	}
	return out
}

// redirsUnsupported refuses redirections attached to a grouping construct,
// whose target applies to a whole statement list.
func (w *walker) redirsUnsupported(stmt *syntax.Stmt) {
	if len(stmt.Redirs) > 0 {
		w.unsupported(parser.UnsupportedRedirection, stmt, w.text(stmt))
	}
}

// word expands one shell word as far as Intenter is willing to guess.
func (w *walker) word(src *syntax.Word) parser.Word {
	out := parser.Word{}
	if src == nil {
		return out
	}

	var text strings.Builder
	w.wordParts(src.Parts, &text, &out, false)
	out.Text = text.String()

	// Tilde expansion applies only to an unquoted leading ~ (§14.2).
	if lit, ok := firstLit(src.Parts); ok && strings.HasPrefix(lit.Value, "~") {
		out.Text = parser.ExpandTilde(out.Text, w.vars.Home)
	}
	return out
}

// wordParts accumulates one word's parts. quoted marks a double-quoted context,
// where neither globbing nor tilde expansion happens.
func (w *walker) wordParts(parts []syntax.WordPart, text *strings.Builder, out *parser.Word, quoted bool) {
	for _, part := range parts {
		switch node := part.(type) {
		case *syntax.Lit:
			if quoted {
				text.WriteString(unescapeDouble(node.Value))
				continue
			}
			value, glob := unescapeUnquoted(node.Value)
			text.WriteString(value)
			if glob {
				out.ContainsGlob = true
			}

		case *syntax.SglQuoted:
			out.Quoted = true
			text.WriteString(node.Value)

		case *syntax.DblQuoted:
			out.Quoted = true
			w.wordParts(node.Parts, text, out, true)

		case *syntax.ParamExp:
			w.paramExp(node, text, out)

		case *syntax.CmdSubst:
			w.unsupportedAt(parser.UnsupportedCommandSubstitution, node.Pos(), w.text(node))
			out.ContainsUnexpandedVar = true
			text.WriteString(w.text(node))

		case *syntax.ArithmExp:
			w.unsupportedAt(parser.UnsupportedArithmetic, node.Pos(), w.text(node))
			out.ContainsUnexpandedVar = true
			text.WriteString(w.text(node))

		case *syntax.ProcSubst:
			w.unsupportedAt(parser.UnsupportedProcessSubstitution, node.Pos(), w.text(node))
			out.ContainsUnexpandedVar = true
			text.WriteString(w.text(node))

		case *syntax.ExtGlob:
			out.ContainsGlob = true
			text.WriteString(w.text(node))

		default:
			w.unsupportedAt(parser.UnsupportedNode, part.Pos(), w.text(part))
			out.ContainsUnexpandedVar = true
			text.WriteString(w.text(part))
		}
	}
}

// paramExp substitutes the small set of variables Intenter knows; anything
// else leaves the word ambiguous instead of being guessed (§16.1 step 1).
func (w *walker) paramExp(node *syntax.ParamExp, text *strings.Builder, out *parser.Word) {
	if name, ok := plainParamName(node); ok {
		if value, found := parser.Lookup(action.DialectPosix, name, w.vars); found {
			text.WriteString(value)
			return
		}
	}
	out.ContainsUnexpandedVar = true
	text.WriteString(w.text(node))
}

// unsupported records a refused construct at a node's position.
func (w *walker) unsupported(kind parser.UnsupportedKind, node syntax.Node, text string) {
	w.unsupportedAt(kind, node.Pos(), text)
}

func (w *walker) unsupportedAt(kind parser.UnsupportedKind, pos syntax.Pos, text string) {
	w.out.AddUnsupported(kind, int(pos.Offset()), truncate(text))
}

// text returns the source of a node, which keeps explanations faithful to what
// the agent actually wrote.
func (w *walker) text(node syntax.Node) string {
	start, end := node.Pos().Offset(), node.End().Offset()
	if !node.Pos().IsValid() || start > end || int(end) > len(w.src) {
		return ""
	}
	return w.src[start:end]
}

// refusedBuiltins are commands whose effects depend on shell state or on text
// Intenter will not re-enter (§14.3).
var refusedBuiltins = map[string]parser.UnsupportedKind{
	"eval":     parser.UnsupportedEval,
	"source":   parser.UnsupportedSource,
	".":        parser.UnsupportedSource,
	"exec":     parser.UnsupportedExec,
	"xargs":    parser.UnsupportedXargs,
	"alias":    parser.UnsupportedAlias,
	"unalias":  parser.UnsupportedAlias,
	"export":   parser.UnsupportedExport,
	"set":      parser.UnsupportedExport,
	"unset":    parser.UnsupportedExport,
	"declare":  parser.UnsupportedExport,
	"local":    parser.UnsupportedExport,
	"readonly": parser.UnsupportedExport,
	"typeset":  parser.UnsupportedExport,
	"setx":     parser.UnsupportedExport,
	"pushd":    parser.UnsupportedDirStack,
	"popd":     parser.UnsupportedDirStack,
	"dirs":     parser.UnsupportedDirStack,
	"trap":     parser.UnsupportedTrap,
}

// inlineScriptFlags make an interpreter run a script given as an argument. The
// spec names -c; -e is included because `node -e` and `perl -e` hide a payload
// the same way, and naming the construct explains the decision better than
// falling through to "unknown executable".
var inlineScriptFlags = map[string]bool{
	"-c": true, "--command": true, "-e": true, "--eval": true,
}

func hasInlineScriptFlag(cmd parser.SimpleCommand) bool {
	for _, arg := range cmd.Args() {
		if inlineScriptFlags[arg.Text] {
			return true
		}
	}
	return false
}

// elevationValueFlags are sudo/doas options that consume the following word,
// which must be skipped to find the inner command.
var elevationValueFlags = map[string]bool{
	"-u": true, "-g": true, "-p": true, "-C": true, "-h": true, "-r": true,
	"-t": true, "-T": true, "-U": true, "-R": true, "-a": true, "-D": true,
	"--user": true, "--group": true, "--prompt": true, "--host": true,
	"--role": true, "--type": true, "--chdir": true, "--close-from": true,
	"--command-timeout": true, "--other-user": true,
}

// stripElevationOptions returns the inner command of a sudo/doas invocation, or
// nil when there is none.
func stripElevationOptions(argv []parser.Word) []parser.Word {
	for i := 0; i < len(argv); i++ {
		text := argv[i].Text
		switch {
		case text == "--":
			return argv[i+1:]
		case strings.HasPrefix(text, "--") && strings.Contains(text, "="):
		case elevationValueFlags[text]:
			i++
		case strings.HasPrefix(text, "-") && text != "-":
		default:
			return argv[i:]
		}
	}
	return nil
}

// binaryOperator maps a shell binary operator onto the shared model. All
// branches are evaluated regardless of the operator (§14.2).
func binaryOperator(op syntax.BinCmdOperator) parser.Operator {
	switch op {
	case syntax.AndStmt:
		return parser.OpAnd
	case syntax.OrStmt:
		return parser.OpOr
	case syntax.Pipe, syntax.PipeAll:
		return parser.OpPipe
	}
	return parser.OpSequence
}

func isPipe(op syntax.BinCmdOperator) bool {
	return op == syntax.Pipe || op == syntax.PipeAll
}

// fileRedirection reports whether a redirection operator names a file, as
// opposed to duplicating a descriptor or feeding a here-document.
func fileRedirection(op syntax.RedirOperator) bool {
	switch op {
	case syntax.RdrOut, syntax.AppOut, syntax.RdrIn, syntax.RdrInOut, syntax.ClbOut,
		syntax.RdrAll, syntax.AppAll:
		return true
	}
	return false
}

// plainParamName returns the variable name of a bare $NAME or ${NAME}. Any
// expansion with an operator, index, slice or replacement is not a plain name
// and stays unexpanded.
func plainParamName(node *syntax.ParamExp) (string, bool) {
	if node.Param == nil || node.Excl || node.Length || node.Width ||
		node.Index != nil || node.Slice != nil || node.Repl != nil ||
		node.Exp != nil || node.Names != 0 {
		return "", false
	}
	return node.Param.Value, true
}

// firstLit returns the first word part when it is an unquoted literal.
func firstLit(parts []syntax.WordPart) (*syntax.Lit, bool) {
	if len(parts) == 0 {
		return nil, false
	}
	lit, ok := parts[0].(*syntax.Lit)
	return lit, ok
}

// declVariant names the declaration keyword for the explanation.
func declVariant(node *syntax.DeclClause) string {
	if node.Variant == nil {
		return "declare"
	}
	return node.Variant.Value
}

// unescapeUnquoted removes backslash escapes from an unquoted literal and
// reports whether an unescaped glob metacharacter remained.
func unescapeUnquoted(text string) (string, bool) {
	var out strings.Builder
	glob := false
	for i := 0; i < len(text); i++ {
		char := text[i]
		if char == '\\' && i+1 < len(text) {
			out.WriteByte(text[i+1])
			i++
			continue
		}
		switch char {
		case '*', '?':
			glob = true
		case '[':
			if strings.IndexByte(text[i:], ']') > 0 {
				glob = true
			}
		}
		out.WriteByte(char)
	}
	return out.String(), glob
}

// unescapeDouble removes the backslash escapes that are special inside double
// quotes; every other backslash is literal there.
func unescapeDouble(text string) string {
	if !strings.Contains(text, `\`) {
		return text
	}
	var out strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' && i+1 < len(text) {
			switch text[i+1] {
			case '"', '\\', '$', '`':
				continue
			}
		}
		out.WriteByte(text[i])
	}
	return out.String()
}

// joinCwd resolves a `cd` argument against the current effective cwd. Only the
// lexical join happens here; canonicalization is internal/scope's job (§16.1).
func joinCwd(cwd, dir string) string {
	switch {
	case dir == "":
		return cwd
	case isAbsolute(dir), cwd == "":
		return dir
	}
	separator := "/"
	if strings.Contains(cwd, `\`) && !strings.Contains(cwd, "/") {
		separator = `\`
	}
	return strings.TrimSuffix(cwd, separator) + separator + dir
}

// isAbsolute covers the POSIX form plus the Windows forms a Git Bash session
// can still produce.
func isAbsolute(dir string) bool {
	if strings.HasPrefix(dir, "/") || strings.HasPrefix(dir, `\`) {
		return true
	}
	return len(dir) >= 3 && dir[1] == ':' && (dir[2] == '/' || dir[2] == '\\')
}

// maxExplanationText bounds the source excerpt kept for an unsupported
// construct so a long command line cannot bloat the audit log.
const maxExplanationText = 120

func truncate(text string) string {
	if len(text) <= maxExplanationText {
		return text
	}
	return text[:maxExplanationText] + "…"
}
