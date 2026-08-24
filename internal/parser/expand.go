package parser

import (
	"path/filepath"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
)

// VarContext supplies the only values Intenter is willing to substitute.
// Anything outside this set makes a word ambiguous rather than guessed
// (PROTOTYPE_SPEC.md §14.2, §16.1 step 1).
type VarContext struct {
	Home    string
	Cwd     string
	TempDir string
	// Env carries variables a caller explicitly supplies; it is empty in normal
	// operation because the agent's environment is invisible to hooks (§27).
	Env map[string]string
}

// FromInput builds a variable context out of parser input.
func FromInput(in Input) VarContext {
	return VarContext{Home: in.Home, Cwd: in.Cwd, TempDir: in.TempDir, Env: in.Env}
}

// Lookup resolves one variable name for a dialect. The name is given without
// the dialect's sigils: "HOME", "PWD", "env:TEMP", …
func Lookup(dialect action.Dialect, name string, ctx VarContext) (string, bool) {
	if value, ok := ctx.Env[name]; ok {
		return value, true
	}

	switch dialect {
	case action.DialectPosix:
		switch name {
		case "HOME":
			return ctx.Home, ctx.Home != ""
		case "PWD":
			return ctx.Cwd, ctx.Cwd != ""
		case "TMPDIR":
			return ctx.TempDir, ctx.TempDir != ""
		}
	case action.DialectPowerShell:
		switch name {
		case "HOME", "env:HOME", "env:USERPROFILE":
			return ctx.Home, ctx.Home != ""
		case "PWD":
			return ctx.Cwd, ctx.Cwd != ""
		case "env:TEMP", "env:TMP":
			return ctx.TempDir, ctx.TempDir != ""
		}
	case action.DialectCmd:
		switch strings.ToUpper(name) {
		case "USERPROFILE":
			return ctx.Home, ctx.Home != ""
		case "HOMEDRIVE":
			return filepath.VolumeName(ctx.Home), ctx.Home != ""
		case "HOMEPATH":
			return strings.TrimPrefix(ctx.Home, filepath.VolumeName(ctx.Home)), ctx.Home != ""
		case "TEMP", "TMP":
			return ctx.TempDir, ctx.TempDir != ""
		case "CD":
			return ctx.Cwd, ctx.Cwd != ""
		}
	}
	return "", false
}

// ExpandTilde replaces a leading ~ or ~/… with the home directory. A ~user
// form is left untouched: Intenter does not resolve other users' homes.
func ExpandTilde(text, home string) string {
	if home == "" || text == "" || text[0] != '~' {
		return text
	}
	if text == "~" {
		return home
	}
	if len(text) > 1 && (text[1] == '/' || text[1] == '\\') {
		return home + text[1:]
	}
	return text
}

// ExpandPowerShell expands the PowerShell forms Intenter supports and reports
// whether an unsupported variable remained (§14.2).
func ExpandPowerShell(text string, ctx VarContext) (string, bool) {
	text = ExpandTilde(text, ctx.Home)

	var out strings.Builder
	unexpanded := false
	for i := 0; i < len(text); {
		if text[i] != '$' {
			out.WriteByte(text[i])
			i++
			continue
		}
		name, next := readPowerShellName(text, i+1)
		if name == "" {
			out.WriteByte(text[i])
			i++
			continue
		}
		if value, ok := Lookup(action.DialectPowerShell, name, ctx); ok {
			out.WriteString(value)
		} else {
			unexpanded = true
			out.WriteString(text[i:next])
		}
		i = next
	}
	return out.String(), unexpanded
}

// readPowerShellName reads a variable name starting after '$', supporting the
// `$env:NAME` and `${NAME}` forms.
func readPowerShellName(text string, start int) (string, int) {
	if start >= len(text) {
		return "", start
	}
	if text[start] == '{' {
		if end := strings.IndexByte(text[start:], '}'); end > 0 {
			return text[start+1 : start+end], start + end + 1
		}
		return "", start
	}
	end := start
	for end < len(text) && (isNameChar(text[end]) || text[end] == ':') {
		end++
	}
	if end == start {
		return "", start
	}
	return text[start:end], end
}

// ExpandCmd expands the cmd.exe forms Intenter supports and reports whether
// an unsupported variable remained (§14.2).
func ExpandCmd(text string, ctx VarContext) (string, bool) {
	var out strings.Builder
	unexpanded := false
	for i := 0; i < len(text); {
		if text[i] != '%' {
			out.WriteByte(text[i])
			i++
			continue
		}
		end := strings.IndexByte(text[i+1:], '%')
		if end < 0 {
			out.WriteString(text[i:])
			break
		}
		name := text[i+1 : i+1+end]
		if name == "" {
			// "%%" is a literal percent sign.
			out.WriteByte('%')
			i += 2
			continue
		}
		if value, ok := Lookup(action.DialectCmd, name, ctx); ok {
			out.WriteString(value)
		} else {
			unexpanded = true
			out.WriteString(text[i : i+end+2])
		}
		i += end + 2
	}
	return out.String(), unexpanded
}

// ExpandPosixString expands the POSIX forms in a plain string. The POSIX parser
// works on the shell AST instead; this helper serves resolved script text and
// tests.
func ExpandPosixString(text string, ctx VarContext) (string, bool) {
	text = ExpandTilde(text, ctx.Home)

	var out strings.Builder
	unexpanded := false
	for i := 0; i < len(text); {
		if text[i] != '$' {
			out.WriteByte(text[i])
			i++
			continue
		}
		name, next := readPosixName(text, i+1)
		if name == "" {
			out.WriteByte(text[i])
			i++
			continue
		}
		if value, ok := Lookup(action.DialectPosix, name, ctx); ok {
			out.WriteString(value)
		} else {
			unexpanded = true
			out.WriteString(text[i:next])
		}
		i = next
	}
	return out.String(), unexpanded
}

// readPosixName reads a variable name starting after '$', supporting `${NAME}`.
func readPosixName(text string, start int) (string, int) {
	if start >= len(text) {
		return "", start
	}
	if text[start] == '{' {
		if end := strings.IndexByte(text[start:], '}'); end > 0 {
			name := text[start+1 : start+end]
			// Parameter expansions with operators (${VAR:-x}) are not plain
			// names and stay unexpanded.
			if strings.ContainsAny(name, ":-+?#%/^,") {
				return name, start + end + 1
			}
			return name, start + end + 1
		}
		return "", start
	}
	end := start
	for end < len(text) && isNameChar(text[end]) {
		end++
	}
	if end == start {
		return "", start
	}
	return text[start:end], end
}

func isNameChar(c byte) bool {
	return c == '_' ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}

// ContainsGlob reports whether a word carries glob metacharacters (§14.2).
func ContainsGlob(text string) bool {
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '*', '?':
			return true
		case '[':
			if strings.IndexByte(text[i:], ']') > 0 {
				return true
			}
		}
	}
	return false
}

// IgnoredRedirectionTargets are devices whose redirection has no filesystem
// effect (§14.2).
var IgnoredRedirectionTargets = map[string]bool{
	"/dev/null":   true,
	"/dev/stdout": true,
	"/dev/stderr": true,
	"/dev/stdin":  true,
	"nul":         true,
	"NUL":         true,
	"$null":       true,
}

// IsIgnoredRedirectionTarget reports whether a redirection target is a device
// Intenter ignores.
func IsIgnoredRedirectionTarget(target string) bool {
	if IgnoredRedirectionTargets[target] {
		return true
	}
	return IgnoredRedirectionTargets[strings.ToLower(target)]
}

// NeutralEnvNames are the environment assignments known not to change what a
// modeled command runs, where it writes, or which host it contacts. They are
// the only prefixes a command may carry and stay resolvable (§14.2).
//
// This is an allowlist on purpose. The threat model is an agent that chooses
// the command line, and the set of variables that make a program execute
// something else — PAGER and EDITOR for git, BASH_ENV for a shell, PERL5OPT
// and PYTHONSTARTUP for interpreters, *_JAVA_OPTIONS and MAVEN_OPTS for the
// build tools, PATH and the loader variables — is open-ended. Enumerating the
// dangerous ones was tried and missed PAGER, which made
// `PAGER='<anything>' git --paginate log` an auto-allowed read. Naming the
// harmless ones cannot fail that way: an unlisted variable costs a prompt,
// never a decision.
var NeutralEnvNames = map[string]bool{
	"CI": true, "NODE_ENV": true, "DEBUG": true, "LOG_LEVEL": true, "VERBOSE": true,
	"QUIET": true, "FORCE_COLOR": true, "NO_COLOR": true, "CLICOLOR": true,
	"CLICOLOR_FORCE": true, "TERM": true, "COLUMNS": true, "LINES": true, "TZ": true,
	"LANG": true, "LANGUAGE": true, "LC_ALL": true, "LC_CTYPE": true, "LC_MESSAGES": true,
	"LC_COLLATE": true, "LC_NUMERIC": true, "LC_TIME": true, "PORT": true,
	"DO_NOT_TRACK": true, "NEXT_TELEMETRY_DISABLED": true, "NODE_NO_WARNINGS": true,
	"PYTHONUNBUFFERED": true, "PYTHONDONTWRITEBYTECODE": true, "HUSKY": true,
	"BROWSER": false, "GRADLE_OPTS": false, "MAVEN_OPTS": false, "JAVA_HOME": false,
}

// IsDangerousEnvAssignment reports whether an assignment may change a command's
// behavior in a way Intenter cannot model. Every name outside NeutralEnvNames
// is dangerous; the entries explicitly set to false above document variables
// that look harmless and are not.
func IsDangerousEnvAssignment(name string) bool {
	return !NeutralEnvNames[strings.ToUpper(strings.TrimSpace(name))]
}

// StreamInterpreters execute whatever they are given; a pipeline stage that is
// one of these is UNRESOLVED with EXECUTE(UNRESOLVED) (§14.2, hard rule R12).
var StreamInterpreters = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true,
	"pwsh": true, "powershell": true, "cmd": true,
	"python": true, "python2": true, "python3": true,
	"node": true, "deno": true, "bun": true,
	"perl": true, "ruby": true, "php": true,
	"invoke-expression": true, "iex": true,
}

// IsStreamInterpreter reports whether a program executes content piped into it.
func IsStreamInterpreter(name string) bool {
	base := strings.ToLower(filepath.Base(name))
	base = strings.TrimSuffix(base, ".exe")
	return StreamInterpreters[base]
}

// ElevationWrappers run another command with elevated privileges (§14.3).
var ElevationWrappers = map[string]bool{"sudo": true, "doas": true, "su": true, "runas": true}

// IsElevationWrapper reports whether a program elevates privileges.
func IsElevationWrapper(name string) bool {
	base := strings.ToLower(filepath.Base(name))
	base = strings.TrimSuffix(base, ".exe")
	return ElevationWrappers[base]
}
