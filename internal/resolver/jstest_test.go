package resolver

import (
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
)

func TestJSTestRunnersAreDeclared(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	tests := []string{
		"jest",
		"jest --coverage",
		"jest --ci --silent",
		"vitest --run",
		"vitest run",
		"mocha",
		"node --test",
		"jest -t 'my case'",
		"jest --maxWorkers=2",
		"jest --reporter=json",
		"jest src/app.test.js",
	}

	for _, command := range tests {
		t.Run(command, func(t *testing.T) {
			out := r.resolveCommand(t, command)
			if out.Status != action.StatusDeclared {
				t.Fatalf("status = %s (%s), want DECLARED", out.Status, out.StatusReason)
			}
			if out.SemanticOp != action.OpRunTests {
				t.Errorf("semantic op = %s, want RUN_TESTS", out.SemanticOp)
			}
		})
	}
}

func TestJSTestDeclaredEnvelope(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)
	out := r.resolveCommand(t, "jest --coverage")

	summary := strings.Join(effectSummary(out), "\n")
	for _, want := range []string{
		"READ(recursive) .",
		"CREATE(recursive) ./coverage",
		"WRITE(recursive) ./coverage",
		"EXECUTE program:DECLARED",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("declared envelope must include %q:\n%s", want, summary)
		}
	}
	// A test run is local; it must not declare network access.
	if strings.Contains(summary, "NETWORK") {
		t.Errorf("a test run declares no network access:\n%s", summary)
	}
}

func TestJSTestRefusesUnknownFlags(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	for _, command := range []string{
		"jest --zap",
		"jest --config custom.js",
		"jest --rootDir ../other",
		"vitest --dir ../elsewhere",
	} {
		out := r.resolveCommand(t, command)
		if out.Status != action.StatusUnresolved {
			t.Errorf("%q: status = %s, want UNRESOLVED for a flag that moves the run", command, out.Status)
		}
	}
}

func TestNodeWithoutTestIsUnresolved(t *testing.T) {
	// `node script.js` runs code Intenter has never read.
	r := nodeRepo(t, `{"scripts":{}}`)

	for _, command := range []string{"node server.js", "node", "node -e 'x'"} {
		out := r.resolveCommand(t, command)
		if out.Status != action.StatusUnresolved {
			t.Errorf("%q: status = %s, want UNRESOLVED", command, out.Status)
		}
	}
}

func TestNpmTestResolvesThroughTheRunner(t *testing.T) {
	// The reason the runners exist: `npm test` must resolve rather than stop at
	// an unknown executable.
	r := nodeRepo(t, `{"scripts":{"test":"jest --ci"}}`)

	out := r.resolveAction(t, "npm test")
	if out.Status != action.StatusDeclared {
		t.Fatalf("status = %s (%s), want DECLARED", out.Status, out.StatusReason)
	}
	if len(out.SemanticOps) != 1 || out.SemanticOps[0] != action.OpRunScript {
		t.Errorf("semantic ops = %v, want [RUN_SCRIPT]", out.SemanticOps)
	}
	if _, ok := fingerprintValue(out, "npm-script:package.json#scripts.test"); !ok {
		t.Errorf("the script must be fingerprinted: %+v", out.Fingerprints)
	}

	summary := strings.Join(actionEffects(out), "\n")
	if !strings.Contains(summary, "EXECUTE program:DECLARED") {
		t.Errorf("the test run must be declared:\n%s", summary)
	}
}

func TestRimrafIsADeclaredDelete(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	out := r.resolveCommand(t, "rimraf ./dist")
	if out.Status != action.StatusDeclared {
		t.Fatalf("status = %s (%s), want DECLARED", out.Status, out.StatusReason)
	}
	if out.SemanticOp != action.OpFSDelete {
		t.Errorf("semantic op = %s, want FS_DELETE", out.SemanticOp)
	}
	assertEffects(t, out, "DELETE(force,recursive) ./dist")
}

func TestRimrafIsStillSubjectToTheSafetyFloor(t *testing.T) {
	// A cross-platform delete tool is still a delete: the target's scope is
	// what matters, not the executable.
	r := nodeRepo(t, `{"scripts":{}}`)

	out := r.resolveCommand(t, "rimraf ~/Documents")
	if len(out.Targets) != 1 {
		t.Fatalf("targets = %+v, want one", out.Targets)
	}
	if out.Targets[0].Scope != action.ScopeHome {
		t.Errorf("scope = %s, want HOME", out.Targets[0].Scope)
	}
	if !out.Effects[0].HasFlag(action.EffectFlagRecursive) {
		t.Error("rimraf always deletes recursively")
	}
}

func TestNpmCleanupThroughRimraf(t *testing.T) {
	// The cross-platform form of the demo script.
	r := nodeRepo(t, `{"scripts":{"cleanup":"rimraf ./dist"}}`)

	out := r.resolveAction(t, "npm run cleanup")
	if out.Status != action.StatusDeclared {
		t.Fatalf("status = %s (%s)", out.Status, out.StatusReason)
	}
	// rimraf's behavior is fully modeled from its arguments, so it declares the
	// delete and nothing else: there is no unread program to record.
	assertActionEffects(t, out, "DELETE(force,recursive) ./dist")
}
