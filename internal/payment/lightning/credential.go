package lightning

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"gopkg.in/macaroon.v2"
)

const credentialLocation = "blockaide-credential"

// mintCredential creates a long-lived credential macaroon whose identifier is
// the hex-encoded principal ID. Uses the credential root key, which is distinct
// from the payment root key so that payment macaroons cannot masquerade as
// credentials.
func (v *Verifier) mintCredential(principalID string) (string, error) {
	id := []byte(principalID)
	m, err := macaroon.New(v.credentialKey, id, credentialLocation, macaroon.LatestVersion)
	if err != nil {
		return "", fmt.Errorf("mint credential: %w", err)
	}
	raw, err := m.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("marshal credential: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// validateCredential decodes and verifies a base64-encoded credential macaroon,
// returning the principal ID from its identifier. Returns an error if the
// macaroon's HMAC chain does not verify against the credential root key.
func (v *Verifier) validateCredential(credB64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(credB64)
	if err != nil {
		return "", fmt.Errorf("decode credential base64: %w", err)
	}
	var m macaroon.Macaroon
	if err := m.UnmarshalBinary(raw); err != nil {
		return "", fmt.Errorf("unmarshal credential: %w", err)
	}
	if err := m.Verify(v.credentialKey, func(caveat string) error { return nil }, nil); err != nil {
		return "", fmt.Errorf("credential signature invalid: %w", err)
	}
	return string(m.Id()), nil
}

func newPrincipalID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
