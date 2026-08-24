package resolver

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/parser"
)

// CurlRecognizer models `curl` (§15.4). It is the one recognizer whose targets
// are mostly remote: what matters is which host is contacted, with what method,
// and whether the command carries a credential or disables TLS verification.
func CurlRecognizer() Recognizer { return curlRecognizer{} }

type curlRecognizer struct{}

func (curlRecognizer) Names() []string { return []string{"curl"} }

var curlGrammar = Grammar{
	Safe: []string{
		"-s", "--silent", "-S", "--show-error", "-L", "--location", "-f", "--fail",
		"-v", "--verbose", "-i", "--include", "-I", "--head", "--compressed",
		"--http1.1", "--http2", "-4", "--ipv4", "-6", "--ipv6",
		"--fail-with-body", "-g", "--globoff", "-N", "--no-buffer",
		"-#", "--progress-bar", "--no-progress-meter",
		"-O", "--remote-name", "-J", "--remote-header-name",
		"-k", "--insecure", "--tlsv1.2", "--tlsv1.3",
	},
	SafeValue: []string{
		"-A", "--user-agent", "-e", "--referer",
		"--retry", "--retry-delay", "--retry-max-time", "--max-time",
		"--connect-timeout", "--proto", "--max-redirs",
	},
	SafePrefixes: []string{"--retry"},
	// Every option below either reads a file, writes one, carries a credential,
	// or changes where the request goes; each is modeled explicitly in
	// Recognize. Options that redirect the connection away from the named host
	// (--resolve, --connect-to, --interface, --proxy, --unix-socket) are left
	// out altogether so they refuse: an approval for api.example.com must not
	// cover a request that resolves that name to an attacker's address.
	SemanticValue: []string{
		"-X", "--request", "-d", "--data", "--data-raw", "--data-binary",
		"--data-urlencode", "--data-ascii", "--json", "-F", "--form",
		"--form-string", "-T", "--upload-file", "-o", "--output",
		"-u", "--user", "--oauth2-bearer", "-K", "--config",
		"-H", "--header", "-w", "--write-out",
		"-b", "--cookie", "-c", "--cookie-jar",
	},
	// curl accepts clustered short options, e.g. `-sSL` and `-XPOST`.
	Cluster: true,
}

// curlCredentialHeaders are request headers whose value is a secret. A command
// that spells one out carries an inline credential (hard rule R10), the same
// as `-u user:pass`.
var curlCredentialHeaders = []string{"authorization:", "proxy-authorization:", "cookie:", "x-api-key:"}

func (curlRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := curlGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpHTTPRequest)

	if !args.OK() {
		return Unresolved(req, action.OpHTTPRequest, args.UnknownReason(req.Command.Name()))
	}
	// A config file can carry any option at all, including another URL.
	if args.HasAny("-K", "--config") {
		return Unresolved(req, action.OpHTTPRequest,
			"curl --config reads options from a file Intenter has not read")
	}
	if len(args.Operands) == 0 {
		return Unresolved(req, action.OpHTTPRequest, "curl was called without a URL")
	}

	method := curlMethod(args)
	var flags []action.EffectFlag
	if args.HasAny("-u", "--user", "--oauth2-bearer") || curlHasCredentialHeader(args) {
		flags = append(flags, action.EffectFlagInlineCredential)
	}
	if args.HasAny("-k", "--insecure") {
		flags = append(flags, action.EffectFlagInsecureTLS)
	}

	for _, operand := range args.Operands {
		if operand.ContainsUnexpandedVar {
			return Unresolved(req, action.OpHTTPRequest,
				"the URL depends on a variable Intenter cannot expand")
		}
		target, ok := parseCurlURL(operand.Text)
		if !ok {
			return Unresolved(req, action.OpHTTPRequest,
				"curl was given a URL Intenter cannot parse: "+operand.Text)
		}
		target.Method = method

		effect := action.Effect{Type: action.EffectNetwork, Network: &target}
		effect.AddFlags(flags...)
		out.Effects = append(out.Effects, effect)
	}

	// A request body read from a file, and any downloaded output, are ordinary
	// filesystem effects and must be modeled as such. Every option is
	// repeatable, so all values are read, not just the last.
	for _, value := range args.All("-T", "--upload-file") {
		for _, target := range req.TargetsFor(value) {
			addEffect(&out, target, action.EffectRead)
		}
	}
	for _, value := range args.All("-d", "--data", "--data-binary", "--data-ascii", "--json") {
		if path, ok := strings.CutPrefix(value.Text, "@"); ok && path != "" {
			for _, target := range req.TargetsFor(parser.Word{Text: path}) {
				addEffect(&out, target, action.EffectRead)
			}
		}
	}
	// --data-urlencode reads a file in its `@file`, `name@file` forms; -F reads
	// one in `name=@file` and `name=<file`; -w reads its format from `@file`.
	for _, value := range args.All("--data-urlencode") {
		if path := curlDataURLEncodeFile(value.Text); path != "" {
			for _, target := range req.TargetsFor(parser.Word{Text: path}) {
				addEffect(&out, target, action.EffectRead)
			}
		}
	}
	for _, value := range args.All("-F", "--form") {
		if path := curlFormFile(value.Text); path != "" {
			for _, target := range req.TargetsFor(parser.Word{Text: path}) {
				addEffect(&out, target, action.EffectRead)
			}
		}
	}
	for _, value := range args.All("-w", "--write-out") {
		if path, ok := strings.CutPrefix(value.Text, "@"); ok && path != "" && path != "-" {
			for _, target := range req.TargetsFor(parser.Word{Text: path}) {
				addEffect(&out, target, action.EffectRead)
			}
		}
	}
	// -b/--cookie names a file unless the value is a literal cookie string
	// (curl decides by the presence of `=`); -c/--cookie-jar always writes one.
	for _, value := range args.All("-b", "--cookie") {
		if !strings.Contains(value.Text, "=") && value.Text != "" && value.Text != "-" {
			for _, target := range req.TargetsFor(value) {
				addEffect(&out, target, action.EffectRead)
			}
		}
	}
	for _, value := range args.All("-c", "--cookie-jar", "-o", "--output") {
		if value.Text == "-" {
			continue
		}
		if target, ok := req.TargetFor(value); ok {
			addEffect(&out, target, action.EffectCreate)
			addEffect(&out, target, action.EffectWrite)
		}
	}
	if args.HasAny("-O", "--remote-name") {
		// The file lands in the working directory under the URL's own name.
		if target, ok := req.PathTarget(req.Command.EffectiveCwd); ok {
			addEffect(&out, target, action.EffectCreate)
			addEffect(&out, target, action.EffectWrite)
		}
	}
	return out
}

// curlHasCredentialHeader reports whether any -H value spells out a secret.
func curlHasCredentialHeader(args Args) bool {
	for _, header := range args.All("-H", "--header") {
		lowered := strings.ToLower(strings.TrimSpace(header.Text))
		for _, name := range curlCredentialHeaders {
			if strings.HasPrefix(lowered, name) {
				return true
			}
		}
	}
	return false
}

// curlFormFile returns the file a -F/--form value uploads (`name=@path` or
// `name=<path`), stripping the `;type=…` suffix, or "" for a literal value.
func curlFormFile(value string) string {
	_, rest, found := strings.Cut(value, "=")
	if !found || rest == "" || (rest[0] != '@' && rest[0] != '<') {
		return ""
	}
	path, _, _ := strings.Cut(rest[1:], ";")
	return strings.Trim(path, `"`)
}

// curlDataURLEncodeFile returns the file a --data-urlencode value reads:
// `@path` and `name@path` do, `=content` and `name=content` do not.
func curlDataURLEncodeFile(value string) string {
	if strings.HasPrefix(value, "=") {
		return ""
	}
	name, rest, found := strings.Cut(value, "@")
	if !found || rest == "" || strings.Contains(name, "=") {
		return ""
	}
	return rest
}

// curlMethod determines the request method: the explicit one when given, else
// the one the body flags imply (§15.4).
func curlMethod(args Args) string {
	for _, option := range []string{"-X", "--request"} {
		if args.Has(option) {
			return strings.ToUpper(args.Value(option).Text)
		}
	}
	switch {
	case args.HasAny("-T", "--upload-file"):
		return "PUT"
	case args.HasAny("-d", "--data", "--data-raw", "--data-binary", "--data-ascii",
		"--data-urlencode", "--json", "-F", "--form", "--form-string"):
		return "POST"
	case args.HasAny("-I", "--head"):
		return "HEAD"
	}
	return "GET"
}

// parseCurlURL turns a URL argument into a network target. A scheme-less URL is
// http, which is what curl assumes.
func parseCurlURL(raw string) (action.NetworkTarget, bool) {
	text := raw
	if !strings.Contains(text, "://") {
		text = "http://" + text
	}

	parsed, err := url.Parse(text)
	if err != nil || parsed.Host == "" {
		return action.NetworkTarget{}, false
	}

	target := action.NetworkTarget{Scheme: parsed.Scheme, Host: parsed.Hostname()}
	if port := parsed.Port(); port != "" {
		if number, err := strconv.Atoi(port); err == nil {
			target.Port = number
		}
	}
	return target, true
}
