package action

import "sort"

// Target is one filesystem path a command acts on, after normalization and
// canonicalization (PROTOTYPE_SPEC.md §13.4, §16.1).
type Target struct {
	// Raw is the path as written in the command.
	Raw string `json:"raw"`
	// Canonical is absolute, lexically clean and symlink-resolved. Scope
	// classification uses this path and never the textual form (I-14).
	Canonical string `json:"canonical"`
	// Display is workspace-relative under W, ~-relative under HOME, else absolute.
	// Approvals compare display forms so a moved checkout does not match.
	Display   string       `json:"display"`
	Scope     Scope        `json:"scope"`
	Exists    bool         `json:"exists"`
	IsDir     bool         `json:"is_dir"`
	IsSymlink bool         `json:"is_symlink"`
	Flags     []TargetFlag `json:"flags,omitempty"`
	Status    TargetStatus `json:"status"`
}

// HasFlag reports whether the target carries the given flag.
func (t *Target) HasFlag(f TargetFlag) bool {
	if t == nil {
		return false
	}
	for _, have := range t.Flags {
		if have == f {
			return true
		}
	}
	return false
}

// HasAnyFlag reports whether the target carries at least one of the flags.
func (t *Target) HasAnyFlag(flags ...TargetFlag) bool {
	for _, f := range flags {
		if t.HasFlag(f) {
			return true
		}
	}
	return false
}

// AddFlags adds flags, keeping the set unique and sorted so that serialized
// targets are deterministic.
func (t *Target) AddFlags(flags ...TargetFlag) {
	for _, f := range flags {
		if !t.HasFlag(f) {
			t.Flags = append(t.Flags, f)
		}
	}
	sort.Slice(t.Flags, func(i, j int) bool { return t.Flags[i] < t.Flags[j] })
}

// Ambiguous reports whether the path could not be determined exactly (an
// unexpanded variable or unsupported expansion).
func (t *Target) Ambiguous() bool {
	return t != nil && t.Status == TargetAmbiguous
}
