package e2e

import (
	"os"
	"testing"
)

// The suite compiles the binary under test — twice, once as the installed copy
// and once as the release it updates to. Those are tens of megabytes each, and
// they are built into temporary directories that outlive any single test.
//
// Without this they accumulate one pair per `go test` run, which is invisible
// until a machine runs out of disk in the middle of something else.
func TestMain(m *testing.M) {
	code := m.Run()

	for _, dir := range []string{buildTmpDir, releaseTmpDir} {
		if dir != "" {
			os.RemoveAll(dir)
		}
	}
	os.Exit(code)
}
