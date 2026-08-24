package version

import (
	"encoding/json"
	"runtime"
	"testing"
)

func TestCurrentReportsBuildAndCompatibilityVersions(t *testing.T) {
	got := Current()
	if got.Version == "" {
		t.Fatal("Version must not be empty")
	}
	if got.EngineVersion != EngineVersion {
		t.Errorf("EngineVersion = %d, want %d", got.EngineVersion, EngineVersion)
	}
	if got.ProtocolVersion != ProtocolVersion {
		t.Errorf("ProtocolVersion = %d, want %d", got.ProtocolVersion, ProtocolVersion)
	}
	if got.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", got.SchemaVersion, SchemaVersion)
	}
	if got.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		t.Errorf("Platform = %q, want %q", got.Platform, runtime.GOOS+"/"+runtime.GOARCH)
	}
}

func TestInfoJSONKeys(t *testing.T) {
	raw, err := json.Marshal(Current())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"version", "engine_version", "protocol_version", "schema_version", "go_version", "platform"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("missing key %q in %s", key, raw)
		}
	}
}

func TestEngineCompatible(t *testing.T) {
	if !EngineCompatible(EngineVersion) {
		t.Error("current engine version must be compatible with itself")
	}
	if EngineCompatible(EngineVersion + 1) {
		t.Error("a newer engine version must not be compatible")
	}
	if EngineCompatible(EngineVersion - 1) {
		t.Error("an older engine version must not be compatible")
	}
}
