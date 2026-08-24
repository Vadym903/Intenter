package action

import (
	"encoding/json"
	"testing"
)

func TestWeakerStatusTakesTheWeakestOverCommands(t *testing.T) {
	tests := []struct {
		a, b, want ResolutionStatus
	}{
		{StatusResolved, StatusResolved, StatusResolved},
		{StatusResolved, StatusDeclared, StatusDeclared},
		{StatusDeclared, StatusResolved, StatusDeclared},
		{StatusDeclared, StatusUnresolved, StatusUnresolved},
		{StatusUnresolved, StatusParseFailed, StatusParseFailed},
		{StatusParseFailed, StatusContextFailed, StatusParseFailed},
		{StatusContextFailed, StatusParseFailed, StatusContextFailed},
		{StatusResolved, StatusContextFailed, StatusContextFailed},
	}
	for _, tt := range tests {
		if got := WeakerStatus(tt.a, tt.b); got != tt.want {
			t.Errorf("WeakerStatus(%s, %s) = %s, want %s", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestApprovableStatuses(t *testing.T) {
	approvable := map[ResolutionStatus]bool{
		StatusResolved:      true,
		StatusDeclared:      true,
		StatusUnresolved:    false,
		StatusParseFailed:   false,
		StatusContextFailed: false,
	}
	for status, want := range approvable {
		if got := status.Approvable(); got != want {
			t.Errorf("%s.Approvable() = %v, want %v", status, got, want)
		}
	}
}

func TestDecisionOutcomeUsesWireFormInJSON(t *testing.T) {
	raw, err := json.Marshal(EvaluationResult{Decision: OutcomeBlock, Class: ClassApprovalMismatch})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["decision"] != "block" {
		t.Errorf("decision = %v, want %q (contracts/ipc-protocol.md)", decoded["decision"], "block")
	}
	if decoded["class"] != string(ClassApprovalMismatch) {
		t.Errorf("class = %v, want %q", decoded["class"], ClassApprovalMismatch)
	}
}

func TestDecisionOutcomeUnmarshalAcceptsBothForms(t *testing.T) {
	for _, input := range []string{`"allow"`, `"ALLOW"`} {
		var got DecisionOutcome
		if err := json.Unmarshal([]byte(input), &got); err != nil {
			t.Fatalf("unmarshal %s: %v", input, err)
		}
		if got != OutcomeAllow {
			t.Errorf("unmarshal %s = %s, want %s", input, got, OutcomeAllow)
		}
	}
	var bad DecisionOutcome
	if err := json.Unmarshal([]byte(`"maybe"`), &bad); err == nil {
		t.Error("expected an error for an unknown outcome")
	}
}

func TestHardRuleClass(t *testing.T) {
	if got := HardRuleClass("R2"); got != DecisionClass("HARD_RULE_R2") {
		t.Errorf("HardRuleClass(R2) = %s", got)
	}
}

func TestTargetFlagsStaySortedAndUnique(t *testing.T) {
	target := &Target{Raw: "./dist"}
	target.AddFlags(FlagWildcard, FlagBroad, FlagWildcard)
	target.AddFlags(FlagSensitive)

	want := []TargetFlag{FlagBroad, FlagSensitive, FlagWildcard}
	if len(target.Flags) != len(want) {
		t.Fatalf("flags = %v, want %v", target.Flags, want)
	}
	for i := range want {
		if target.Flags[i] != want[i] {
			t.Fatalf("flags = %v, want %v", target.Flags, want)
		}
	}
	if !target.HasAnyFlag(FlagSensitive, FlagTemp) {
		t.Error("HasAnyFlag must find a present flag")
	}
	if target.HasAnyFlag(FlagTemp, FlagNetworkPath) {
		t.Error("HasAnyFlag must not report absent flags")
	}
}

func TestEnvelopeIncludesFlagsAndDeduplicates(t *testing.T) {
	generated := &Target{Display: "./dist", Scope: ScopeWorkspaceGenerated}
	effects := []Effect{
		{Type: EffectDelete, Target: generated, Flags: []EffectFlag{EffectFlagRecursive, EffectFlagForce}},
		{Type: EffectDelete, Target: generated, Flags: []EffectFlag{EffectFlagForce, EffectFlagRecursive}},
		{Type: EffectRead, Target: &Target{Display: "package.json", Scope: ScopeWorkspace}},
	}

	envelope := Envelope(effects)
	if len(envelope) != 2 {
		t.Fatalf("envelope = %v, want 2 entries", envelope)
	}

	var deleteEntry EnvelopeEntry
	for _, entry := range envelope {
		if entry.Type == EffectDelete {
			deleteEntry = entry
		}
	}
	if deleteEntry.Scope != ScopeWorkspaceGenerated {
		t.Errorf("scope = %s", deleteEntry.Scope)
	}
	if got := deleteEntry.Key(); got != "DELETE/WORKSPACE_GENERATED{force,recursive}[]" {
		t.Errorf("key = %q", got)
	}
	if got := deleteEntry.String(); got != "DELETE(force,recursive) WORKSPACE_GENERATED" {
		t.Errorf("string = %q", got)
	}
}

func TestEnvelopeDistinguishesFlagSets(t *testing.T) {
	target := &Target{Display: "./dist", Scope: ScopeWorkspaceGenerated}
	narrow := Envelope([]Effect{{Type: EffectDelete, Target: target, Flags: []EffectFlag{EffectFlagRecursive}}})
	broad := Envelope([]Effect{{Type: EffectDelete, Target: target, Flags: []EffectFlag{EffectFlagRecursive, EffectFlagWildcard}}})
	if narrow[0].Key() == broad[0].Key() {
		t.Error("a broader flag set must not share an envelope key (INVARIANT I-1)")
	}
}

func TestEnvelopeDistinguishesProgramResolution(t *testing.T) {
	declared := Envelope([]Effect{{Type: EffectExecute, Program: &ProgramRef{Name: "gradle", Resolution: ProgramDeclared}}})
	unresolved := Envelope([]Effect{{Type: EffectExecute, Program: &ProgramRef{Name: "gradle", Resolution: ProgramUnresolved}}})
	if declared[0].Key() == unresolved[0].Key() {
		t.Error("EXECUTE(DECLARED) must not share an envelope key with EXECUTE(UNRESOLVED)")
	}
}

func TestNetworkTargetsDeduplicateAndSort(t *testing.T) {
	effects := []Effect{
		{Type: EffectNetwork, Network: &NetworkTarget{Host: "b.example", Scheme: "https", Method: "GET"}},
		{Type: EffectNetwork, Network: &NetworkTarget{Host: "a.example", Scheme: "https", Method: "GET"}},
		{Type: EffectNetwork, Network: &NetworkTarget{Host: "a.example", Scheme: "https", Method: "GET"}},
	}
	got := NetworkTargets(effects)
	if len(got) != 2 {
		t.Fatalf("network targets = %v, want 2", got)
	}
	if got[0].Host != "a.example" || got[1].Host != "b.example" {
		t.Errorf("network targets not sorted: %v", got)
	}
}

func TestNetworkTargetKeyDistinguishesMethodAndHost(t *testing.T) {
	get := NetworkTarget{Host: "api.example.com", Scheme: "https", Method: "GET"}
	post := NetworkTarget{Host: "api.example.com", Scheme: "https", Method: "POST"}
	other := NetworkTarget{Host: "evil.example.net", Scheme: "https", Method: "GET"}
	if get.Key() == post.Key() {
		t.Error("method must be part of the network identity")
	}
	if get.Key() == other.Key() {
		t.Error("host must be part of the network identity")
	}
	if got := get.String(); got != "GET https://api.example.com" {
		t.Errorf("String() = %q", got)
	}
}

func TestHasAmbiguousTargetLooksAtEffectsAndTargets(t *testing.T) {
	action := &ResolvedAction{Commands: []ResolvedCommand{{
		Targets: []Target{{Raw: "$UNKNOWN/x", Status: TargetAmbiguous}},
	}}}
	if !action.HasAmbiguousTarget() {
		t.Error("ambiguous command target must be reported")
	}

	viaEffect := &ResolvedAction{Commands: []ResolvedCommand{{
		Effects: []Effect{{Type: EffectDelete, Target: &Target{Status: TargetAmbiguous}}},
	}}}
	if !viaEffect.HasAmbiguousTarget() {
		t.Error("ambiguous effect target must be reported")
	}

	clean := &ResolvedAction{Commands: []ResolvedCommand{{
		Targets: []Target{{Raw: "./dist", Status: TargetResolved}},
	}}}
	if clean.HasAmbiguousTarget() {
		t.Error("resolved targets must not be reported as ambiguous")
	}
}

func TestMergeFingerprintsIsUniqueByKeyAndSorted(t *testing.T) {
	merged := MergeFingerprints(nil,
		Fingerprint{Key: "npm-script:package.json#scripts.cleanup", Value: "a"},
		Fingerprint{Key: "gradle-config", Value: "b"},
		Fingerprint{Key: "npm-script:package.json#scripts.cleanup", Value: "changed"},
	)
	if len(merged) != 2 {
		t.Fatalf("merged = %v, want 2", merged)
	}
	if merged[0].Key != "gradle-config" {
		t.Errorf("fingerprints not sorted: %v", merged)
	}
	if merged[1].Value != "a" {
		t.Errorf("first value must win, got %q", merged[1].Value)
	}
}

func TestAgentConsentUsable(t *testing.T) {
	var nilConsent *AgentConsent
	if nilConsent.Usable() {
		t.Error("nil consent must not be usable")
	}
	if (&AgentConsent{Kind: "session", RuleKeys: []string{"x"}}).Usable() {
		t.Error("only persistent_rule consent is usable")
	}
	if (&AgentConsent{Kind: ConsentKindPersistentRule}).Usable() {
		t.Error("consent without rule keys must not be usable")
	}
	if !(&AgentConsent{Kind: ConsentKindPersistentRule, RuleKeys: []string{"local:Bash(npm run cleanup)"}}).Usable() {
		t.Error("valid persistent rule consent must be usable")
	}
}
