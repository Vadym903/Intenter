package action

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// SHA256Hex returns the hex SHA-256 of b.
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// NormalizeText normalizes line endings so a Windows checkout and a Unix
// checkout of the same file produce the same fingerprint (research R-13).
func NormalizeText(b []byte) []byte {
	if !strings.ContainsRune(string(b), '\r') {
		return b
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	return []byte(s)
}

// HashText fingerprints text content with line-ending normalization.
func HashText(b []byte) string { return SHA256Hex(NormalizeText(b)) }

// HashString fingerprints a string value (configuration values, script text).
func HashString(s string) string { return HashText([]byte(s)) }

// ProjectID is the identity of a workspace: sha256 of its canonical root
// (PROTOTYPE_SPEC.md §16.2, research R-19).
func ProjectID(canonicalRoot string) string {
	return SHA256Hex([]byte(canonicalRoot))
}

// canonicalJSON encodes v with sorted object keys and no insignificant
// whitespace, so that the same logical value always produces the same bytes.
func canonicalJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var generic any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&generic); err != nil {
		return nil, err
	}
	var buf strings.Builder
	if err := writeCanonical(&buf, generic); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

func writeCanonical(buf *strings.Builder, v any) error {
	switch value := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		buf.WriteString(strconv.FormatBool(value))
	case json.Number:
		buf.WriteString(value.String())
	case string:
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		buf.Write(encoded)
	case []any:
		buf.WriteByte('[')
		for i, item := range value {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(value))
		for k := range value {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			encoded, err := json.Marshal(k)
			if err != nil {
				return err
			}
			buf.Write(encoded)
			buf.WriteByte(':')
			if err := writeCanonical(buf, value[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("canonical json: unsupported type %T", v)
	}
	return nil
}

// CanonicalJSON exposes the canonical encoder for callers that need stable
// bytes (audit payloads, tests).
func CanonicalJSON(v any) ([]byte, error) { return canonicalJSON(v) }

// actionKeyPayload is the exact field set hashed into action_key
// (PROTOTYPE_SPEC.md §20.2, data-model.md §3).
type actionKeyPayload struct {
	ProjectID     string            `json:"project_id"`
	EngineVersion int               `json:"engine_version"`
	SemanticOps   []string          `json:"semantic_ops"`
	Targets       []string          `json:"targets"`
	Effects       []string          `json:"effects"`
	Network       []string          `json:"network"`
	Fingerprints  map[string]string `json:"fingerprints"`
}

// ActionKey computes the canonical identity of a resolved action. EXACT
// matching may short-circuit on equality of this key, but MUST agree with the
// field-wise rules of §20.3.
func ActionKey(a *ResolvedAction, projectID string, engineVersion int) (string, error) {
	ops := make([]string, 0, len(a.SemanticOps))
	for _, op := range a.SemanticOps {
		ops = append(ops, string(op))
	}

	envelope := a.Envelope()
	effects := make([]string, 0, len(envelope))
	for _, entry := range envelope {
		effects = append(effects, entry.Key())
	}
	sort.Strings(effects)

	network := make([]string, 0)
	for _, n := range a.Network() {
		network = append(network, n.Key())
	}
	sort.Strings(network)

	payload := actionKeyPayload{
		ProjectID:     projectID,
		EngineVersion: engineVersion,
		SemanticOps:   ops,
		Targets:       a.DisplayTargets(),
		Effects:       effects,
		Network:       network,
		Fingerprints:  a.FingerprintMap(),
	}

	encoded, err := canonicalJSON(payload)
	if err != nil {
		return "", err
	}
	return SHA256Hex(encoded), nil
}

// HashPairs fingerprints an ordered set of (path, hash) pairs, used for
// aggregate keys such as gradle-config and maven-config
// (PROTOTYPE_SPEC.md §15.5.2, data-model.md §3).
func HashPairs(pairs map[string]string) string {
	keys := make([]string, 0, len(pairs))
	for k := range pairs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf strings.Builder
	for _, k := range keys {
		buf.WriteString(k)
		buf.WriteByte('\x00')
		buf.WriteString(pairs[k])
		buf.WriteByte('\n')
	}
	return SHA256Hex([]byte(buf.String()))
}
