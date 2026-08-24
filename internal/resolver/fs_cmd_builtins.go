package resolver

import (
	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/parser"
)

// cmd.exe builtins carry `/`-prefixed switches, so their grammars list those
// rather than the POSIX `-` forms. They map onto the same semantic operations
// as their POSIX counterparts (§15.4).

// cmdDeleteRecognizer models `del` and `erase`, which remove files.
type cmdDeleteRecognizer struct{}

func (cmdDeleteRecognizer) Names() []string { return []string{"del", "erase"} }

var cmdDeleteGrammar = Grammar{
	Safe:          []string{"/p", "/a", "/f", "/q"},
	Semantic:      []string{"/s"},
	SlashSwitches: true,
}

func (cmdDeleteRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := cmdDeleteGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpFSDelete)
	if !args.OK() {
		return Unresolved(req, action.OpFSDelete, args.UnknownReason(req.Command.Name()))
	}
	if len(args.Operands) == 0 {
		return Unresolved(req, action.OpFSDelete, "del was called without a target")
	}

	// /q suppresses the confirmation prompt, which is what /f does for POSIX rm.
	var flags []action.EffectFlag
	if args.Has("/s") {
		flags = append(flags, action.EffectFlagRecursive)
	}
	if args.HasAny("/q", "/f") {
		flags = append(flags, action.EffectFlagForce)
	}

	for _, operand := range args.Operands {
		for _, target := range req.TargetsFor(operand) {
			addEffect(&out, target, action.EffectDelete, flags...)
		}
	}
	return out
}

// cmdRemoveDirRecognizer models `rd` and `rmdir`, which remove directories.
type cmdRemoveDirRecognizer struct{}

func (cmdRemoveDirRecognizer) Names() []string { return []string{"rd", "rmdir"} }

var cmdRemoveDirGrammar = Grammar{
	Safe:          []string{"/q"},
	Semantic:      []string{"/s"},
	SlashSwitches: true,
}

func (cmdRemoveDirRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := cmdRemoveDirGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpFSDelete)
	if !args.OK() {
		return Unresolved(req, action.OpFSDelete, args.UnknownReason(req.Command.Name()))
	}
	if len(args.Operands) == 0 {
		return Unresolved(req, action.OpFSDelete, "rd was called without a directory")
	}

	var flags []action.EffectFlag
	if args.Has("/s") {
		flags = append(flags, action.EffectFlagRecursive)
	}
	if args.Has("/q") {
		flags = append(flags, action.EffectFlagForce)
	}

	for _, operand := range args.Operands {
		for _, target := range req.TargetsFor(operand) {
			addEffect(&out, target, action.EffectDelete, flags...)
		}
	}
	return out
}

// cmdCopyRecognizer models `copy` and `xcopy`.
type cmdCopyRecognizer struct{}

func (cmdCopyRecognizer) Names() []string { return []string{"copy", "xcopy"} }

var cmdCopyGrammar = Grammar{
	Safe:          []string{"/y", "/-y", "/v", "/z", "/i", "/q", "/f", "/h", "/r", "/k"},
	Semantic:      []string{"/s", "/e"},
	SlashSwitches: true,
}

func (cmdCopyRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := cmdCopyGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpFSCopy)
	if !args.OK() {
		return Unresolved(req, action.OpFSCopy, args.UnknownReason(req.Command.Name()))
	}
	if len(args.Operands) < 2 {
		return Unresolved(req, action.OpFSCopy, "copy was called without a source and a destination")
	}

	var flags []action.EffectFlag
	if args.HasAny("/s", "/e") {
		flags = append(flags, action.EffectFlagRecursive)
	}

	last := len(args.Operands) - 1
	for _, operand := range args.Operands[:last] {
		for _, target := range req.TargetsFor(operand) {
			addEffect(&out, target, action.EffectRead, flags...)
		}
	}
	if target, ok := req.TargetFor(args.Operands[last]); ok {
		addEffect(&out, target, action.EffectCreate, flags...)
		addEffect(&out, target, action.EffectWrite, flags...)
	}
	return out
}

// cmdMoveRecognizer models `move`.
type cmdMoveRecognizer struct{}

func (cmdMoveRecognizer) Names() []string { return []string{"move"} }

var cmdMoveGrammar = Grammar{Safe: []string{"/y", "/-y"}, SlashSwitches: true}

func (cmdMoveRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := cmdMoveGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpFSMove)
	if !args.OK() {
		return Unresolved(req, action.OpFSMove, args.UnknownReason(req.Command.Name()))
	}
	if len(args.Operands) < 2 {
		return Unresolved(req, action.OpFSMove, "move was called without a source and a destination")
	}

	last := len(args.Operands) - 1
	for _, operand := range args.Operands[:last] {
		for _, target := range req.TargetsFor(operand) {
			var flags []action.EffectFlag
			if target.IsDir {
				flags = append(flags, action.EffectFlagRecursive)
			}
			addEffect(&out, target, action.EffectDelete, flags...)
		}
	}
	if target, ok := req.TargetFor(args.Operands[last]); ok {
		addEffect(&out, target, action.EffectCreate)
	}
	return out
}

// cmdMakeDirRecognizer models `md` and `mkdir`.
type cmdMakeDirRecognizer struct{}

func (cmdMakeDirRecognizer) Names() []string { return []string{"md"} }

// cmdMakeDirGrammar takes no switches: `md` only names directories.
var cmdMakeDirGrammar = Grammar{SlashSwitches: true}

func (cmdMakeDirRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := cmdMakeDirGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpFSCreate)
	if !args.OK() {
		return Unresolved(req, action.OpFSCreate, args.UnknownReason(req.Command.Name()))
	}
	if len(args.Operands) == 0 {
		return Unresolved(req, action.OpFSCreate, "md was called without a directory")
	}

	for _, operand := range args.Operands {
		for _, target := range req.TargetsFor(operand) {
			addEffect(&out, target, action.EffectCreate)
		}
	}
	return out
}

// cmdTypeRecognizer models `type`, which prints a file.
type cmdTypeRecognizer struct{}

func (cmdTypeRecognizer) Names() []string { return []string{"type"} }

// cmdTypeGrammar takes no switches: `type` only names files.
var cmdTypeGrammar = Grammar{SlashSwitches: true}

func (cmdTypeRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := cmdTypeGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpFSRead)
	if !args.OK() {
		return Unresolved(req, action.OpFSRead, args.UnknownReason(req.Command.Name()))
	}
	for _, operand := range args.Operands {
		for _, target := range req.TargetsFor(operand) {
			addEffect(&out, target, action.EffectRead)
		}
	}
	return out
}

// cmdDirRecognizer models `dir`.
type cmdDirRecognizer struct{}

func (cmdDirRecognizer) Names() []string { return []string{"dir"} }

var cmdDirGrammar = Grammar{
	Safe:          []string{"/b", "/a", "/o", "/p", "/w", "/q", "/n", "/x", "/c", "/l"},
	Semantic:      []string{"/s"},
	SlashSwitches: true,
}

func (cmdDirRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := cmdDirGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpFSRead)
	if !args.OK() {
		return Unresolved(req, action.OpFSRead, args.UnknownReason(req.Command.Name()))
	}

	targets := args.Operands
	if len(targets) == 0 {
		targets = []parser.Word{{Text: "."}}
	}
	var flags []action.EffectFlag
	if args.Has("/s") {
		flags = append(flags, action.EffectFlagRecursive)
	}
	for _, operand := range targets {
		for _, target := range req.TargetsFor(operand) {
			addEffect(&out, target, action.EffectRead, flags...)
		}
	}
	return out
}

// cmdFindStrRecognizer models `findstr`, the cmd grep.
type cmdFindStrRecognizer struct{}

func (cmdFindStrRecognizer) Names() []string { return []string{"findstr"} }

var cmdFindStrGrammar = Grammar{
	Safe:              []string{"/i", "/n", "/v", "/x", "/l", "/r", "/c", "/b", "/e", "/m", "/o", "/p"},
	Semantic:          []string{"/s"},
	PermissiveUnknown: true,
	SlashSwitches:     true,
}

func (cmdFindStrRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := cmdFindStrGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpFSRead)
	if !args.OK() {
		return Unresolved(req, action.OpFSRead, args.UnknownReason(req.Command.Name()))
	}

	// The first operand is the pattern.
	files := args.Operands
	if len(files) == 0 {
		return out
	}
	files = files[1:]

	var flags []action.EffectFlag
	if args.Has("/s") {
		flags = append(flags, action.EffectFlagRecursive)
	}
	for _, operand := range files {
		for _, target := range req.TargetsFor(operand) {
			addEffect(&out, target, action.EffectRead, flags...)
		}
	}
	return out
}
