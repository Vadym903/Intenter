package resolver

import (
	"fmt"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/parser"
)

// FilesystemRecognizers returns the POSIX filesystem tools of §15.4.
func FilesystemRecognizers() []Recognizer {
	return append([]Recognizer{
		removeRecognizer{},
		copyRecognizer{},
		moveRecognizer{},
		makeDirRecognizer{},
		catRecognizer{},
		grepRecognizer{},
		findRecognizer{},
	}, ReadOnlyRecognizers()...)
}

// ReadOnlyRecognizers returns the commands that only ever look at things.
//
// They matter because of the baseline: a command Intenter does not model
// falls through to a prompt, so an unmodeled `ls` would make the agent ask
// about listing a directory. Modeling them is what keeps the prompts meaningful
// (§18.3 B1, §15.4).
func ReadOnlyRecognizers() []Recognizer {
	return []Recognizer{
		readFileRecognizer{},
		listRecognizer{},
		noopRecognizer{},
	}
}

// readFileRecognizer models the commands that read files and print them.
type readFileRecognizer struct{}

func (readFileRecognizer) Names() []string {
	return []string{"head", "tail", "wc", "less", "more", "nl", "od", "xxd", "file", "stat", "du"}
}

// readFileGrammar accepts unlisted options: none of these commands can be made
// to write, so an unknown flag cannot widen what they do.
var readFileGrammar = Grammar{
	SafeValue: []string{
		"-n", "--lines", "-c", "--bytes", "-q", "--quiet", "--max-depth",
		"-w", "--width", "-t", "--format", "-s",
	},
	PermissiveUnknown: true,
	Cluster:           true,
	SafeNumericShort:  true,
}

func (readFileRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := readFileGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpFSRead)
	if !args.OK() {
		return Unresolved(req, action.OpFSRead, args.UnknownReason(req.Command.Name()))
	}

	for _, operand := range args.Operands {
		if operand.Text == "-" {
			continue
		}
		for _, target := range req.TargetsFor(operand) {
			addEffect(&out, target, action.EffectRead)
		}
	}
	return out
}

// listRecognizer models directory listings.
type listRecognizer struct{}

func (listRecognizer) Names() []string { return []string{"ls", "tree"} }

var listGrammar = Grammar{
	Semantic:          []string{"-R", "--recursive"},
	SafeValue:         []string{"-L", "--level", "-I", "--ignore"},
	PermissiveUnknown: true,
	Cluster:           true,
}

func (listRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := listGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpFSRead)
	if !args.OK() {
		return Unresolved(req, action.OpFSRead, args.UnknownReason(req.Command.Name()))
	}

	targets := args.Operands
	if len(targets) == 0 {
		targets = []parser.Word{{Text: "."}}
	}
	var flags []action.EffectFlag
	if args.HasAny("-R", "--recursive") {
		flags = append(flags, action.EffectFlagRecursive)
	}
	for _, operand := range targets {
		for _, target := range req.TargetsFor(operand) {
			addEffect(&out, target, action.EffectRead, flags...)
		}
	}
	return out
}

// noopRecognizer models the commands that touch nothing at all.
//
// They are worth modeling precisely because they are harmless: `echo`,
// `pwd` and `true` appear constantly in shell one-liners, and leaving them
// unmodeled would make the whole line unresolvable.
type noopRecognizer struct{}

func (noopRecognizer) Names() []string {
	return []string{"echo", "printf", "pwd", "true", "false", "test", "[", "date", "sleep", "which", "basename", "dirname"}
}

var noopGrammar = Grammar{PermissiveUnknown: true, Cluster: true}

func (noopRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := noopGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpNoop)
	if !args.OK() {
		return Unresolved(req, action.OpNoop, args.UnknownReason(req.Command.Name()))
	}
	return out
}

// removeRecognizer models `rm` (§15.4).
type removeRecognizer struct{}

func (removeRecognizer) Names() []string { return []string{"rm"} }

var removeGrammar = Grammar{
	Safe: []string{
		"-i", "-I", "-v", "-d", "--interactive", "--verbose", "--dir",
		"--one-file-system", "--preserve-root",
	},
	Semantic: []string{
		"-r", "-R", "--recursive", "-f", "--force", "--no-preserve-root",
	},
	Cluster: true,
}

func (removeRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := removeGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpFSDelete)
	if !args.OK() {
		return Unresolved(req, action.OpFSDelete, args.UnknownReason(req.Command.Name()))
	}
	if len(args.Operands) == 0 {
		return Unresolved(req, action.OpFSDelete, "rm was called without a target")
	}

	var flags []action.EffectFlag
	if args.HasAny("-r", "-R", "--recursive") {
		flags = append(flags, action.EffectFlagRecursive)
	}
	if args.HasAny("-f", "--force") {
		flags = append(flags, action.EffectFlagForce)
	}

	for _, operand := range args.Operands {
		for _, target := range req.TargetsFor(operand) {
			addEffect(&out, target, action.EffectDelete, flags...)
		}
	}
	return out
}

// copyRecognizer models `cp` (§15.4): the sources are read and the destination
// is created.
type copyRecognizer struct{}

func (copyRecognizer) Names() []string { return []string{"cp"} }

var copyGrammar = Grammar{
	Safe: []string{
		"-i", "-n", "-v", "-p", "-f", "-L", "-P", "-H", "-u",
		"--interactive", "--no-clobber", "--verbose", "--force", "--update",
		"--dereference", "--no-dereference", "-T", "--no-target-directory",
	},
	SafeOptionalValue: []string{"--preserve", "--no-preserve", "--reflink", "--sparse"},
	Semantic:          []string{"-r", "-R", "-a", "--recursive", "--archive"},
	SemanticValue:     []string{"-t", "--target-directory"},
	Cluster:           true,
}

func (copyRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := copyGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpFSCopy)
	if !args.OK() {
		return Unresolved(req, action.OpFSCopy, args.UnknownReason(req.Command.Name()))
	}

	sources, destination, ok := sourcesAndDestination(args)
	if !ok {
		return Unresolved(req, action.OpFSCopy, "cp was called without a source and a destination")
	}

	var flags []action.EffectFlag
	if args.HasAny("-r", "-R", "-a", "--recursive", "--archive") {
		flags = append(flags, action.EffectFlagRecursive)
	}

	for _, source := range sources {
		for _, target := range req.TargetsFor(source) {
			addEffect(&out, target, action.EffectRead, flags...)
		}
	}
	if target, ok := req.TargetFor(destination); ok {
		addEffect(&out, target, action.EffectCreate, flags...)
		addEffect(&out, target, action.EffectWrite, flags...)
	}
	return out
}

// moveRecognizer models `mv` (§15.4). The source deletion is modeled explicitly
// so the hard rules see it.
type moveRecognizer struct{}

func (moveRecognizer) Names() []string { return []string{"mv"} }

var moveGrammar = Grammar{
	Safe: []string{
		"-i", "-n", "-v", "-f", "-u", "--interactive", "--no-clobber",
		"--verbose", "--force", "--update", "-T", "--no-target-directory",
	},
	SemanticValue: []string{"-t", "--target-directory"},
	Cluster:       true,
}

func (moveRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := moveGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpFSMove)
	if !args.OK() {
		return Unresolved(req, action.OpFSMove, args.UnknownReason(req.Command.Name()))
	}

	sources, destination, ok := sourcesAndDestination(args)
	if !ok {
		return Unresolved(req, action.OpFSMove, "mv was called without a source and a destination")
	}

	for _, source := range sources {
		for _, target := range req.TargetsFor(source) {
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

// makeDirRecognizer models `mkdir` (§15.4).
type makeDirRecognizer struct{}

func (makeDirRecognizer) Names() []string { return []string{"mkdir"} }

var makeDirGrammar = Grammar{
	Safe:      []string{"-p", "-v", "--parents", "--verbose"},
	SafeValue: []string{"-m", "--mode"},
	Cluster:   true,
}

func (makeDirRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := makeDirGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpFSCreate)
	if !args.OK() {
		return Unresolved(req, action.OpFSCreate, args.UnknownReason(req.Command.Name()))
	}
	if len(args.Operands) == 0 {
		return Unresolved(req, action.OpFSCreate, "mkdir was called without a directory")
	}

	for _, operand := range args.Operands {
		for _, target := range req.TargetsFor(operand) {
			addEffect(&out, target, action.EffectCreate)
		}
	}
	return out
}

// catRecognizer models `cat` (§15.4).
type catRecognizer struct{}

func (catRecognizer) Names() []string { return []string{"cat"} }

var catGrammar = Grammar{
	Safe: []string{
		"-n", "-b", "-s", "-E", "-T", "-v", "-A", "-e", "-t", "-u",
		"--number", "--number-nonblank", "--squeeze-blank", "--show-ends",
		"--show-tabs", "--show-nonprinting", "--show-all",
	},
	Cluster: true,
}

func (catRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := catGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpFSRead)
	if !args.OK() {
		return Unresolved(req, action.OpFSRead, args.UnknownReason(req.Command.Name()))
	}

	for _, operand := range args.Operands {
		if operand.Text == "-" {
			continue
		}
		for _, target := range req.TargetsFor(operand) {
			addEffect(&out, target, action.EffectRead)
		}
	}
	return out
}

// grepRecognizer models `grep` (§15.4).
//
// grep is read-only by construction, so its grammar accepts unlisted options as
// SAFE rather than refusing them. Options that consume a following word are
// still listed: misreading one would swallow a path and hide the read.
type grepRecognizer struct{}

func (grepRecognizer) Names() []string { return []string{"grep", "egrep", "fgrep", "rgrep"} }

var grepGrammar = Grammar{
	SafeValue: []string{
		"-e", "--regexp", "-m", "--max-count", "-A", "--after-context",
		"-B", "--before-context", "-C", "--context", "--label",
		"--include", "--exclude", "--exclude-dir", "--include-dir",
	},
	SafeOptionalValue: []string{"--color", "--colour", "--binary-files", "--devices", "--directories"},
	SemanticValue:     []string{"-f", "--file", "--exclude-from"},
	Semantic:          []string{"-r", "-R", "--recursive", "--dereference-recursive"},
	PermissiveUnknown: true,
	Cluster:           true,
}

func (grepRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := grepGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpFSRead)
	if !args.OK() {
		return Unresolved(req, action.OpFSRead, args.UnknownReason(req.Command.Name()))
	}

	var flags []action.EffectFlag
	if args.HasAny("-r", "-R", "--recursive", "--dereference-recursive") {
		flags = append(flags, action.EffectFlagRecursive)
	}

	// A pattern or exclude file is read like any other input.
	for _, option := range []string{"-f", "--file", "--exclude-from"} {
		if !args.Has(option) {
			continue
		}
		for _, target := range req.TargetsFor(args.Value(option)) {
			addEffect(&out, target, action.EffectRead)
		}
	}

	// Without -e or -f the first operand is the pattern, not a path.
	files := args.Operands
	if !args.HasAny("-e", "--regexp", "-f", "--file") {
		if len(files) == 0 {
			return out
		}
		files = files[1:]
	}
	for _, operand := range files {
		if operand.Text == "-" {
			continue
		}
		for _, target := range req.TargetsFor(operand) {
			addEffect(&out, target, action.EffectRead, flags...)
		}
	}
	return out
}

// findRecognizer models `find` (§15.4). Its single-dash options are words, so
// short-option clustering is off and predicates outside the whitelist make the
// command UNRESOLVED.
type findRecognizer struct{}

func (findRecognizer) Names() []string { return []string{"find"} }

var findGrammar = Grammar{
	Safe: []string{
		"-print", "-print0", "-ls", "-prune", "-empty", "-not", "-a", "-and",
		"-o", "-or", "-L", "-H", "-P", "-depth", "-follow", "-xdev", "-mount",
		"-nouser", "-nogroup", "-readable", "-writable", "-executable",
	},
	SafeValue: []string{
		"-name", "-iname", "-path", "-ipath", "-regex", "-iregex", "-type",
		"-mtime", "-mmin", "-ctime", "-cmin", "-atime", "-amin", "-newer",
		"-size", "-maxdepth", "-mindepth", "-perm", "-user", "-group",
		"-regextype", "-inum", "-links", "-samefile", "-anewer", "-cnewer",
	},
	Semantic:      []string{"-delete"},
	SemanticValue: []string{"-fprint", "-fprint0", "-fls", "-exec", "-execdir", "-ok", "-okdir"},
}

// findGroupingTokens are search-expression syntax, not start paths.
var findGroupingTokens = map[string]bool{"(": true, ")": true, "!": true, ",": true}

func (findRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := findGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpFSRead)
	if !args.OK() {
		return Unresolved(req, action.OpFSRead, args.UnknownReason(req.Command.Name()))
	}

	// Start paths precede the first predicate; anything after it belongs to the
	// search expression, including the words -exec swallows.
	starts := make([]parser.Word, 0, len(args.Operands))
	for _, operand := range args.LeadingOperands() {
		if findGroupingTokens[operand.Text] {
			continue
		}
		starts = append(starts, operand)
	}
	if len(starts) == 0 {
		starts = append(starts, parser.Word{Text: "."})
	}

	deletes := args.Has("-delete")
	if deletes {
		out.SemanticOp = action.OpFSDelete
	}

	for _, start := range starts {
		for _, target := range req.TargetsFor(start) {
			addEffect(&out, target, action.EffectRead)
			if deletes {
				addEffect(&out, target, action.EffectDelete,
					action.EffectFlagRecursive, action.EffectFlagWildcard)
			}
		}
	}

	// -fprint and friends write their output file.
	for _, option := range []string{"-fprint", "-fprint0", "-fls"} {
		if !args.Has(option) {
			continue
		}
		if target, ok := req.TargetFor(args.Value(option)); ok {
			addEffect(&out, target, action.EffectCreate)
			addEffect(&out, target, action.EffectWrite)
		}
	}

	// -exec runs a command Intenter never sees.
	for _, option := range []string{"-exec", "-execdir", "-ok", "-okdir"} {
		if !args.Has(option) {
			continue
		}
		out.Effects = append(out.Effects, action.Effect{
			Type:    action.EffectExecute,
			Program: &action.ProgramRef{Name: args.Value(option).Text, Resolution: action.ProgramUnresolved},
		})
		degrade(&out, fmt.Sprintf("find %s runs a command Intenter cannot model", option))
	}
	return out
}

// resolved starts a fully modeled command.
func resolved(req Request, op action.SemanticOp) action.ResolvedCommand {
	return action.ResolvedCommand{
		Executable: req.Command.Name(),
		SemanticOp: op,
		Status:     action.StatusResolved,
		RawText:    req.Command.RawText,
	}
}

// addEffect records an effect on a target, along with the target itself. Each
// effect gets its own copy of the target so later appends cannot alias it.
func addEffect(out *action.ResolvedCommand, target action.Target, kind action.EffectType, flags ...action.EffectFlag) {
	pinned := target
	effect := action.Effect{Type: kind, Target: &pinned}
	effect.AddFlags(flags...)
	if target.HasFlag(action.FlagWildcard) {
		effect.AddFlags(action.EffectFlagWildcard)
	}
	out.Effects = append(out.Effects, effect)
	appendTarget(out, target)

	// A target that could not be determined exactly can never be approved
	// (§18.1 step 3); recording it here keeps the reason with the command.
	if target.Ambiguous() && out.Status == action.StatusResolved {
		out.StatusReason = fmt.Sprintf("%s contains a variable Intenter cannot expand", target.Raw)
	}
}

// appendTarget adds a target unless an equal one is already recorded.
func appendTarget(out *action.ResolvedCommand, target action.Target) {
	for i := range out.Targets {
		if out.Targets[i].Canonical == target.Canonical && out.Targets[i].Display == target.Display {
			return
		}
	}
	out.Targets = append(out.Targets, target)
}

// sourcesAndDestination splits the operands of cp/mv, honoring -t.
func sourcesAndDestination(args Args) (sources []parser.Word, destination parser.Word, ok bool) {
	for _, option := range []string{"-t", "--target-directory"} {
		if args.Has(option) {
			if len(args.Operands) == 0 {
				return nil, parser.Word{}, false
			}
			return args.Operands, args.Value(option), true
		}
	}
	if len(args.Operands) < 2 {
		return nil, parser.Word{}, false
	}
	last := len(args.Operands) - 1
	return args.Operands[:last], args.Operands[last], true
}
