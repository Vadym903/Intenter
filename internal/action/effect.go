package action

import (
	"sort"
	"strconv"
	"strings"
)

// Effect is one low-level consequence of a command: a filesystem operation on a
// target, a network request, or the execution of a program whose behavior is
// declared or unknown (PROTOTYPE_SPEC.md §13.5, §17.1).
type Effect struct {
	Type    EffectType     `json:"type"`
	Target  *Target        `json:"target,omitempty"`
	Network *NetworkTarget `json:"network,omitempty"`
	Program *ProgramRef    `json:"program,omitempty"`
	Flags   []EffectFlag   `json:"flags,omitempty"`
}

// NetworkTarget identifies a remote endpoint, either concretely (host/port/
// scheme/method) or by declared category for build tools.
type NetworkTarget struct {
	Host   string `json:"host,omitempty"`
	Port   int    `json:"port,omitempty"`
	Scheme string `json:"scheme,omitempty"`
	Method string `json:"method,omitempty"`
	// DeclaredKind categorizes network access declared by convention, e.g.
	// "dependency-registry" or "publish" (PROTOTYPE_SPEC.md §17.3).
	DeclaredKind string `json:"declared_kind,omitempty"`
}

// Key is the comparison form used by approval matching (§20.3 rule 5).
func (n NetworkTarget) Key() string {
	if n.DeclaredKind != "" && n.Host == "" {
		return "declared:" + n.DeclaredKind
	}
	return strings.Join([]string{n.Scheme, n.Host, portString(n.Port), n.Method, n.DeclaredKind}, "|")
}

// String renders a network target for explanations.
func (n NetworkTarget) String() string {
	if n.Host == "" {
		return "declared:" + n.DeclaredKind
	}
	out := n.Host
	if n.Scheme != "" {
		out = n.Scheme + "://" + out
	}
	if n.Port != 0 {
		out += ":" + portString(n.Port)
	}
	if n.Method != "" {
		out = n.Method + " " + out
	}
	return out
}

func portString(port int) string {
	if port == 0 {
		return ""
	}
	return strconv.Itoa(port)
}

// ProgramRef describes a program an action executes.
type ProgramRef struct {
	Name       string            `json:"name"`
	Resolution ProgramResolution `json:"resolution"`
	Elevated   bool              `json:"elevated,omitempty"`
	// Streamed marks a program whose input is piped from another command, so
	// what it executes is content that was just fetched or generated. Hard rule
	// R12 fires on exactly this case and needs to tell it apart from an
	// ordinary unknown executable.
	Streamed bool `json:"streamed,omitempty"`
}

// HasFlag reports whether the effect carries the given flag.
func (e *Effect) HasFlag(f EffectFlag) bool {
	if e == nil {
		return false
	}
	for _, have := range e.Flags {
		if have == f {
			return true
		}
	}
	return false
}

// HasAnyFlag reports whether the effect carries at least one of the flags.
func (e *Effect) HasAnyFlag(flags ...EffectFlag) bool {
	for _, f := range flags {
		if e.HasFlag(f) {
			return true
		}
	}
	return false
}

// AddFlags adds flags, keeping the set unique and sorted.
func (e *Effect) AddFlags(flags ...EffectFlag) {
	for _, f := range flags {
		if !e.HasFlag(f) {
			e.Flags = append(e.Flags, f)
		}
	}
	sort.Slice(e.Flags, func(i, j int) bool { return e.Flags[i] < e.Flags[j] })
}

// EnvelopeEntry is the (effect type, scope, flags, target flags) tuple an
// approval permits. Flags are part of the identity: DELETE/WORKSPACE_GENERATED/
// {recursive} does not cover {recursive,wildcard} (PROTOTYPE_SPEC.md §20.3
// rule 4).
type EnvelopeEntry struct {
	Type  EffectType   `json:"type"`
	Scope Scope        `json:"scope,omitempty"`
	Flags []EffectFlag `json:"flags,omitempty"`
	// TargetFlags carries the target's own classification (sensitive,
	// traversal, symlink_escape, tool_cache, broad, wildcard, network_path,
	// temp) into the comparison key. Without this, a target that gains one of
	// these after an approval was granted — e.g. a plain HOME write approval
	// extending to a write inside a tool cache, which nothing else in the
	// matcher watches for — would look like the exact same envelope entry
	// (I-1; only R2/R3/R5/R6 give some of these flags hard-rule coverage, and
	// only for specific effect types and scopes, so the matcher must not rely
	// on that alone).
	TargetFlags []TargetFlag `json:"target_flags,omitempty"`
	// Program is set for EXECUTE entries so a DECLARED execution envelope never
	// covers an UNRESOLVED one.
	Program ProgramResolution `json:"program,omitempty"`
}

// Key is the canonical comparison string for an envelope entry.
func (e EnvelopeEntry) Key() string {
	flags := append([]EffectFlag(nil), e.Flags...)
	sort.Slice(flags, func(i, j int) bool { return flags[i] < flags[j] })
	parts := make([]string, 0, len(flags))
	for _, f := range flags {
		parts = append(parts, string(f))
	}
	targetFlags := append([]TargetFlag(nil), e.TargetFlags...)
	sort.Slice(targetFlags, func(i, j int) bool { return targetFlags[i] < targetFlags[j] })
	targetParts := make([]string, 0, len(targetFlags))
	for _, f := range targetFlags {
		targetParts = append(targetParts, string(f))
	}
	key := string(e.Type) + "/" + string(e.Scope)
	if e.Program != "" {
		key += "/" + string(e.Program)
	}
	return key + "{" + strings.Join(parts, ",") + "}[" + strings.Join(targetParts, ",") + "]"
}

// String renders an envelope entry for explanations, e.g.
// "DELETE(recursive,force) WORKSPACE_GENERATED".
func (e EnvelopeEntry) String() string {
	out := string(e.Type)
	if len(e.Flags) > 0 {
		flags := make([]string, 0, len(e.Flags))
		for _, f := range e.Flags {
			flags = append(flags, string(f))
		}
		sort.Strings(flags)
		out += "(" + strings.Join(flags, ",") + ")"
	}
	if e.Program != "" {
		out += "[" + string(e.Program) + "]"
	}
	if e.Scope != "" {
		out += " " + string(e.Scope)
	}
	return out
}

// EnvelopeEntry projects an effect onto the (type, scope, flags) triple used by
// approval matching. Network effects have no scope; their identity is the
// NetworkTarget compared separately (§20.3 rule 5).
func (e *Effect) EnvelopeEntry() EnvelopeEntry {
	entry := EnvelopeEntry{Type: e.Type, Flags: append([]EffectFlag(nil), e.Flags...)}
	if e.Target != nil {
		entry.Scope = e.Target.Scope
		entry.TargetFlags = append([]TargetFlag(nil), e.Target.Flags...)
	}
	if e.Program != nil {
		entry.Program = e.Program.Resolution
	}
	sort.Slice(entry.Flags, func(i, j int) bool { return entry.Flags[i] < entry.Flags[j] })
	sort.Slice(entry.TargetFlags, func(i, j int) bool { return entry.TargetFlags[i] < entry.TargetFlags[j] })
	return entry
}

// Envelope projects a list of effects onto a deduplicated, sorted envelope.
func Envelope(effects []Effect) []EnvelopeEntry {
	seen := make(map[string]bool, len(effects))
	out := make([]EnvelopeEntry, 0, len(effects))
	for i := range effects {
		entry := effects[i].EnvelopeEntry()
		key := entry.Key()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}

// NetworkTargets extracts the deduplicated, sorted network targets of a list of
// effects.
func NetworkTargets(effects []Effect) []NetworkTarget {
	seen := make(map[string]bool)
	out := make([]NetworkTarget, 0)
	for i := range effects {
		n := effects[i].Network
		if n == nil {
			continue
		}
		if seen[n.Key()] {
			continue
		}
		seen[n.Key()] = true
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}
