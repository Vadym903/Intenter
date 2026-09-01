package claude

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
)

// claudeSeparators are the tokens Claude splits a Bash command on before
// matching each part against its rules (§11.6).
var claudeSeparators = []string{"&&", "||", "|&", ";", "|", "&", "\n"}

// claudeWrappers are prefixes Claude strips before matching. `xargs` is only
// stripped when bare.
var claudeWrappers = map[string]bool{
	"timeout": true, "time": true, "nice": true, "nohup": true,
	"stdbuf": true, "command": true, "builtin": true, "noglob": true,
}

// Rule is one permission rule from a settings file.
type Rule struct {
	Scope Scope
	Tool  string
	// Content is the text inside the parentheses; empty for a bare tool rule.
	Content string
	// Raw is the rule exactly as written, for the stored rule key.
	Raw string
}

// Key identifies a rule in `agent_rule_imports`, e.g.
// "local:Bash(npm run cleanup)". It includes the scope so the same text in two
// files is imported at most once each.
func (r Rule) Key() string { return string(r.Scope) + ":" + r.Raw }

// ParseRule reads one settings entry, e.g. `Bash(npm run test:*)`.
func ParseRule(scope Scope, raw string) (Rule, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Rule{}, false
	}

	open := strings.Index(raw, "(")
	if open < 0 {
		// A bare tool name permits everything that tool can do.
		return Rule{Scope: scope, Tool: raw, Content: "*", Raw: raw}, true
	}
	if !strings.HasSuffix(raw, ")") {
		return Rule{}, false
	}
	tool := strings.TrimSpace(raw[:open])
	if tool == "" {
		return Rule{}, false
	}
	return Rule{Scope: scope, Tool: tool, Content: raw[open+1 : len(raw)-1], Raw: raw}, true
}

// AllowRules collects the allow rules for one tool, in file precedence order.
func AllowRules(files []SettingsFile, tool string) []Rule {
	var rules []Rule
	for _, file := range files {
		for _, raw := range file.Permissions.Allow {
			rule, ok := ParseRule(file.Scope, raw)
			if !ok || rule.Tool != tool {
				continue
			}
			rules = append(rules, rule)
		}
	}
	return rules
}

// ProjectRule is one allow rule the agent holds, together with the file it
// lives in and whether this user can change it here.
//
// A rule like this lets a command run without a prompt whether or not Intenter
// ever imported it, so anything that reports what a project trusts has to
// include them or it is under-reporting.
type ProjectRule struct {
	Rule
	// Path is the settings file the rule was read from.
	Path string
	// Changeable reports whether Intenter may edit that file on this user's
	// behalf.
	Changeable bool
	// Reason says why it may not, when it may not.
	Reason string
}

// ProjectRules lists the allow rules for one tool that apply to a project
// directory, and the files whose rules could not be read.
//
// An unreadable file is reported rather than skipped: "this project has no
// rules" and "we could not tell what rules this project has" are different
// answers, and only one of them is safe to show as a complete list.
func ProjectRules(reader *SettingsReader, projectDir, tool string) (rules []ProjectRule, unreadable []string) {
	for _, file := range reader.Discover(projectDir) {
		if file.Unreadable {
			unreadable = append(unreadable, file.Path)
			continue
		}
		for _, raw := range file.Permissions.Allow {
			rule, ok := ParseRule(file.Scope, raw)
			if !ok || rule.Tool != tool {
				continue
			}
			changeable, reason := ruleChangeable(file.Scope)
			rules = append(rules, ProjectRule{
				Rule:       rule,
				Path:       file.Path,
				Changeable: changeable,
				Reason:     reason,
			})
		}
	}
	return rules, unreadable
}

// ruleChangeable decides whether Intenter may take a rule out of a file.
//
// Managed settings are an administrator's policy and are never edited. A
// project file is normally shared through the repository, so removing a rule
// from it changes what everyone on the team is allowed to do; that needs the
// user to name the file, not a default.
func ruleChangeable(scope Scope) (bool, string) {
	switch scope {
	case ScopeManaged:
		return false, "an administrator's managed policy file, which Intenter never edits"
	case ScopeProject:
		return false, "shared through the repository, so removing it would change what everyone is allowed to do"
	default:
		return true, ""
	}
}

// Consent reports the persistent permission Claude already holds for a raw
// command, or nil when Intenter cannot be certain it does (§11.6).
//
// The signal is only ever used for a validated, once-only import (§19.5), never
// to allow anything by itself (INVARIANT I-8). Every uncertainty — a separator
// inside quotes, a leading environment assignment, a subcommand no rule covers
// — yields no consent at all.
func Consent(tool, rawCommand string, files []SettingsFile) *action.AgentConsent {
	rules := AllowRules(files, tool)
	if len(rules) == 0 {
		return nil
	}

	subcommands, certain := splitCommand(rawCommand)
	if !certain || len(subcommands) == 0 {
		return nil
	}

	keys := make(map[string]bool)
	exact := true

	for _, subcommand := range subcommands {
		stripped, usable := stripWrappers(subcommand)
		if !usable {
			return nil
		}
		rule, matchExact, ok := matchRule(rules, stripped)
		if !ok {
			// One uncovered part means Claude would still prompt.
			return nil
		}
		keys[rule.Key()] = true
		exact = exact && matchExact
	}

	ruleKeys := make([]string, 0, len(keys))
	for key := range keys {
		ruleKeys = append(ruleKeys, key)
	}
	sort.Strings(ruleKeys)

	return &action.AgentConsent{
		Kind:     action.ConsentKindPersistentRule,
		RuleKeys: ruleKeys,
		Exact:    exact,
	}
}

// matchRule finds the first allow rule covering a subcommand, and whether the
// match was literal rather than through a pattern.
func matchRule(rules []Rule, subcommand string) (Rule, bool, bool) {
	for _, rule := range rules {
		if rule.Content == subcommand {
			return rule, true, true
		}
		if matchContent(rule.Content, subcommand) {
			return rule, false, true
		}
	}
	return Rule{}, false, false
}

// matchContent applies Claude's rule grammar to one subcommand: `*` matches any
// sequence including spaces, a trailing ` *` (or the `:*` shorthand) requires a
// word boundary (§11.6).
func matchContent(content, subcommand string) bool {
	if content == "*" {
		return true
	}

	if prefix, ok := strings.CutSuffix(content, ":*"); ok {
		return matchPrefixBoundary(prefix, subcommand)
	}
	if prefix, ok := strings.CutSuffix(content, " *"); ok {
		return matchPrefixBoundary(prefix, subcommand)
	}
	return globMatch(content, subcommand)
}

// matchPrefixBoundary implements the word-boundary form: the subcommand is the
// prefix itself, or the prefix followed by a space and anything.
func matchPrefixBoundary(prefix, subcommand string) bool {
	if strings.Contains(prefix, "*") {
		return globMatch(prefix, subcommand) || globMatch(prefix+" *", subcommand)
	}
	return subcommand == prefix || strings.HasPrefix(subcommand, prefix+" ")
}

// globMatch matches a pattern where `*` stands for any sequence of characters,
// spaces included.
func globMatch(pattern, text string) bool {
	if !strings.Contains(pattern, "*") {
		return pattern == text
	}

	parts := strings.Split(pattern, "*")
	if !strings.HasPrefix(text, parts[0]) {
		return false
	}
	text = text[len(parts[0]):]

	last := len(parts) - 1
	for i := 1; i < last; i++ {
		index := strings.Index(text, parts[i])
		if index < 0 {
			return false
		}
		text = text[index+len(parts[i]):]
	}
	if last == 0 {
		return true
	}
	return strings.HasSuffix(text, parts[last])
}

// splitCommand splits a raw command the way Claude does, and reports whether
// the split is certain.
//
// Claude splits on its separators without regard for quoting. When a separator
// appears inside quotes the two readings differ, and Intenter refuses to
// guess which one Claude used: uncertainty yields no consent (§11.6).
func splitCommand(rawCommand string) ([]string, bool) {
	if separatorInsideQuotes(rawCommand) {
		return nil, false
	}

	parts := []string{rawCommand}
	for _, separator := range claudeSeparators {
		var next []string
		for _, part := range parts {
			next = append(next, strings.Split(part, separator)...)
		}
		parts = next
	}

	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out, true
}

// separatorInsideQuotes reports whether any Claude separator occurs inside a
// quoted section, which makes the split ambiguous.
func separatorInsideQuotes(text string) bool {
	var quote byte
	for i := 0; i < len(text); i++ {
		char := text[i]
		switch {
		case quote != 0 && char == quote:
			quote = 0
		case quote != 0:
			if char == '&' || char == '|' || char == ';' || char == '\n' {
				return true
			}
		case char == '\'' || char == '"':
			quote = char
		}
	}
	return false
}

// stripWrappers removes the prefixes Claude strips before matching, and reports
// whether the remainder can be matched at all. A leading environment
// assignment makes the match uncertain (§11.6).
func stripWrappers(subcommand string) (string, bool) {
	current := strings.TrimSpace(subcommand)

	for {
		fields := strings.Fields(current)
		if len(fields) == 0 {
			return "", false
		}
		head := fields[0]

		if isEnvAssignment(head) {
			return "", false
		}
		if head == "xargs" && len(fields) > 1 {
			// Only a bare `xargs` is stripped; with options it changes what runs.
			return current, true
		}
		if head == "xargs" {
			return "", false
		}
		if !claudeWrappers[head] {
			return current, true
		}

		rest := strings.TrimSpace(strings.TrimPrefix(current, head))
		if rest == "" || rest == current {
			return "", false
		}
		current = rest
	}
}

// isEnvAssignment reports the `KEY=value` prefix form.
func isEnvAssignment(word string) bool {
	index := strings.Index(word, "=")
	if index <= 0 {
		return false
	}
	for i := 0; i < index; i++ {
		char := word[i]
		switch {
		case char == '_':
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// DenyMatch reports the first deny or ask rule that visibly covers a command.
// Claude enforces its own deny and ask rules regardless of what a hook returns,
// so this is only used to explain a decision, never to make one (§11.6).
func DenyMatch(tool, rawCommand string, files []SettingsFile) (string, bool) {
	subcommands, certain := splitCommand(rawCommand)
	if !certain {
		return "", false
	}

	for _, file := range files {
		for _, list := range [][]string{file.Permissions.Deny, file.Permissions.Ask} {
			for _, raw := range list {
				rule, ok := ParseRule(file.Scope, raw)
				if !ok || rule.Tool != tool {
					continue
				}
				for _, subcommand := range subcommands {
					stripped, usable := stripWrappers(subcommand)
					if !usable {
						continue
					}
					if stripped == rule.Content || matchContent(rule.Content, stripped) {
						return rule.Key(), true
					}
				}
			}
		}
	}
	return "", false
}

// String renders a rule for diagnostics.
func (r Rule) String() string { return fmt.Sprintf("%s:%s", r.Scope, r.Raw) }
