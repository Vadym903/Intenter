package action

import (
	"strings"
	"testing"
)

func sampleAction() *ResolvedAction {
	dist := Target{Raw: "./dist", Canonical: "/w/dist", Display: "./dist", Scope: ScopeWorkspaceGenerated, Status: TargetResolved}
	pkg := Target{Raw: "package.json", Canonical: "/w/package.json", Display: "package.json", Scope: ScopeWorkspace, Status: TargetResolved}
	return &ResolvedAction{
		RawCommand:  "npm run cleanup",
		Dialect:     DialectPosix,
		SemanticOps: []SemanticOp{OpRunScript, OpFSDelete},
		Commands: []ResolvedCommand{
			{Executable: "npm", SemanticOp: OpRunScript, Targets: []Target{pkg}, Status: StatusResolved},
			{Executable: "rm", SemanticOp: OpFSDelete, Targets: []Target{dist}, Status: StatusResolved},
		},
		Effects: []Effect{
			{Type: EffectRead, Target: &pkg},
			{Type: EffectDelete, Target: &dist, Flags: []EffectFlag{EffectFlagRecursive, EffectFlagForce}},
		},
		Fingerprints: []Fingerprint{
			{Key: "npm-script:package.json#scripts.cleanup", Value: "aaa"},
			{Key: "npm-config:.npmrc#script-shell", Value: "bbb"},
		},
		Status: StatusResolved,
	}
}

func TestActionKeyIsStableAcrossOrderings(t *testing.T) {
	first := sampleAction()

	reordered := sampleAction()
	reordered.Effects[0], reordered.Effects[1] = reordered.Effects[1], reordered.Effects[0]
	reordered.Fingerprints[0], reordered.Fingerprints[1] = reordered.Fingerprints[1], reordered.Fingerprints[0]
	reordered.Commands[0], reordered.Commands[1] = reordered.Commands[1], reordered.Commands[0]

	keyA, err := ActionKey(first, "project-1", 1)
	if err != nil {
		t.Fatalf("ActionKey: %v", err)
	}
	keyB, err := ActionKey(reordered, "project-1", 1)
	if err != nil {
		t.Fatalf("ActionKey: %v", err)
	}
	if keyA != keyB {
		t.Errorf("action_key must not depend on slice ordering:\n%s\n%s", keyA, keyB)
	}
	if len(keyA) != 64 {
		t.Errorf("action_key must be a hex sha256, got %q", keyA)
	}
}

func TestActionKeyChangesWithEveryMatchedField(t *testing.T) {
	base, err := ActionKey(sampleAction(), "project-1", 1)
	if err != nil {
		t.Fatalf("ActionKey: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(a *ResolvedAction) (projectID string, engine int)
	}{
		{"project", func(a *ResolvedAction) (string, int) { return "project-2", 1 }},
		{"engine version", func(a *ResolvedAction) (string, int) { return "project-1", 2 }},
		{"semantic ops", func(a *ResolvedAction) (string, int) {
			a.SemanticOps = []SemanticOp{OpFSDelete, OpRunScript}
			return "project-1", 1
		}},
		{"target", func(a *ResolvedAction) (string, int) {
			a.Commands[1].Targets[0].Display = "./src"
			return "project-1", 1
		}},
		{"effect scope", func(a *ResolvedAction) (string, int) {
			a.Effects[1].Target.Scope = ScopeHome
			return "project-1", 1
		}},
		{"effect flag", func(a *ResolvedAction) (string, int) {
			a.Effects[1].Flags = append(a.Effects[1].Flags, EffectFlagWildcard)
			return "project-1", 1
		}},
		{"fingerprint value", func(a *ResolvedAction) (string, int) {
			a.Fingerprints[0].Value = "changed"
			return "project-1", 1
		}},
		{"fingerprint key", func(a *ResolvedAction) (string, int) {
			a.Fingerprints[0].Key = "npm-script:package.json#scripts.other"
			return "project-1", 1
		}},
		{"network target", func(a *ResolvedAction) (string, int) {
			a.Effects = append(a.Effects, Effect{Type: EffectNetwork, Network: &NetworkTarget{Host: "example.com", Scheme: "https", Method: "GET"}})
			return "project-1", 1
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mutated := sampleAction()
			projectID, engine := tt.mutate(mutated)
			got, err := ActionKey(mutated, projectID, engine)
			if err != nil {
				t.Fatalf("ActionKey: %v", err)
			}
			if got == base {
				t.Errorf("changing %s must change action_key", tt.name)
			}
		})
	}
}

func TestCanonicalJSONSortsKeys(t *testing.T) {
	encoded, err := CanonicalJSON(map[string]any{
		"b": 1,
		"a": map[string]any{"z": true, "y": []any{3, 1, 2}},
	})
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	want := `{"a":{"y":[3,1,2],"z":true},"b":1}`
	if string(encoded) != want {
		t.Errorf("canonical json = %s, want %s", encoded, want)
	}
}

func TestHashTextNormalizesLineEndings(t *testing.T) {
	unix := HashText([]byte("rm -rf ./dist\nexit 0\n"))
	windows := HashText([]byte("rm -rf ./dist\r\nexit 0\r\n"))
	if unix != windows {
		t.Error("CRLF and LF content must fingerprint identically (research R-13)")
	}
	if HashText([]byte("rm -rf ./src\n")) == unix {
		t.Error("different content must fingerprint differently")
	}
}

func TestHashPairsIsOrderIndependent(t *testing.T) {
	a := HashPairs(map[string]string{"build.gradle": "1", "settings.gradle": "2"})
	b := HashPairs(map[string]string{"settings.gradle": "2", "build.gradle": "1"})
	if a != b {
		t.Error("aggregate fingerprints must not depend on map iteration order")
	}
	if HashPairs(map[string]string{"build.gradle": "changed", "settings.gradle": "2"}) == a {
		t.Error("a changed member hash must change the aggregate")
	}
	if HashPairs(map[string]string{"build.gradle": "1"}) == a {
		t.Error("a removed member must change the aggregate")
	}
}

func TestProjectIDIsSHA256OfCanonicalRoot(t *testing.T) {
	id := ProjectID("/Users/u/proj")
	if len(id) != 64 || strings.ContainsAny(id, "/ ") {
		t.Errorf("project id = %q, want hex sha256", id)
	}
	if ProjectID("/Users/u/other") == id {
		t.Error("different roots must have different project ids")
	}
}
