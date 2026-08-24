package updater

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/Vadym903/Intenter/internal/platform"
)

// The updater trusts exactly one key to say what a release is: the one
// committed at the repository root as cosign.pub. These tests confirm that
// trust is anchored to that file, and that anything short of a valid
// signature from it — a stale signature, a missing file, an unrelated key —
// is refused (research R-05, contracts/release-and-signing.md §2-3).

func TestTheEmbeddedKeyMatchesTheRepositoryCosignPub(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("..", "..", "cosign.pub"))
	if err != nil {
		t.Fatalf("read repository cosign.pub: %v", err)
	}
	if string(embeddedPublicKeyPEM) != string(want) {
		t.Error("internal/updater/cosign.pub has drifted from the repository root copy")
	}
}

func TestTheEmbeddedKeyIsAValidECDSAKey(t *testing.T) {
	key, err := parsePublicKey(embeddedPublicKeyPEM)
	if err != nil {
		t.Fatalf("parse embedded key: %v", err)
	}
	if key.Curve != elliptic.P256() {
		t.Errorf("curve = %v, want P-256", key.Curve)
	}
}

// writeSigned writes content as checksums.txt and a matching checksums.txt.sig
// signed by priv, and returns the checksums.txt path.
func writeSigned(t *testing.T, dir, content string, priv *ecdsa.PrivateKey) string {
	t.Helper()
	checksumsPath := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(checksumsPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write checksums.txt: %v", err)
	}
	digest := sha256.Sum256([]byte(content))
	signature, err := ecdsa.SignASN1(rand.Reader, priv, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(signature)
	if err := os.WriteFile(checksumsPath+".sig", []byte(encoded), 0o644); err != nil {
		t.Fatalf("write checksums.txt.sig: %v", err)
	}
	return checksumsPath
}

func TestASignatureFromTheMatchingKeyVerifies(t *testing.T) {
	priv := testSigningKey(t)
	dir := t.TempDir()
	checksumsPath := writeSigned(t, dir, "d34db33f  intenter_0.2.0_darwin_arm64.tar.gz\n", priv)

	if err := verifySignature(checksumsPath, checksumsPath+".sig", &priv.PublicKey); err != nil {
		t.Errorf("verifySignature: %v", err)
	}
}

func TestATamperedChecksumsFileFailsVerification(t *testing.T) {
	priv := testSigningKey(t)
	dir := t.TempDir()
	checksumsPath := writeSigned(t, dir, "original content\n", priv)

	if err := os.WriteFile(checksumsPath, []byte("tampered content\n"), 0o644); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	err := verifySignature(checksumsPath, checksumsPath+".sig", &priv.PublicKey)
	if got := ExitCodeFor(err); got != ExitSignature {
		t.Errorf("exit code = %d, want %d", got, ExitSignature)
	}
}

func TestASignatureFromAnUnrelatedKeyFailsVerification(t *testing.T) {
	priv := testSigningKey(t)
	other := testSigningKey(t)
	dir := t.TempDir()
	checksumsPath := writeSigned(t, dir, "content\n", priv)

	err := verifySignature(checksumsPath, checksumsPath+".sig", &other.PublicKey)
	if got := ExitCodeFor(err); got != ExitSignature {
		t.Errorf("exit code = %d, want %d", got, ExitSignature)
	}
}

func TestAnUndecodableSignatureIsRefused(t *testing.T) {
	dir := t.TempDir()
	checksumsPath := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(checksumsPath, []byte("content\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(checksumsPath+".sig", []byte("not-base64!!!"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	priv := testSigningKey(t)

	err := verifySignature(checksumsPath, checksumsPath+".sig", &priv.PublicKey)
	if got := ExitCodeFor(err); got != ExitSignature {
		t.Errorf("exit code = %d, want %d", got, ExitSignature)
	}
}

func TestAMissingSignatureFileIsRefused(t *testing.T) {
	dir := t.TempDir()
	checksumsPath := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(checksumsPath, []byte("content\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	priv := testSigningKey(t)

	err := verifySignature(checksumsPath, checksumsPath+".sig", &priv.PublicKey)
	if got := ExitCodeFor(err); got != ExitSignature {
		t.Errorf("exit code = %d, want %d", got, ExitSignature)
	}
}

func TestANonECDSAKeyIsRejected(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})

	_, err = parsePublicKey(pemBytes)
	if got := ExitCodeFor(err); got != ExitSignature {
		t.Errorf("exit code = %d, want %d", got, ExitSignature)
	}
}

func TestUnreadablePEMIsRejected(t *testing.T) {
	_, err := parsePublicKey([]byte("not a PEM block"))
	if got := ExitCodeFor(err); got != ExitSignature {
		t.Errorf("exit code = %d, want %d", got, ExitSignature)
	}
}

// writeTestPublicKeyPEM writes pub as the PEM a signing-key override reads.
func writeTestPublicKeyPEM(t *testing.T, pub *ecdsa.PublicKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	path := filepath.Join(t.TempDir(), "test.pub")
	block := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	if err := os.WriteFile(path, block, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestTheSigningKeyFileOverrideIsInertOutsideTestMode(t *testing.T) {
	// Same gate as the other test-mode overrides (testmode.go, AG-08): a real
	// installation must never be steerable towards a different key by a
	// variable left in a shell profile.
	t.Setenv(platform.EnvTestMode, "")
	other := testSigningKey(t)
	t.Setenv(EnvSigningKeyFile, writeTestPublicKeyPEM(t, &other.PublicKey))

	key, err := verifyingKey(nil)
	if err != nil {
		t.Fatalf("verifyingKey: %v", err)
	}
	embedded, err := parsePublicKey(embeddedPublicKeyPEM)
	if err != nil {
		t.Fatalf("parse embedded key: %v", err)
	}
	if !key.Equal(embedded) {
		t.Error("INTENTER_SIGNING_KEY_FILE must do nothing without INTENTER_TEST_MODE=1")
	}
}

func TestTheSigningKeyFileOverrideAppliesInTestMode(t *testing.T) {
	t.Setenv(platform.EnvTestMode, "1")
	other := testSigningKey(t)
	t.Setenv(EnvSigningKeyFile, writeTestPublicKeyPEM(t, &other.PublicKey))

	key, err := verifyingKey(nil)
	if err != nil {
		t.Fatalf("verifyingKey: %v", err)
	}
	if !key.Equal(&other.PublicKey) {
		t.Error("the override must apply in test mode")
	}
}

func TestAnExplicitPublicKeyWinsOverTheEnvironmentOverride(t *testing.T) {
	t.Setenv(platform.EnvTestMode, "1")
	envKey := testSigningKey(t)
	t.Setenv(EnvSigningKeyFile, writeTestPublicKeyPEM(t, &envKey.PublicKey))
	explicit := testSigningKey(t)

	key, err := verifyingKey(&explicit.PublicKey)
	if err != nil {
		t.Fatalf("verifyingKey: %v", err)
	}
	if !key.Equal(&explicit.PublicKey) {
		t.Error("an explicit PublicKey must win over the environment override")
	}
}
