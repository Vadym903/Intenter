package action_test

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The invariant index for the dependency direction: I-7.
// See internal/approval/invariants_test.go for what this index is for.

// corePackages are the packages that must know nothing about any particular
// agent. They are the same set the depguard rule in .golangci.yml lists.
var corePackages = []string{
	"github.com/Vadym903/Intenter/internal/action",
	"github.com/Vadym903/Intenter/internal/parser",
	"github.com/Vadym903/Intenter/internal/parser/posix",
	"github.com/Vadym903/Intenter/internal/parser/cmd",
	"github.com/Vadym903/Intenter/internal/parser/powershell",
	"github.com/Vadym903/Intenter/internal/resolver",
	"github.com/Vadym903/Intenter/internal/scope",
	"github.com/Vadym903/Intenter/internal/policy",
	"github.com/Vadym903/Intenter/internal/approval",
	"github.com/Vadym903/Intenter/internal/audit",
	"github.com/Vadym903/Intenter/internal/storage",
	"github.com/Vadym903/Intenter/internal/daemon",
	"github.com/Vadym903/Intenter/internal/ipc",
	"github.com/Vadym903/Intenter/internal/platform",
	"github.com/Vadym903/Intenter/internal/config",
	"github.com/Vadym903/Intenter/internal/version",
	"github.com/Vadym903/Intenter/internal/logging",
	"github.com/Vadym903/Intenter/internal/updater",
}

// forbiddenByCore are the layers that know about a specific agent or about the
// terminal. Nothing in corePackages may reach them, at any depth.
var forbiddenByCore = []string{
	"github.com/Vadym903/Intenter/internal/adapter",
	"github.com/Vadym903/Intenter/internal/cli",
}

func TestInvariant_I7_CoreIndependentOfAdapters(t *testing.T) {
	// I-7: core packages MUST NOT depend on agent-specific structures; the
	// adapter is the only agent-aware component.
	//
	// This is what makes a second agent a new adapter rather than a rewrite,
	// and it is the invariant most easily lost to a single convenient import —
	// one `claude.Event` reference in the daemon and the boundary is gone.
	// depguard enforces it at lint time; this enforces it wherever tests run,
	// including on a machine with no linter installed.
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("the go tool is not available to inspect the dependency graph")
	}

	for _, pkg := range corePackages {
		t.Run(strings.TrimPrefix(pkg, "github.com/Vadym903/Intenter/"), func(t *testing.T) {
			deps := dependenciesOf(t, pkg)

			for _, forbidden := range forbiddenByCore {
				for _, dep := range deps {
					if dep == forbidden || strings.HasPrefix(dep, forbidden+"/") {
						t.Errorf("%s reaches %s; core packages are agent-independent (I-7)", pkg, dep)
					}
				}
			}
		})
	}
}

func TestInvariant_I7_TheCheckWouldNoticeAViolation(t *testing.T) {
	// A dependency check that cannot fail proves nothing. The adapter itself
	// imports the core, so asking the question the other way round must find
	// exactly what the core direction forbids.
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("the go tool is not available to inspect the dependency graph")
	}

	deps := dependenciesOf(t, "github.com/Vadym903/Intenter/internal/cli")

	found := false
	for _, dep := range deps {
		if strings.HasPrefix(dep, "github.com/Vadym903/Intenter/internal/adapter") {
			found = true
			break
		}
	}
	if !found {
		t.Error("the CLI is expected to reach the adapter; if it no longer does, " +
			"this check is no longer proving anything about the core packages")
	}
}

// dependenciesOf returns the full transitive import closure of a package.
func dependenciesOf(t *testing.T, pkg string) []string {
	t.Helper()

	output, err := exec.Command("go", "list", "-deps", pkg).CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps %s: %v\n%s", pkg, err, output)
	}
	return strings.Fields(string(output))
}

func TestEveryInvariantInTheSpecHasATest(t *testing.T) {
	// The index is only worth having if it stays complete. Adding an invariant
	// to Appendix A and no test for it is exactly the kind of omission nobody
	// notices, because nothing fails.
	specPath := filepath.Join("..", "..", "specs", "001-agentguard-prototype", "PROTOTYPE_SPEC.md")
	spec, err := os.ReadFile(specPath)
	if err != nil {
		t.Skipf("the specification is not present in this tree: %v", err)
	}

	declared := invariantIDs(string(spec))
	if len(declared) == 0 {
		t.Fatalf("no invariants found in %s; has Appendix A moved?", specPath)
	}
	// Appendix A numbers its rows from 1 without gaps, so a partial parse shows
	// up here rather than as a quietly shorter list.
	for i, id := range declared {
		if id != i+1 {
			t.Fatalf("read invariants %v from Appendix A; the list must run from I-1 without gaps, "+
				"so this is a parsing failure rather than a missing test", declared)
		}
	}
	t.Logf("Appendix A declares %d invariants", len(declared))

	tested := testedInvariantIDs(t)
	for _, id := range declared {
		if !tested[id] {
			t.Errorf("I-%d has no TestInvariant_I%d_… test; add one to the nearest package's "+
				"invariants_test.go", id, id)
		}
	}
	for id := range tested {
		if !slices.Contains(declared, id) {
			t.Errorf("TestInvariant_I%d_… names an invariant Appendix A does not declare", id)
		}
	}
}

// invariantIDs returns the invariant numbers Appendix A declares.
func invariantIDs(spec string) []int {
	appendix := strings.Index(spec, "## Appendix A")
	if appendix < 0 {
		return nil
	}
	end := strings.Index(spec[appendix:], "## Appendix B")
	if end < 0 {
		end = len(spec) - appendix
	}

	var ids []int
	for _, match := range invariantRowPattern.FindAllStringSubmatch(spec[appendix:appendix+end], -1) {
		id, err := strconv.Atoi(match[1])
		if err != nil || slices.Contains(ids, id) {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

// testedInvariantIDs returns the invariant numbers that have a named test.
func testedInvariantIDs(t *testing.T) map[int]bool {
	t.Helper()

	tested := make(map[int]bool)
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, match := range invariantTestPattern.FindAllSubmatch(content, -1) {
			if id, convErr := strconv.Atoi(string(match[1])); convErr == nil {
				tested[id] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the source tree: %v", err)
	}
	return tested
}

var (
	invariantRowPattern  = regexp.MustCompile(`\|\s*I-(\d+)\s*\|`)
	invariantTestPattern = regexp.MustCompile(`func TestInvariant_I(\d+)_`)
)
