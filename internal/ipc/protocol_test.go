package ipc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/version"
)

func TestRequestEnvelopeMatchesContract(t *testing.T) {
	req, err := NewRequest("6f1c", MethodEvaluate, EvaluateParams{
		DryRun:  true,
		Request: action.ActionRequest{Agent: "claude", RawCommand: "git status"},
	})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["protocol_version"] != float64(version.ProtocolVersion) {
		t.Errorf("protocol_version = %v", decoded["protocol_version"])
	}
	if decoded["request_id"] != "6f1c" || decoded["method"] != "evaluate" {
		t.Errorf("envelope = %v", decoded)
	}
	if _, ok := decoded["params"]; !ok {
		t.Error("params must be present")
	}

	var params EvaluateParams
	if err := req.DecodeParams(&params); err != nil {
		t.Fatalf("DecodeParams: %v", err)
	}
	if !params.DryRun || params.Request.RawCommand != "git status" {
		t.Errorf("params = %+v", params)
	}
}

func TestResponseEnvelopes(t *testing.T) {
	resp, err := NewResponse("6f1c", PingResult{Version: "0.1.0", EngineVersion: 1, ProtocolVersion: 1, UptimeS: 12, PID: 42})
	if err != nil {
		t.Fatalf("NewResponse: %v", err)
	}
	if !resp.OK || resp.Error != nil {
		t.Errorf("response = %+v", resp)
	}

	var ping PingResult
	if err := resp.DecodeResult(&ping); err != nil {
		t.Fatalf("DecodeResult: %v", err)
	}
	if ping.PID != 42 || ping.Version != "0.1.0" {
		t.Errorf("ping = %+v", ping)
	}

	failure := NewErrorResponse("6f1c", CodeNotFound, "approval 42 not found")
	if failure.OK || failure.Error.Code != CodeNotFound {
		t.Errorf("error response = %+v", failure)
	}
	var target PingResult
	err = failure.DecodeResult(&target)
	var protocolErr *Error
	if !errors.As(err, &protocolErr) || protocolErr.Code != CodeNotFound {
		t.Errorf("DecodeResult on an error response = %v, want the protocol error", err)
	}
	if got := protocolErr.Error(); !strings.Contains(got, "NOT_FOUND") {
		t.Errorf("error string = %q", got)
	}
}

func TestDecodeParamsRejectsGarbage(t *testing.T) {
	req := &Request{Method: MethodEvaluate, Params: json.RawMessage(`{"dry_run": "yes"}`)}
	var params EvaluateParams
	if err := req.DecodeParams(&params); err == nil {
		t.Error("expected a decode error for a wrong type")
	}

	empty := &Request{Method: MethodPing}
	if err := empty.DecodeParams(&params); err != nil {
		t.Errorf("missing params must decode cleanly: %v", err)
	}
}

func TestSupportedProtocol(t *testing.T) {
	if !SupportedProtocol(version.ProtocolVersion) {
		t.Error("the current protocol version must be supported")
	}
	if SupportedProtocol(version.ProtocolVersion + 1) {
		t.Error("a newer protocol version must be rejected (§10.3)")
	}
	if SupportedProtocol(0) {
		t.Error("protocol version 0 must be rejected")
	}
}

func TestEvaluateResultUsesContractShape(t *testing.T) {
	result := EvaluateResult{
		AuditEventID:     action.Ref(1207),
		Decision:         action.OutcomeAllow,
		Class:            action.ClassApprovalMatch,
		Reason:           "matched approval 42",
		ApprovalID:       action.Ref(42),
		ResolutionStatus: action.StatusResolved,
		Explanation:      []string{"resolved: npm run cleanup -> rm -rf ./dist"},
		UserMessage:      "Intenter: auto-allowed (approval 42)",
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"audit_event_id", "decision", "class", "reason", "approval_id", "resolution_status", "explanation", "user_message"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("missing key %q in %s", key, raw)
		}
	}
	if decoded["decision"] != "allow" {
		t.Errorf("decision = %v, want the lower-case wire form", decoded["decision"])
	}
}

func TestFramerRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	framer := NewFramer(&buf)

	req, _ := NewRequest("abc", MethodPing, nil)
	if err := framer.Write(req); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !bytes.HasSuffix(buf.Bytes(), []byte("\n")) {
		t.Error("a framed message must end with a newline (§10.2)")
	}
	if bytes.Count(buf.Bytes(), []byte("\n")) != 1 {
		t.Error("exactly one newline per message")
	}

	var got Request
	if err := NewFramer(&buf).Read(&got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Method != MethodPing || got.RequestID != "abc" {
		t.Errorf("round trip = %+v", got)
	}
}

func TestFramerRejectsOversizedMessages(t *testing.T) {
	oversized := strings.Repeat("x", MaxMessageBytes+10)
	reader := strings.NewReader(`{"method":"` + oversized + `"}` + "\n")

	var got Request
	if err := NewFramer(struct {
		io.Reader
		io.Writer
	}{Reader: reader, Writer: io.Discard}).Read(&got); !errors.Is(err, ErrMessageTooLarge) {
		t.Errorf("read error = %v, want ErrMessageTooLarge", err)
	}

	var buf bytes.Buffer
	err := NewFramer(&buf).Write(&Request{Method: oversized})
	if !errors.Is(err, ErrMessageTooLarge) {
		t.Errorf("write error = %v, want ErrMessageTooLarge", err)
	}
}

func TestFramerReadsMessageWithoutTrailingNewline(t *testing.T) {
	reader := strings.NewReader(`{"protocol_version":1,"method":"ping"}`)
	var got Request
	if err := NewFramer(struct {
		io.Reader
		io.Writer
	}{Reader: reader, Writer: io.Discard}).Read(&got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Method != MethodPing {
		t.Errorf("method = %q", got.Method)
	}
}

func TestFramerReportsEOFOnEmptyStream(t *testing.T) {
	var got Request
	err := NewFramer(struct {
		io.Reader
		io.Writer
	}{Reader: strings.NewReader(""), Writer: io.Discard}).Read(&got)
	if !errors.Is(err, io.EOF) {
		t.Errorf("error = %v, want io.EOF", err)
	}
}

func TestFramerRejectsInvalidJSON(t *testing.T) {
	var got Request
	err := NewFramer(struct {
		io.Reader
		io.Writer
	}{Reader: strings.NewReader("not json\n"), Writer: io.Discard}).Read(&got)
	if err == nil || errors.Is(err, io.EOF) {
		t.Errorf("error = %v, want a decode error", err)
	}
}
