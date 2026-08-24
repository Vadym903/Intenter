package resolver

import (
	"fmt"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/parser"
)

// WindowsRecognizers returns the PowerShell cmdlets and cmd.exe builtins that
// do the same things as the POSIX tools (§15.4).
//
// They map onto exactly the same semantic operations and effects, which is what
// makes an approval portable: the same script approved on macOS still resolves
// to a DELETE of the same scope when it runs through cmd.exe on Windows.
func WindowsRecognizers() []Recognizer {
	return []Recognizer{
		removeItemRecognizer{}, copyItemRecognizer{}, moveItemRecognizer{},
		newItemRecognizer{}, getContentRecognizer{}, selectStringRecognizer{},
		getChildItemRecognizer{},
		cmdDeleteRecognizer{}, cmdRemoveDirRecognizer{}, cmdCopyRecognizer{},
		cmdMoveRecognizer{}, cmdMakeDirRecognizer{}, cmdTypeRecognizer{},
		cmdDirRecognizer{}, cmdFindStrRecognizer{},
	}
}

// cmdletPathOptions name the parameters that carry paths. PowerShell accepts
// them positionally too, which the operand handling covers.
var cmdletPathOptions = []string{"-Path", "-LiteralPath", "-Destination"}

// cmdletGrammar is shared by the item cmdlets. PowerShell parameter names are
// case-insensitive, which the scan normalizes before lookup.
func cmdletGrammar(extraSafe, extraSemantic []string) Grammar {
	return Grammar{
		Safe: append([]string{
			"-Verbose", "-Debug", "-WhatIf", "-Confirm", "-ErrorAction",
			"-WarningAction", "-InformationAction", "-PassThru", "-Exclude",
			"-Include", "-Filter",
		}, extraSafe...),
		SafeValue: []string{
			"-ErrorAction", "-WarningAction", "-InformationAction",
			"-Exclude", "-Include", "-Filter", "-Encoding",
		},
		Semantic:      append([]string{"-Recurse", "-Force"}, extraSemantic...),
		SemanticValue: cmdletPathOptions,
	}
}

// scanCmdlet normalizes parameter case before scanning, so `-recurse` and
// `-Recurse` are the same option.
func scanCmdlet(grammar Grammar, args []parser.Word) Args {
	normalized := make([]parser.Word, 0, len(args))
	for _, word := range args {
		if strings.HasPrefix(word.Text, "-") {
			word.Text = canonicalParameter(word.Text)
		}
		normalized = append(normalized, word)
	}
	return grammar.Scan(normalized)
}

// canonicalParameter restores the documented casing of a known parameter.
func canonicalParameter(text string) string {
	name, value, hasValue := strings.Cut(text, ":")
	for _, known := range knownParameters {
		if !strings.EqualFold(name, known) {
			continue
		}
		if hasValue {
			return known + ":" + value
		}
		return known
	}
	return text
}

// knownParameters is every parameter the cmdlet recognizers understand.
var knownParameters = []string{
	"-Path", "-LiteralPath", "-Destination", "-Recurse", "-Force", "-Verbose",
	"-Debug", "-WhatIf", "-Confirm", "-ErrorAction", "-WarningAction",
	"-InformationAction", "-PassThru", "-Exclude", "-Include", "-Filter",
	"-ItemType", "-Value", "-Encoding", "-Raw", "-TotalCount", "-Tail",
	"-Pattern", "-SimpleMatch", "-CaseSensitive", "-List", "-Name",
	"-Container", "-Recurse:", "-Force:",
}

// cmdletTargets collects the paths a cmdlet acts on: the positional operands
// plus anything given through -Path or -LiteralPath.
func cmdletTargets(args Args, options ...string) []parser.Word {
	targets := append([]parser.Word(nil), args.Operands...)
	for _, option := range options {
		if args.Has(option) {
			targets = append(targets, args.Value(option))
		}
	}
	return targets
}

// linkItemTypes are the New-Item -ItemType values that create a filesystem
// alias rather than an ordinary file or directory (AG-121).
var linkItemTypes = map[string]bool{
	"symboliclink": true, "junction": true, "hardlink": true,
}

// isLinkItemType reports whether an -ItemType value creates a link.
func isLinkItemType(itemType string) bool {
	return linkItemTypes[strings.ToLower(strings.TrimSpace(itemType))]
}

// removeItemRecognizer models Remove-Item and its aliases (§15.4).
type removeItemRecognizer struct{}

func (removeItemRecognizer) Names() []string { return []string{"Remove-Item"} }

func (removeItemRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := scanCmdlet(cmdletGrammar(nil, nil), req.Command.Args())
	out := resolved(req, action.OpFSDelete)
	if !args.OK() {
		return Unresolved(req, action.OpFSDelete, args.UnknownReason(req.Command.Name()))
	}

	targets := cmdletTargets(args, "-Path", "-LiteralPath")
	if len(targets) == 0 {
		return Unresolved(req, action.OpFSDelete, "Remove-Item was called without a path")
	}

	var flags []action.EffectFlag
	if args.Has("-Recurse") {
		flags = append(flags, action.EffectFlagRecursive)
	}
	if args.Has("-Force") {
		flags = append(flags, action.EffectFlagForce)
	}

	for _, word := range targets {
		for _, target := range req.TargetsFor(word) {
			addEffect(&out, target, action.EffectDelete, flags...)
		}
	}
	return out
}

// copyItemRecognizer models Copy-Item.
type copyItemRecognizer struct{}

func (copyItemRecognizer) Names() []string { return []string{"Copy-Item"} }

func (copyItemRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := scanCmdlet(cmdletGrammar(nil, nil), req.Command.Args())
	out := resolved(req, action.OpFSCopy)
	if !args.OK() {
		return Unresolved(req, action.OpFSCopy, args.UnknownReason(req.Command.Name()))
	}

	sources, destination, ok := cmdletSourcesAndDestination(args)
	if !ok {
		return Unresolved(req, action.OpFSCopy, "Copy-Item was called without a source and a destination")
	}

	var flags []action.EffectFlag
	if args.Has("-Recurse") {
		flags = append(flags, action.EffectFlagRecursive)
	}

	for _, word := range sources {
		for _, target := range req.TargetsFor(word) {
			addEffect(&out, target, action.EffectRead, flags...)
		}
	}
	if target, ok := req.TargetFor(destination); ok {
		addEffect(&out, target, action.EffectCreate, flags...)
		addEffect(&out, target, action.EffectWrite, flags...)
	}
	return out
}

// moveItemRecognizer models Move-Item: the source is removed.
type moveItemRecognizer struct{}

func (moveItemRecognizer) Names() []string { return []string{"Move-Item"} }

func (moveItemRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := scanCmdlet(cmdletGrammar(nil, nil), req.Command.Args())
	out := resolved(req, action.OpFSMove)
	if !args.OK() {
		return Unresolved(req, action.OpFSMove, args.UnknownReason(req.Command.Name()))
	}

	sources, destination, ok := cmdletSourcesAndDestination(args)
	if !ok {
		return Unresolved(req, action.OpFSMove, "Move-Item was called without a source and a destination")
	}

	for _, word := range sources {
		for _, target := range req.TargetsFor(word) {
			var flags []action.EffectFlag
			if target.IsDir {
				flags = append(flags, action.EffectFlagRecursive)
			}
			addEffect(&out, target, action.EffectDelete, flags...)
		}
	}
	if target, ok := req.TargetFor(destination); ok {
		addEffect(&out, target, action.EffectCreate)
	}
	return out
}

// cmdletSourcesAndDestination splits the paths of a copy or move.
func cmdletSourcesAndDestination(args Args) ([]parser.Word, parser.Word, bool) {
	if args.Has("-Destination") {
		sources := cmdletTargets(args, "-Path", "-LiteralPath")
		if len(sources) == 0 {
			return nil, parser.Word{}, false
		}
		return sources, args.Value("-Destination"), true
	}
	if len(args.Operands) < 2 {
		return nil, parser.Word{}, false
	}
	last := len(args.Operands) - 1
	return args.Operands[:last], args.Operands[last], true
}

// newItemRecognizer models New-Item, which creates a file or a directory.
type newItemRecognizer struct{}

func (newItemRecognizer) Names() []string { return []string{"New-Item"} }

func (newItemRecognizer) Recognize(req Request) action.ResolvedCommand {
	grammar := cmdletGrammar(nil, nil)
	grammar.SafeValue = append(grammar.SafeValue, "-ItemType", "-Value", "-Name")

	args := scanCmdlet(grammar, req.Command.Args())
	out := resolved(req, action.OpFSCreate)
	if !args.OK() {
		return Unresolved(req, action.OpFSCreate, args.UnknownReason(req.Command.Name()))
	}

	// A symbolic link, junction or hard link is a new filesystem alias, not an
	// ordinary file: the -Value it is given (where it points) is nowhere in
	// the modeled effects, and an approval for "create a file here" was never
	// a promise to also let the agent alias this path to anywhere else,
	// including outside the workspace. POSIX draws the same line by not
	// modeling `ln` at all, so it is always asked; New-Item should be no less
	// cautious just because the item type is a parameter instead of a
	// different program name.
	if itemType := args.Value("-ItemType").Text; isLinkItemType(itemType) {
		return Unresolved(req, action.OpFSCreate,
			fmt.Sprintf("New-Item -ItemType %s creates a filesystem link, which Intenter does not model", itemType))
	}

	targets := cmdletTargets(args, "-Path", "-LiteralPath")
	if len(targets) == 0 {
		return Unresolved(req, action.OpFSCreate, "New-Item was called without a path")
	}
	for _, word := range targets {
		for _, target := range req.TargetsFor(word) {
			addEffect(&out, target, action.EffectCreate)
		}
	}
	return out
}

// getContentRecognizer models Get-Content.
type getContentRecognizer struct{}

func (getContentRecognizer) Names() []string { return []string{"Get-Content"} }

func (getContentRecognizer) Recognize(req Request) action.ResolvedCommand {
	grammar := cmdletGrammar([]string{"-Raw"}, nil)
	grammar.SafeValue = append(grammar.SafeValue, "-TotalCount", "-Tail")

	args := scanCmdlet(grammar, req.Command.Args())
	out := resolved(req, action.OpFSRead)
	if !args.OK() {
		return Unresolved(req, action.OpFSRead, args.UnknownReason(req.Command.Name()))
	}

	for _, word := range cmdletTargets(args, "-Path", "-LiteralPath") {
		for _, target := range req.TargetsFor(word) {
			addEffect(&out, target, action.EffectRead)
		}
	}
	return out
}

// selectStringRecognizer models Select-String, the PowerShell grep.
type selectStringRecognizer struct{}

func (selectStringRecognizer) Names() []string { return []string{"Select-String"} }

func (selectStringRecognizer) Recognize(req Request) action.ResolvedCommand {
	grammar := cmdletGrammar([]string{"-SimpleMatch", "-CaseSensitive", "-List", "-Raw"}, nil)
	grammar.SafeValue = append(grammar.SafeValue, "-Pattern")

	args := scanCmdlet(grammar, req.Command.Args())
	out := resolved(req, action.OpFSRead)
	if !args.OK() {
		return Unresolved(req, action.OpFSRead, args.UnknownReason(req.Command.Name()))
	}

	// Without -Pattern the first positional argument is the pattern.
	files := cmdletTargets(args, "-Path", "-LiteralPath")
	if !args.Has("-Pattern") && len(args.Operands) > 0 {
		files = files[1:]
	}

	var flags []action.EffectFlag
	if args.Has("-Recurse") {
		flags = append(flags, action.EffectFlagRecursive)
	}
	for _, word := range files {
		for _, target := range req.TargetsFor(word) {
			addEffect(&out, target, action.EffectRead, flags...)
		}
	}
	return out
}

// getChildItemRecognizer models Get-ChildItem, the PowerShell ls.
type getChildItemRecognizer struct{}

func (getChildItemRecognizer) Names() []string { return []string{"Get-ChildItem"} }

func (getChildItemRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := scanCmdlet(cmdletGrammar([]string{"-Name", "-Container"}, nil), req.Command.Args())
	out := resolved(req, action.OpFSRead)
	if !args.OK() {
		return Unresolved(req, action.OpFSRead, args.UnknownReason(req.Command.Name()))
	}

	targets := cmdletTargets(args, "-Path", "-LiteralPath")
	if len(targets) == 0 {
		targets = []parser.Word{{Text: "."}}
	}

	var flags []action.EffectFlag
	if args.Has("-Recurse") {
		flags = append(flags, action.EffectFlagRecursive)
	}
	for _, word := range targets {
		for _, target := range req.TargetsFor(word) {
			addEffect(&out, target, action.EffectRead, flags...)
		}
	}
	return out
}
