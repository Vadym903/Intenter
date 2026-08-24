package cmd

import (
	"fmt"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/parser"
)

// Dialect parses the cmd.exe subset Intenter understands. It is a pure
// function of its input: nothing is executed (PROTOTYPE_SPEC.md §14.1).
//
// This dialect matters even on macOS and Linux: npm and yarn run package
// scripts through cmd.exe on Windows, so a script has to be understood under
// both dialects before its effects can be combined (§15.5.4, I-13).
type Dialect struct{}

// New builds the cmd dialect.
func New() Dialect { return Dialect{} }

// Name identifies the dialect in requests and approvals.
func (Dialect) Name() action.Dialect { return action.DialectCmd }

// refusedCommands change the shell's own state or hide their effects in text
// this parser will not re-enter (§14.3).
var refusedCommands = map[string]parser.UnsupportedKind{
	"call":     parser.UnsupportedShellWrapper,
	"start":    parser.UnsupportedStartProcess,
	"set":      parser.UnsupportedExport,
	"setx":     parser.UnsupportedExport,
	"setlocal": parser.UnsupportedExport,
	"endlocal": parser.UnsupportedExport,
	"pushd":    parser.UnsupportedDirStack,
	"popd":     parser.UnsupportedDirStack,
	"goto":     parser.UnsupportedControlFlow,
	"if":       parser.UnsupportedControlFlow,
	"for":      parser.UnsupportedControlFlow,
	"doskey":   parser.UnsupportedAlias,
}

// Parse converts a cmd.exe command line.
func (d Dialect) Parse(in parser.Input) (*parser.ParsedCommand, error) {
	out := &parser.ParsedCommand{Dialect: action.DialectCmd, FinalCwd: in.Cwd}

	if len(in.Command) > parser.MaxCommandBytes {
		out.AddUnsupported(parser.UnsupportedCommandTooLong, 0,
			fmt.Sprintf("%d bytes exceeds the %d byte limit", len(in.Command), parser.MaxCommandBytes))
		return out, nil
	}

	w := &walker{out: out, vars: parser.FromInput(in), cwd: in.Cwd}
	w.parse(in.Command)
	out.FinalCwd = w.cwd
	return out, nil
}

type walker struct {
	out       *parser.ParsedCommand
	vars      parser.VarContext
	cwd       string
	pendingOp parser.Operator
	truncated bool
	// hardCapped records that the memory bound was already reported.
	hardCapped bool
}

// segment is one command and the operator that joined it to the previous one.
type segment struct {
	text     string
	operator parser.Operator
	position int
	piped    bool
	index    int
}

func (w *walker) parse(line string) {
	segments, ok := splitSegments(line, w.out)
	if !ok {
		return
	}
	for i := range segments {
		if i > 0 {
			w.pendingOp = segments[i].operator
		}
		w.command(&segments[i])
	}
	w.markPipelines(segments)
}

// splitSegments cuts the line on cmd.exe's separators outside quotes. Unlike a
// POSIX shell, cmd treats `&` as an ordinary sequencing operator.
func splitSegments(line string, out *parser.ParsedCommand) ([]segment, bool) {
	segments := make([]segment, 0, 4)
	current := strings.Builder{}
	operator := parser.Operator("")
	start := 0
	inQuote := false

	flush := func(next parser.Operator, position int, piped bool) {
		segments = append(segments, segment{
			text: current.String(), operator: operator, position: start, piped: piped, index: -1,
		})
		current.Reset()
		operator = next
		start = position
	}

	for i := 0; i < len(line); i++ {
		char := line[i]

		// The caret escapes the next character, but only outside a quoted
		// string: cmd.exe does not recognize `^` as an escape inside "..."
		// (a literal caret there has no effect on the character after it,
		// including a closing quote). Escaping unconditionally would let a
		// `^"` inside quotes swallow the real closing quote, so the parser
		// keeps believing it is still inside the string while cmd.exe has
		// already closed it — hiding every `&`/`|` that follows, and with it
		// whatever command cmd.exe actually runs there.
		if !inQuote && char == '^' && i+1 < len(line) {
			i++
			current.WriteByte(line[i])
			continue
		}
		if char == '"' {
			inQuote = !inQuote
			current.WriteByte(char)
			continue
		}
		if inQuote {
			current.WriteByte(char)
			continue
		}

		// A `&` immediately following a (optionally handle-numbered)
		// redirection operator duplicates a stream handle (`2>&1`, `>&2`)
		// rather than sequencing a new command; cmd.exe does not split
		// there. Without this check the `&` in `dir 2>&1` is read as the
		// ordinary sequence operator, silently dropping `dir` from the
		// parsed output (its trailing "2>" becomes an unterminated
		// redirect and the segment is discarded) while `1` reappears as a
		// bare, unrelated command name — the delete/write the line
		// actually performs is never seen by the hard rules (AG-122).
		if char == '&' && endsWithRedirectOperator(current.String()) {
			current.WriteByte(char)
			continue
		}

		switch {
		case char == '&' && i+1 < len(line) && line[i+1] == '&':
			flush(parser.OpAnd, i+2, false)
			i++
		case char == '|' && i+1 < len(line) && line[i+1] == '|':
			flush(parser.OpOr, i+2, false)
			i++
		case char == '&':
			flush(parser.OpSequence, i+1, false)
		case char == '|':
			flush(parser.OpPipe, i+1, true)
		default:
			current.WriteByte(char)
		}
	}

	if inQuote {
		out.AddUnsupported(parser.UnsupportedSyntaxError, len(line), "unterminated quote")
		return nil, false
	}
	flush("", len(line), false)
	return segments, true
}

// endsWithRedirectOperator reports whether s ends with `<`/`>`, optionally
// preceded by a handle-number digit (`2>`), so a following `&` duplicates a
// stream handle instead of sequencing a new command.
func endsWithRedirectOperator(s string) bool {
	if s == "" {
		return false
	}
	last := s[len(s)-1]
	return last == '<' || last == '>'
}

// markPipelines records which commands feed which. The operator belongs to the
// segment that follows it, which is the one reading the previous output.
func (w *walker) markPipelines(segments []segment) {
	for i := 1; i < len(segments); i++ {
		if segments[i].operator != parser.OpPipe {
			continue
		}
		previous, current := segments[i-1].index, segments[i].index
		if previous < 0 || current < 0 {
			continue
		}
		w.out.Commands[previous].PipedInto = true
		w.out.Commands[current].PipedFrom = true
	}
}

// command converts one segment.
func (w *walker) command(item *segment) {
	text := stripComment(item.text)
	if strings.TrimSpace(text) == "" {
		return
	}

	words, redirections, ok := w.tokenize(text, item.position)
	if !ok || len(words) == 0 {
		return
	}
	// An empty command name is not something to run; an empty argument still
	// is a path, so only the name is checked.
	if words[0].Text == "" {
		w.out.AddUnsupported(parser.UnsupportedSyntaxError, item.position, "empty command name")
		return
	}

	cmd := parser.SimpleCommand{
		Argv:         words,
		Redirections: redirections,
		EffectiveCwd: w.cwd,
		RawText:      strings.TrimSpace(item.text),
	}
	w.dispatch(cmd, item)
}

// dispatch applies the rules that depend on the command name.
func (w *walker) dispatch(cmd parser.SimpleCommand, item *segment) {
	name := strings.ToLower(strings.TrimSuffix(cmd.Name(), ".exe"))

	if kind, refused := refusedCommands[name]; refused {
		w.out.AddUnsupported(kind, item.position, cmd.Name())
		w.emit(&cmd, item)
		return
	}
	if parser.IsStreamInterpreter(name) && hasInlineScriptFlag(cmd) {
		w.out.AddUnsupported(parser.UnsupportedShellWrapper, item.position, cmd.Name())
		w.emit(&cmd, item)
		return
	}
	if parser.IsElevationWrapper(name) {
		w.out.AddUnsupported(parser.UnsupportedElevation, item.position, cmd.Name())
		cmd.Elevated = true
		w.emit(&cmd, item)
		return
	}
	if name == "cd" || name == "chdir" {
		w.changeDir(&cmd, item)
		return
	}
	w.emit(&cmd, item)
}

// changeDir emits a directory change and moves the effective cwd. cmd's `cd`
// takes /d to change drive as well, which does not change the target.
func (w *walker) changeDir(cmd *parser.SimpleCommand, item *segment) {
	cmd.Builtin = true

	args := make([]string, 0, len(cmd.Argv))
	for _, word := range cmd.Args() {
		if strings.HasPrefix(word.Text, "/") {
			continue
		}
		args = append(args, word.Text)
	}

	if len(args) == 0 {
		// Bare `cd` prints the current directory; it does not move.
		w.emit(cmd, item)
		return
	}
	w.emit(cmd, item)
	w.cwd = joinCwd(w.cwd, args[0])
}

func (w *walker) emit(cmd *parser.SimpleCommand, item *segment) {
	// Past the approvable cap the line is refused, but every command is still
	// emitted so the hard rules see a delete padded to the end (§15.1).
	if len(w.out.Commands) >= parser.MaxSimpleCommands && !w.truncated {
		w.out.AddUnsupported(parser.UnsupportedTooManyCommands, item.position,
			fmt.Sprintf("more than %d commands", parser.MaxSimpleCommands))
		w.truncated = true
	}
	if len(w.out.Commands) >= parser.MaxParsedCommands {
		if !w.hardCapped {
			w.out.AddUnsupported(parser.UnsupportedTruncated, item.position,
				fmt.Sprintf("more than %d commands were not examined", parser.MaxParsedCommands))
			w.hardCapped = true
		}
		return
	}
	if len(w.out.Commands) > 0 {
		cmd.Operator = w.pendingOp
		w.out.Operators = append(w.out.Operators, w.pendingOp)
	}
	w.out.Commands = append(w.out.Commands, *cmd)
	item.index = len(w.out.Commands) - 1
}

// tokenize splits one segment into words and redirections.
func (w *walker) tokenize(text string, position int) ([]parser.Word, []parser.Redirection, bool) {
	var (
		words        []parser.Word
		redirections []parser.Redirection
		pendingRedir string
	)

	for _, item := range splitTokens(text) {
		if operator, isRedirect := redirectionOperator(item.text); isRedirect {
			if operator == "" {
				continue
			}
			pendingRedir = operator
			continue
		}

		word := w.word(item)
		if pendingRedir != "" {
			if !parser.IsIgnoredRedirectionTarget(word.Text) {
				redirections = append(redirections, parser.Redirection{Op: pendingRedir, Target: word})
			}
			pendingRedir = ""
			continue
		}
		words = append(words, word)
	}

	if pendingRedir != "" {
		w.out.AddUnsupported(parser.UnsupportedSyntaxError, position, "redirection without a target")
		return nil, nil, false
	}
	return words, redirections, true
}

// word expands one token. cmd has no single-quote form: `%VAR%` expands even
// inside double quotes.
func (w *walker) word(item token) parser.Word {
	expanded, unexpanded := parser.ExpandCmd(item.text, w.vars)

	out := parser.Word{Text: expanded, Quoted: item.quoted, ContainsUnexpandedVar: unexpanded}
	if !item.quoted {
		out.ContainsGlob = parser.ContainsGlob(expanded)
	}
	return out
}

type token struct {
	text   string
	quoted bool
}

// splitTokens splits a segment on whitespace and on the `<`/`>` redirection
// metacharacters, honoring double quotes and the caret escape.
func splitTokens(text string) []token {
	var (
		tokens  []token
		current strings.Builder
		inQuote bool
		quoted  bool
		started bool
	)

	flush := func() {
		if !started {
			return
		}
		tokens = append(tokens, token{text: current.String(), quoted: quoted})
		current.Reset()
		quoted = false
		started = false
	}

	for i := 0; i < len(text); i++ {
		char := text[i]

		// See splitSegments: the caret only escapes outside a quoted string.
		if !inQuote && char == '^' && i+1 < len(text) {
			i++
			current.WriteByte(text[i])
			started = true
			continue
		}
		if char == '"' {
			inQuote = !inQuote
			quoted = true
			started = true
			continue
		}
		if inQuote {
			current.WriteByte(char)
			started = true
			continue
		}

		// cmd.exe treats `<`/`>` as metacharacters even with no surrounding
		// whitespace — `echo hi>out.txt` redirects exactly like
		// `echo hi > out.txt` — so they must end the current token here
		// too, not just at a blank. Leaving this to whitespace-only
		// splitting made every unspaced redirection (and every unspaced
		// operand) invisible to applyRedirections: the write was never
		// modeled as an effect at all (AG-122). A bare digit run
		// accumulated so far is the operator's handle number (`2>nul`),
		// not a separate word, so it is pulled into the operator instead
		// of being flushed with it.
		if char == '<' || char == '>' {
			pending := current.String()
			handle := ""
			if pending != "" && isDigits(pending) {
				handle = pending
				current.Reset()
				started = false
			} else {
				flush()
			}
			op := handle + string(char)
			switch {
			case i+1 < len(text) && text[i+1] == char:
				op += string(char)
				i++
			case i+1 < len(text) && text[i+1] == '&':
				// Stream-handle duplication (`2>&1`): the `&digits` suffix
				// is part of the same operator, not a new word.
				j := i + 2
				for j < len(text) && text[j] >= '0' && text[j] <= '9' {
					j++
				}
				op += text[i+1 : j]
				i = j - 1
			}
			tokens = append(tokens, token{text: op})
			continue
		}

		switch char {
		case ' ', '\t', '\n', '\r':
			flush()
		default:
			current.WriteByte(char)
			started = true
		}
	}
	flush()
	return tokens
}

// isDigits reports whether s is non-empty and consists only of ASCII digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// redirectionOperator reports whether a token is a redirection.
func redirectionOperator(text string) (string, bool) {
	switch text {
	case ">", ">>", "<", "1>", "1>>", "2>", "2>>":
		return text, true
	case "2>&1", "1>&2":
		return "", true
	}
	return "", false
}

// hasInlineScriptFlag reports the `/c` and `/k` forms that hide a command in a
// string.
func hasInlineScriptFlag(cmd parser.SimpleCommand) bool {
	for _, arg := range cmd.Args() {
		switch strings.ToLower(arg.Text) {
		case "/c", "/k", "-c":
			return true
		}
	}
	return false
}

// stripComment removes a `REM` or `::` comment.
func stripComment(text string) string {
	trimmed := strings.TrimLeft(text, " \t")
	lowered := strings.ToLower(trimmed)
	if strings.HasPrefix(lowered, "rem ") || lowered == "rem" || strings.HasPrefix(trimmed, "::") {
		return ""
	}
	return text
}

// joinCwd resolves a directory argument against the current one.
func joinCwd(cwd, dir string) string {
	switch {
	case dir == "":
		return cwd
	case isAbsolute(dir), cwd == "":
		return dir
	}
	separator := `\`
	if strings.Contains(cwd, "/") && !strings.Contains(cwd, `\`) {
		separator = "/"
	}
	return strings.TrimRight(cwd, `/\`) + separator + dir
}

func isAbsolute(dir string) bool {
	if strings.HasPrefix(dir, `\`) || strings.HasPrefix(dir, "/") {
		return true
	}
	return len(dir) >= 3 && dir[1] == ':' && (dir[2] == '/' || dir[2] == '\\')
}
