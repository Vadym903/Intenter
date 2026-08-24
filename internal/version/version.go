// Package version exposes the build version and the compatibility versions
// that Intenter persists and negotiates (PROTOTYPE_SPEC.md §13, §20.3, §23.4).
package version

import (
	"runtime"
)

// Version is the human-readable release version. Release builds inject it with
// -ldflags "-X github.com/Vadym903/Intenter/internal/version.Version=<v>".
var Version = "0.1.0-dev"

const (
	// EngineVersion identifies the semantics of resolution, policy and approval
	// matching. Approvals record it and match only when the engine major version
	// is equal (PROTOTYPE_SPEC.md §20.3 rule 1).
	EngineVersion = 1

	// ProtocolVersion is the daemon IPC protocol version. It is bumped only for
	// incompatible changes (contracts/ipc-protocol.md).
	ProtocolVersion = 1

	// SchemaVersion is the highest SQLite schema version this build knows. The
	// daemon refuses to run against a database with a higher version.
	SchemaVersion = 1
)

// Info is the machine-readable version report used by `intenter version --json`.
type Info struct {
	Version         string `json:"version"`
	EngineVersion   int    `json:"engine_version"`
	ProtocolVersion int    `json:"protocol_version"`
	SchemaVersion   int    `json:"schema_version"`
	GoVersion       string `json:"go_version"`
	Platform        string `json:"platform"`
}

// Current returns the versions of the running binary.
func Current() Info {
	return Info{
		Version:         Version,
		EngineVersion:   EngineVersion,
		ProtocolVersion: ProtocolVersion,
		SchemaVersion:   SchemaVersion,
		GoVersion:       runtime.Version(),
		Platform:        runtime.GOOS + "/" + runtime.GOARCH,
	}
}

// EngineCompatible reports whether an approval created by engine version other
// may be used by this engine. Engine versions are compatible when equal; the
// prototype has a single major version (PROTOTYPE_SPEC.md §20.3 rule 1).
func EngineCompatible(other int) bool {
	return other == EngineVersion
}
