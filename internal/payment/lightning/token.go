package lightning

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"gopkg.in/macaroon.v2"
)

// token represents a decoded L402 credential.
type token struct {
	macaroon *macaroon.Macaroon
	preimage []byte
}

// decodeToken parses "<base64(macaroon)>:<hex(preimage)>" into a token.
func decodeToken(s string) (*token, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid token format: expected <macaroon>:<preimage>")
	}

	raw, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode macaroon base64: %w", err)
	}
	var m macaroon.Macaroon
	if err := m.UnmarshalBinary(raw); err != nil {
		return nil, fmt.Errorf("unmarshal macaroon: %w", err)
	}

	preimage, err := hex.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode preimage hex: %w", err)
	}
	if len(preimage) != 32 {
		return nil, fmt.Errorf("preimage must be 32 bytes, got %d", len(preimage))
	}

	return &token{macaroon: &m, preimage: preimage}, nil
}

// paymentHashFromID extracts the payment hash stored as the macaroon identifier.
// The identifier is the hex-encoded payment hash set when the macaroon was minted.
func paymentHashFromID(m *macaroon.Macaroon) ([]byte, error) {
	id := string(m.Id())
	h, err := hex.DecodeString(id)
	if err != nil {
		return nil, fmt.Errorf("macaroon id is not a hex payment hash: %w", err)
	}
	if len(h) != 32 {
		return nil, fmt.Errorf("payment hash must be 32 bytes, got %d", len(h))
	}
	return h, nil
}

// verifyPreimage checks that SHA256(preimage) == paymentHash.
func verifyPreimage(preimage, paymentHash []byte) bool {
	h := sha256.Sum256(preimage)
	return h == [32]byte(paymentHash)
}
