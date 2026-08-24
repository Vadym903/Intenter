package resolver

import (
	"path/filepath"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
)

// JSTestRecognizers returns the JavaScript test runners and rimraf (§15.4).
// They matter because `npm test` resolves to one of them, so without these the
// most common script in a Node project would stay UNRESOLVED.
func JSTestRecognizers() []Recognizer {
	return []Recognizer{jsTestRecognizer{}, rimrafRecognizer{}, nodeRecognizer{}}
}

type jsTestRecognizer struct{}

func (jsTestRecognizer) Names() []string { return []string{"jest", "vitest", "mocha"} }

// jsTestGrammar lists the flags that change nothing about what a test run
// touches. A flag that takes a path, or one Intenter does not know, makes the
// invocation UNRESOLVED (§15.4).
var jsTestGrammar = Grammar{
	Safe: []string{
		"--coverage", "--no-coverage", "--ci", "--silent", "--verbose",
		"--watch=false", "--no-watch", "--run", "--bail", "--passWithNoTests",
		"--colors", "--no-colors", "--detectOpenHandles", "--forceExit",
		"--listTests", "--onlyChanged", "--changedSince", "--updateSnapshot",
		"-u", "--all", "--single-run", "--pool=threads", "--no-file-parallelism",
	},
	SafeValue: []string{
		"-t", "--testNamePattern", "--maxWorkers", "-w", "--workers",
		"--shard", "--reporter", "--reporters", "--timeout", "--retries",
	},
	SafePrefixes:     []string{"--reporter", "--testNamePattern", "--maxWorkers", "--shard"},
	SafeNumericShort: true,
}

func (jsTestRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := jsTestGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpRunTests)
	out.Status = action.StatusDeclared

	if !args.OK() {
		return Unresolved(req, action.OpRunTests, args.UnknownReason(req.Command.Name()))
	}

	workspace := req.Workspace()
	if workspace == "" {
		return Unresolved(req, action.OpRunTests, "no workspace root was established for this request")
	}

	declareTestEnvelope(&out, req, workspace)

	// Positional arguments are test path filters, which only narrow the run.
	for _, operand := range args.Operands {
		if strings.HasPrefix(operand.Text, "-") {
			continue
		}
		for _, target := range req.TargetsFor(operand) {
			addEffect(&out, target, action.EffectRead, action.EffectFlagRecursive)
		}
	}
	return out
}

// nodeRecognizer models `node --test`, Node's own test runner. Any other node
// invocation runs a script Intenter has not read, so it stays unresolved.
type nodeRecognizer struct{}

func (nodeRecognizer) Names() []string { return []string{"node"} }

func (nodeRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := req.Command.ArgTexts()
	if len(args) == 0 || args[0] != "--test" {
		return Unresolved(req, action.OpUnknown,
			"node runs a script Intenter has not read; only `node --test` is modeled")
	}

	workspace := req.Workspace()
	if workspace == "" {
		return Unresolved(req, action.OpRunTests, "no workspace root was established for this request")
	}

	out := resolved(req, action.OpRunTests)
	out.Status = action.StatusDeclared
	declareTestEnvelope(&out, req, workspace)
	return out
}

// declareTestEnvelope records what a test run is declared to do: read the
// project, write its own coverage and cache output, and execute project code.
//
// Test code is arbitrary project code, so this envelope is what an approval
// permits — bounded by the fingerprints of the files that define the run.
func declareTestEnvelope(out *action.ResolvedCommand, req Request, workspace string) {
	if target, ok := req.PathTarget(workspace); ok {
		addEffect(out, target, action.EffectRead, action.EffectFlagRecursive)
	}
	for _, dir := range []string{"coverage", "node_modules/.cache", ".vitest", ".jest-cache"} {
		target, ok := req.PathTarget(filepath.Join(workspace, filepath.FromSlash(dir)))
		if !ok {
			continue
		}
		addEffect(out, target, action.EffectCreate, action.EffectFlagRecursive)
		addEffect(out, target, action.EffectWrite, action.EffectFlagRecursive)
	}
	out.Effects = append(out.Effects, action.Effect{
		Type:    action.EffectExecute,
		Program: &action.ProgramRef{Name: displayName(out.Executable), Resolution: action.ProgramDeclared},
	})
}

// rimrafRecognizer models the cross-platform delete used by cleanup scripts.
type rimrafRecognizer struct{}

func (rimrafRecognizer) Names() []string { return []string{"rimraf", "del-cli"} }

var rimrafGrammar = Grammar{
	Safe: []string{
		"-r", "--recursive", "-f", "--force", "-v", "--verbose", "-g", "--glob",
		"--no-glob", "--preserve-root", "--no-preserve-root", "--impl",
	},
	SafeValue: []string{"--max-retries", "--retry-delay", "--backoff"},
}

func (rimrafRecognizer) Recognize(req Request) action.ResolvedCommand {
	args := rimrafGrammar.Scan(req.Command.Args())
	out := resolved(req, action.OpFSDelete)
	out.Status = action.StatusDeclared

	if !args.OK() {
		return Unresolved(req, action.OpFSDelete, args.UnknownReason(req.Command.Name()))
	}
	if len(args.Operands) == 0 {
		return Unresolved(req, action.OpFSDelete, "rimraf was called without a target")
	}

	// rimraf always deletes recursively and without prompting.
	for _, operand := range args.Operands {
		for _, target := range req.TargetsFor(operand) {
			addEffect(&out, target, action.EffectDelete,
				action.EffectFlagRecursive, action.EffectFlagForce)
		}
	}
	return out
}
