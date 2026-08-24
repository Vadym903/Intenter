package resolver

import (
	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/parser"
	cmdshell "github.com/Vadym903/Intenter/internal/parser/cmd"
	"github.com/Vadym903/Intenter/internal/parser/posix"
	"github.com/Vadym903/Intenter/internal/parser/powershell"
)

// NewParserRegistry builds the dialect registry with every parser Intenter
// ships. The Windows dialects parse text, so they are registered on every OS
// (§14.4); a dialect without an implementation yet is simply absent and makes
// the action UNSUPPORTED_SYNTAX rather than being guessed at.
func NewParserRegistry() *parser.Registry {
	registry := parser.NewRegistry()
	registry.Register(posix.New())
	registry.Register(powershell.New())
	registry.Register(cmdshell.New())
	return registry
}

// NewRecognizerRegistry builds the recognizer set of §15.4.
func NewRecognizerRegistry() *Registry {
	registry := NewRegistry()
	registry.Register(FilesystemRecognizers()...)
	registry.Register(WindowsRecognizers()...)
	registry.Register(JSTestRecognizers()...)
	registry.Register(GitRecognizer())
	registry.Register(NpmRecognizer())
	registry.Register(GradleRecognizer())
	registry.Register(MavenRecognizer())
	registry.Register(CurlRecognizer())
	return registry
}

// DialectFor returns the dialect a request should be parsed with, falling back
// to the platform default when the adapter did not name one.
func DialectFor(requested action.Dialect, fallback action.Dialect) action.Dialect {
	if requested != "" {
		return requested
	}
	if fallback != "" {
		return fallback
	}
	return action.DialectPosix
}
