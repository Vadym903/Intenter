package updater

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"

	"github.com/Vadym903/Intenter/internal/platform"
)

// embeddedPublicKeyPEM is the pinned release signing key (research R-05,
// contracts/release-and-signing.md §2): the same PEM committed at the
// repository root as cosign.pub, embedded so verification does not depend on
// finding that file on the machine it is protecting.
//
//go:embed cosign.pub
var embeddedPublicKeyPEM []byte

// EnvSigningKeyFile lets a test point signature verification at a different
// key pair. Honored only under INTENTER_TEST_MODE=1 — the same gate as the
// source overrides in check.go (audit AG-08): a real installation always
// verifies against the pinned release key, never a key named by a variable
// that could be set by anything else on the machine.
const EnvSigningKeyFile = "INTENTER_SIGNING_KEY_FILE"

// signatureLimit bounds the .sig file, which is a few dozen bytes of base64;
// anything approaching this is not one of ours.
const signatureLimit = 4 << 10

// verifyingKey resolves the key checksums.txt.sig is verified against: the
// caller's explicit key if it gave one, else the test-mode file override, else
// the key this build was compiled with.
func verifyingKey(override *ecdsa.PublicKey) (*ecdsa.PublicKey, error) {
	if override != nil {
		return override, nil
	}
	if platform.TestMode() {
		if path := strings.TrimSpace(os.Getenv(EnvSigningKeyFile)); path != "" {
			pemBytes, err := os.ReadFile(path)
			if err != nil {
				return nil, failf(ExitSignature, "updater: read %s: %w", path, err)
			}
			return parsePublicKey(pemBytes)
		}
	}
	return parsePublicKey(embeddedPublicKeyPEM)
}

// parsePublicKey decodes a PEM-encoded SPKI public key and requires it to be
// ECDSA, the only algorithm the release process signs with.
func parsePublicKey(pemBytes []byte) (*ecdsa.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, failf(ExitSignature, "updater: the signing key is not valid PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, failf(ExitSignature, "updater: unreadable signing key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PublicKey)
	if !ok {
		return nil, failf(ExitSignature, "updater: the signing key is not ECDSA")
	}
	return key, nil
}

// verifySignature confirms that sigPath holds a valid base64 ASN.1 ECDSA-P256
// signature, made by pub, over the SHA-256 of the exact bytes of
// checksumsPath — the format `cosign sign-blob --key` produces (research
// R-05).
//
// This is the check the checksums file's trustworthiness rests on: without
// it, checksums.txt travels the same channel as the archive it vouches for,
// so an attacker able to point downloads elsewhere could serve a matching
// pair of both (audit AG-08).
func verifySignature(checksumsPath, sigPath string, pub *ecdsa.PublicKey) error {
	checksums, err := os.ReadFile(checksumsPath)
	if err != nil {
		return failf(ExitSignature, "updater: read %s: %w", checksumsPath, err)
	}
	encoded, err := os.ReadFile(sigPath)
	if err != nil {
		return failf(ExitSignature, "updater: read %s: %w", filepath.Base(sigPath), err)
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		return failf(ExitSignature, "updater: %s is not valid base64: %w", filepath.Base(sigPath), err)
	}

	digest := sha256.Sum256(checksums)
	if !ecdsa.VerifyASN1(pub, digest[:], signature) {
		return failf(ExitSignature,
			"updater: signature verification failed for %s\n"+
				"nothing was installed; the release may have been tampered with",
			filepath.Base(checksumsPath))
	}
	return nil
}
