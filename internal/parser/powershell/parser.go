package powershell

import (
	"fmt"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/parser"
)

// Dialect parses the PowerShell subset Intenter understands. It is a pure
// function of its input: nothing is executed (PROTOTYPE_SPEC.md §14.1).
//
// PowerShell is a full programming language, so this parser is deliberately
// small: it recognizes command invocations, quoting, the pipeline and
// conditional operators, redirections and the variables §14.2 lists, and
// refuses everything else. Refusing is safe — it caps the decision at ASK.
type Dialect struct{}

// New builds the PowerShell dialect.
func New() Dialect { return Dialect{} }

// Name identifies the dialect in requests and approvals.
func (Dialect) Name() action.Dialect { return action.DialectPowerShell }

// aliases maps PowerShell's built-in aliases onto the cmdlet they run, so a
// recognizer only has to know the cmdlet.
var aliases = map[string]string{
	"ri": "Remove-Item", "rm": "Remove-Item", "del": "Remove-Item",
	"erase": "Remove-Item", "rd": "Remove-Item", "rmdir": "Remove-Item",
	"cpi": "Copy-Item", "cp": "Copy-Item", "copy": "Copy-Item",
	"mi": "Move-Item", "mv": "Move-Item", "move": "Move-Item",
	"ni": "New-Item", "md": "New-Item", "mkdir": "New-Item",
	"gc": "Get-Content", "cat": "Get-Content", "type": "Get-Content",
	"gci": "Get-ChildItem", "ls": "Get-ChildItem", "dir": "Get-ChildItem",
	"sls": "Select-String",
	"sl":  "Set-Location", "cd": "Set-Location", "chdir": "Set-Location",
	"gl": "Get-Location", "pwd": "Get-Location",
	"echo": "Write-Output", "write": "Write-Output",
	"sc": "Set-Content", "ac": "Add-Content",
	"gcm": "Get-Command", "gm": "Get-Member",
}

// refusedCommands hide their effects in text this parser will not re-enter, or
// change the shell's own state (§14.3).
var refusedCommands = map[string]parser.UnsupportedKind{
	"invoke-expression": parser.UnsupportedInvokeExpression,
	"iex":               parser.UnsupportedInvokeExpression,
	"start-process":     parser.UnsupportedStartProcess,
	"saps":              parser.UnsupportedStartProcess,
	"start":             parser.UnsupportedStartProcess,
	"invoke-command":    parser.UnsupportedInvokeExpression,
	"icm":               parser.UnsupportedInvokeExpression,
	"set-alias":         parser.UnsupportedAlias,
	"new-alias":         parser.UnsupportedAlias,
	"sal":               parser.UnsupportedAlias,
	"set-variable":      parser.UnsupportedExport,
	"set-item":          parser.UnsupportedExport,
	"push-location":     parser.UnsupportedDirStack,
	"pushd":             parser.UnsupportedDirStack,
	"pop-location":      parser.UnsupportedDirStack,
	"popd":              parser.UnsupportedDirStack,
	"trap":              parser.UnsupportedTrap,
	"invoke-item":       parser.UnsupportedStartProcess,
	"ii":                parser.UnsupportedStartProcess,
}

// Parse walks the command line one token at a time.
func (d Dialect) Parse(in parser.Input) (*parser.ParsedCommand, error) {
	out := &parser.ParsedCommand{Dialect: action.DialectPowerShell, FinalCwd: in.Cwd}

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

// walker turns a PowerShell command line into the shared model.
type walker struct {
	out       *parser.ParsedCommand
	vars      parser.VarContext
	cwd       string
	pendingOp parser.Operator
	truncated bool
	// hardCapped records that the memory bound was already reported.
	hardCapped bool
}

// parse splits the line into segments on the operators PowerShell supports and
// converts each one.
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

// segment is one command and the operator that joined it to the previous one.
type segment struct {
	text     string
	operator parser.Operator
	position int
	// piped marks a segment joined by `|`.
	piped bool
	// index is the position of the emitted command, or -1 when nothing was
	// emitted for this segment.
	index int
}

// splitSegments cuts the line on `;`, `&&`, `||` and `|` outside quotes.
func splitSegments(line string, out *parser.ParsedCommand) ([]segment, bool) {
	segments := make([]segment, 0, 4)
	current := strings.Builder{}
	operator := parser.Operator("")
	start := 0

	var quote byte
	depth := 0

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

		switch {
		case quote != 0:
			current.WriteByte(char)
			if char == quote {
				quote = 0
			}
			continue
		case char == '\'' || char == '"':
			quote = char
			current.WriteByte(char)
			continue
		case char == '{':
			// A script block is code this parser will not re-enter (§14.3).
			out.AddUnsupported(parser.UnsupportedScriptBlock, i, "{")
			return nil, false
		case char == '(':
			depth++
			current.WriteByte(char)
			continue
		case char == ')':
			depth--
			current.WriteByte(char)
			continue
		case char == '$' && i+1 < len(line) && line[i+1] == '(':
			out.AddUnsupported(parser.UnsupportedCommandSubstitution, i, "$(")
			return nil, false
		case char == '@' && i+1 < len(line) && line[i+1] == '(':
			out.AddUnsupported(parser.UnsupportedCommandSubstitution, i, "@(")
			return nil, false
		}

		if depth > 0 {
			current.WriteByte(char)
			continue
		}

		switch {
		case char == ';':
			flush(parser.OpSequence, i+1, false)
		case char == '&' && i+1 < len(line) && line[i+1] == '&':
			flush(parser.OpAnd, i+2, false)
			i++
		case char == '|' && i+1 < len(line) && line[i+1] == '|':
			flush(parser.OpOr, i+2, false)
			i++
		case char == '|':
			flush(parser.OpPipe, i+1, true)
		default:
			current.WriteByte(char)
		}
	}

	if quote != 0 {
		out.AddUnsupported(parser.UnsupportedSyntaxError, len(line), "unterminated quote")
		return nil, false
	}
	flush("", len(line), false)

	// A trailing `&` starts a background job, which outlives the request.
	for _, item := range segments {
		if strings.HasSuffix(strings.TrimSpace(item.text), "&") {
			out.AddUnsupported(parser.UnsupportedBackground, item.position, "&")
		}
	}
	return segments, true
}

// markPipelines records which commands feed which, after every segment has been
// converted. The operator belongs to the segment that follows it, which is the
// one reading the previous command's output.
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

// command converts one segment into a simple command, recording where it
// landed so pipelines can be marked afterwards.
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
	name := strings.ToLower(cmd.Name())

	// The call operator invokes the path that follows it.
	if name == "&" || name == "." {
		if len(cmd.Argv) < 2 || cmd.Argv[1].Text == "" {
			w.out.AddUnsupported(parser.UnsupportedSyntaxError, item.position,
				"the call operator names nothing to run")
			return
		}
		cmd.Argv = cmd.Argv[1:]
		name = strings.ToLower(cmd.Name())
	}

	if kind, refused := refusedCommands[name]; refused {
		w.out.AddUnsupported(kind, item.position, cmd.Name())
		w.emit(&cmd, item)
		return
	}

	// A `-Command` or `-EncodedCommand` argument hides a script in a string.
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

	// Resolve the alias so recognizers only deal with cmdlet names.
	if cmdlet, aliased := aliases[name]; aliased {
		cmd.Argv[0].Text = cmdlet
	}

	if strings.EqualFold(cmd.Name(), "Set-Location") {
		w.changeDir(&cmd, item)
		return
	}
	w.emit(&cmd, item)
}

// changeDir emits a location change and moves the effective cwd.
func (w *walker) changeDir(cmd *parser.SimpleCommand, item *segment) {
	cmd.Builtin = true

	args := make([]string, 0, len(cmd.Argv))
	for _, word := range cmd.Args() {
		if strings.HasPrefix(word.Text, "-") {
			continue
		}
		args = append(args, word.Text)
	}

	switch {
	case len(args) == 0:
		w.emit(cmd, item)
		if w.vars.Home != "" {
			w.cwd = w.vars.Home
		}
	case args[0] == "-":
		w.out.AddUnsupported(parser.UnsupportedCdDash, item.position, "Set-Location -")
		w.emit(cmd, item)
	default:
		w.emit(cmd, item)
		w.cwd = joinCwd(w.cwd, args[0])
	}
}

// emit appends a command, recording how it joins the previous one and where it
// landed.
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

// tokenize splits one segment into words and redirections, applying the
// quoting and expansion rules of §14.2.
func (w *walker) tokenize(text string, position int) ([]parser.Word, []parser.Redirection, bool) {
	var (
		words        []parser.Word
		redirections []parser.Redirection
		pendingRedir string
	)

	for _, token := range w.splitTokens(text) {
		if operator, isRedirect := redirectionOperator(token.text); isRedirect {
			if operator == "" {
				// A descriptor duplication such as 2>&1 has no file target.
				continue
			}
			pendingRedir = operator
			continue
		}

		word := w.word(token)
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

// word converts one already-expanded token.
func (w *walker) word(token token) parser.Word {
	out := parser.Word{
		Text:                  token.text,
		Quoted:                token.quoted,
		ContainsUnexpandedVar: token.unexpanded,
	}
	if !token.quoted {
		out.ContainsGlob = parser.ContainsGlob(token.expandable)
	}
	return out
}

// token is one word of a segment, already expanded.
type token struct {
	text   string
	quoted bool
	// unexpanded marks a variable Intenter would not substitute.
	unexpanded bool
	// expandable is the part of the token that was subject to expansion, used
	// for glob detection so an escaped or single-quoted `*` stays literal.
	expandable string
}

// splitTokens splits a segment on whitespace and expands it in one pass.
//
// Expansion has to happen here rather than on the finished token: a
// single-quoted section and a backtick-escaped character are both literal, and
// that is only known while the quoting state is still in hand. Expanding
// afterwards would treat `'$env:USERPROFILE'` as a variable.
func (w *walker) splitTokens(text string) []token {
	var (
		tokens     []token
		literal    strings.Builder
		expandable strings.Builder
		quote      byte
		quoted     bool
		started    bool
	)

	flush := func() {
		if !started {
			return
		}
		expanded, unexpanded := parser.ExpandPowerShell(expandable.String(), w.vars)
		tokens = append(tokens, token{
			text:       literal.String() + expanded,
			quoted:     quoted,
			unexpanded: unexpanded,
			expandable: expandable.String(),
		})
		literal.Reset()
		expandable.Reset()
		quoted = false
		started = false
	}

	// keepLiteral moves anything accumulated so far into the literal part, so
	// the expansion never sees it.
	keepLiteral := func(value string) {
		expanded, unexpanded := parser.ExpandPowerShell(expandable.String(), w.vars)
		if unexpanded {
			literal.WriteString(expandable.String())
		} else {
			literal.WriteString(expanded)
		}
		expandable.Reset()
		literal.WriteString(value)
	}

	for i := 0; i < len(text); i++ {
		char := text[i]

		if quote != 0 {
			if char == quote {
				quote = 0
				continue
			}
			if quote == '\'' {
				// Single quotes suppress every expansion in PowerShell.
				keepLiteral(string(char))
			} else {
				expandable.WriteByte(char)
			}
			started = true
			continue
		}

		// PowerShell treats `<`/`>` as redirection metacharacters even with no
		// surrounding whitespace — `Write-Output hi>out.txt` redirects exactly
		// like `Write-Output hi > out.txt` — so they must end the current
		// token here too, not just at a blank. Leaving this to whitespace-only
		// splitting made every unspaced redirection invisible to
		// applyRedirections: the write was never modeled as an effect at all
		// (AG-122, the same root cause as the cmd.exe dialect). A bare
		// handle-number prefix accumulated so far (`2`, or `*` for every
		// stream) belongs to the operator, not to a separate word.
		if (char == '<' || char == '>') && literal.Len() == 0 && isRedirectHandlePrefix(expandable.String()) {
			handle := expandable.String()
			expandable.Reset()
			started = false
			i = w.emitOperatorToken(text, i, char, handle, &tokens)
			continue
		}
		if char == '<' || char == '>' {
			flush()
			i = w.emitOperatorToken(text, i, char, "", &tokens)
			continue
		}

		switch char {
		case '`':
			// The backtick escapes the next character, which is then literal.
			if i+1 < len(text) {
				i++
				keepLiteral(string(text[i]))
				started = true
			}
		case '\'', '"':
			quote = char
			quoted = true
			started = true
		case ' ', '\t', '\n', '\r':
			flush()
		default:
			expandable.WriteByte(char)
			started = true
		}
	}
	flush()
	return tokens
}

// emitOperatorToken consumes a `<`/`>` redirection operator starting at
// text[i] (already known not to be quoted), gluing on a doubled `>>` or a
// stream-handle duplication suffix (`2>&1`), and appends it as its own,
// unexpanded token. It returns the index of the last byte consumed.
func (w *walker) emitOperatorToken(text string, i int, char byte, handle string, tokens *[]token) int {
	op := handle + string(char)
	switch {
	case char == '>' && i+1 < len(text) && text[i+1] == '>':
		op += ">"
		i++
	case i+1 < len(text) && text[i+1] == '&':
		// Stream-handle duplication (`2>&1`, `*>&1`): the `&digits` suffix is
		// part of the same operator, not a separate word.
		j := i + 2
		for j < len(text) && text[j] >= '0' && text[j] <= '9' {
			j++
		}
		op += text[i+1 : j]
		i = j - 1
	}
	*tokens = append(*tokens, token{text: op})
	return i
}

// isRedirectHandlePrefix reports whether s is a stream-handle prefix that
// belongs glued to a following `<`/`>` (a digit 1-6, or `*` for every
// stream) rather than being a separate word.
func isRedirectHandlePrefix(s string) bool {
	if s == "*" {
		return true
	}
	return isDigits(s)
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

// redirectionOperator reports whether a token is a redirection, and the
// operator to record. An empty operator with true means a descriptor
// duplication, which has no filesystem effect.
func redirectionOperator(text string) (string, bool) {
	switch text {
	case ">", ">>", "<", "1>", "1>>", "2>", "2>>", "3>", "4>", "5>", "6>", "*>", "*>>":
		return text, true
	case "2>&1", "1>&2", "*>&1":
		return "", true
	}
	return "", false
}

// hasInlineScriptFlag reports the `-Command`/`-EncodedCommand` forms.
func hasInlineScriptFlag(cmd parser.SimpleCommand) bool {
	for _, arg := range cmd.Args() {
		switch strings.ToLower(arg.Text) {
		case "-command", "-c", "-encodedcommand", "-e", "-enc":
			return true
		}
	}
	return false
}

// stripComment removes a trailing `#` comment outside quotes.
func stripComment(text string) string {
	var quote byte
	for i := 0; i < len(text); i++ {
		char := text[i]
		switch {
		case quote != 0:
			if char == quote {
				quote = 0
			}
		case char == '\'' || char == '"':
			quote = char
		case char == '#':
			return text[:i]
		}
	}
	return text
}

// joinCwd resolves a location argument against the current one. Only the
// lexical join happens here; canonicalization is internal/scope's job.
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

// isAbsolute covers the Windows and POSIX forms PowerShell accepts.
func isAbsolute(dir string) bool {
	if strings.HasPrefix(dir, "/") || strings.HasPrefix(dir, `\`) {
		return true
	}
	return len(dir) >= 3 && dir[1] == ':' && (dir[2] == '/' || dir[2] == '\\')
}
