package lightning

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"gopkg.in/macaroon.v2"

	"github.com/TheFutonEng/btc-paywall/internal/payment"
	"github.com/TheFutonEng/btc-paywall/internal/store"
)

const (
	authScheme    = "L402 "
	wwwAuthHeader = "WWW-Authenticate"
)

// Verifier implements payment.PaymentVerifier and payment.CredentialIssuer
// using Lightning Network invoices and the L402 protocol. Random root keys
// are generated at startup; issued macaroons and credentials are only valid
// for the lifetime of this process.
type Verifier struct {
	lnd           *Client
	rootKey       []byte // signs per-request payment macaroons
	credentialKey []byte // signs long-lived enrollment credentials (distinct key prevents cross-use)
	st            store.Store
}

// NewVerifier creates a Verifier backed by the given lnd Client.
// st records spent tokens for anti-replay; pass store.NewMemStore() for
// in-process-only state or store.OpenSQLite(path) to persist across restarts.
func NewVerifier(lnd *Client, st store.Store) (*Verifier, error) {
	rootKey := make([]byte, 32)
	if _, err := rand.Read(rootKey); err != nil {
		return nil, fmt.Errorf("generate root key: %w", err)
	}
	credentialKey := make([]byte, 32)
	if _, err := rand.Read(credentialKey); err != nil {
		return nil, fmt.Errorf("generate credential key: %w", err)
	}
	return &Verifier{
		lnd:           lnd,
		rootKey:       rootKey,
		credentialKey: credentialKey,
		st:            st,
	}, nil
}

// IssueChallenge creates a Lightning invoice and writes a 402 response with
// the WWW-Authenticate: L402 header. The macaroon identifier is the payment
// hash, binding the credential to this specific invoice.
func (v *Verifier) IssueChallenge(w http.ResponseWriter, r *http.Request) error {
	priceSats, ok := payment.PriceFromContext(r.Context())
	if !ok || priceSats <= 0 {
		return fmt.Errorf("price not set in context")
	}

	payReq, paymentHash, err := v.lnd.AddInvoice(r.Context(), priceSats, "btc-paywall: "+r.URL.Path)
	if err != nil {
		return fmt.Errorf("create invoice: %w", err)
	}

	m, err := v.mintMacaroon(paymentHash)
	if err != nil {
		return fmt.Errorf("mint macaroon: %w", err)
	}

	raw, err := m.MarshalBinary()
	if err != nil {
		return fmt.Errorf("marshal macaroon: %w", err)
	}

	mac64 := base64.StdEncoding.EncodeToString(raw)
	w.Header().Set(wwwAuthHeader, fmt.Sprintf(`L402 macaroon="%s", invoice="%s"`, mac64, payReq))
	w.WriteHeader(http.StatusPaymentRequired)
	return nil
}

// VerifyProof validates an L402 token (everything after "L402 " in the
// Authorization header). It checks:
//  1. The preimage hashes to the payment hash in the macaroon identifier.
//  2. lnd confirms the invoice is settled (i.e. payment was received).
//  3. The token has not been used before (anti-replay).
func (v *Verifier) VerifyProof(tokenStr string) (bool, error) {
	tok, err := decodeToken(tokenStr)
	if err != nil {
		return false, fmt.Errorf("decode token: %w", err)
	}

	paymentHash, err := paymentHashFromID(tok.macaroon)
	if err != nil {
		return false, fmt.Errorf("extract payment hash: %w", err)
	}

	if !verifyPreimage(tok.preimage, paymentHash) {
		return false, nil
	}

	settled, err := v.lnd.IsSettled(context.Background(), paymentHash)
	if err != nil {
		return false, fmt.Errorf("check invoice: %w", err)
	}
	if !settled {
		return false, nil
	}

	hashHex := hex.EncodeToString(paymentHash)
	used, err := v.st.IsUsed(hashHex)
	if err != nil {
		return false, fmt.Errorf("check used token: %w", err)
	}
	if used {
		return false, nil
	}
	if err := v.st.MarkUsed(hashHex); err != nil {
		return false, fmt.Errorf("mark token used: %w", err)
	}

	return true, nil
}

func (v *Verifier) mintMacaroon(paymentHash []byte) (*macaroon.Macaroon, error) {
	id := []byte(hex.EncodeToString(paymentHash))
	m, err := macaroon.New(v.rootKey, id, "btc-paywall", macaroon.LatestVersion)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// ExtractToken strips the "L402 " prefix from an Authorization header value.
// Returns ("", false) if the header is absent or uses a different scheme.
func ExtractToken(authHeader string) (string, bool) {
	if !strings.HasPrefix(authHeader, authScheme) {
		return "", false
	}
	return strings.TrimPrefix(authHeader, authScheme), true
}

// ExtractToken implements payment.PaymentVerifier by delegating to the
// package-level ExtractToken function.
func (v *Verifier) ExtractToken(authHeader string) (string, bool) {
	return ExtractToken(authHeader)
}

// ---------------------------------------------------------------------------
// payment.CredentialIssuer implementation
// ---------------------------------------------------------------------------

// IssueEnrollmentChallenge writes a 402 challenge for the enrollment stake,
// adding type="enrollment" so the client can distinguish it from a per-request
// toll. Returns the payment hash hex for enrollment tracking.
func (v *Verifier) IssueEnrollmentChallenge(w http.ResponseWriter, r *http.Request) (string, error) {
	priceSats, ok := payment.PriceFromContext(r.Context())
	if !ok || priceSats <= 0 {
		return "", fmt.Errorf("price not set in context")
	}

	payReq, paymentHash, err := v.lnd.AddInvoice(r.Context(), priceSats, "blockaide enrollment: "+r.URL.Path)
	if err != nil {
		return "", fmt.Errorf("create enrollment invoice: %w", err)
	}

	m, err := v.mintMacaroon(paymentHash)
	if err != nil {
		return "", fmt.Errorf("mint macaroon: %w", err)
	}

	raw, err := m.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("marshal macaroon: %w", err)
	}

	mac64 := base64.StdEncoding.EncodeToString(raw)
	w.Header().Set(wwwAuthHeader, fmt.Sprintf(
		`L402 macaroon="%s", invoice="%s", type="enrollment"`, mac64, payReq,
	))
	w.WriteHeader(http.StatusPaymentRequired)
	return hex.EncodeToString(paymentHash), nil
}

// CompleteEnrollment verifies an enrollment payment and mints a credential.
// It performs the same three checks as VerifyProof (preimage, settlement,
// anti-replay) and then generates a credential macaroon with a fresh
// principal identifier.
func (v *Verifier) CompleteEnrollment(tokenStr string) (string, string, error) {
	tok, err := decodeToken(tokenStr)
	if err != nil {
		return "", "", fmt.Errorf("decode token: %w", err)
	}

	paymentHash, err := paymentHashFromID(tok.macaroon)
	if err != nil {
		return "", "", fmt.Errorf("extract payment hash: %w", err)
	}

	if !verifyPreimage(tok.preimage, paymentHash) {
		return "", "", fmt.Errorf("preimage does not match payment hash")
	}

	settled, err := v.lnd.IsSettled(context.Background(), paymentHash)
	if err != nil {
		return "", "", fmt.Errorf("check invoice: %w", err)
	}
	if !settled {
		return "", "", fmt.Errorf("invoice not settled")
	}

	hashHex := hex.EncodeToString(paymentHash)
	used, err := v.st.IsUsed(hashHex)
	if err != nil {
		return "", "", fmt.Errorf("check used token: %w", err)
	}
	if used {
		return "", "", fmt.Errorf("enrollment token already used")
	}
	if err := v.st.MarkUsed(hashHex); err != nil {
		return "", "", fmt.Errorf("mark token used: %w", err)
	}

	principalID, err := newPrincipalID()
	if err != nil {
		return "", "", fmt.Errorf("generate principal id: %w", err)
	}

	credB64, err := v.mintCredential(principalID)
	if err != nil {
		return "", "", fmt.Errorf("mint credential: %w", err)
	}

	return principalID, credB64, nil
}

// ValidateCredential implements payment.CredentialIssuer.
func (v *Verifier) ValidateCredential(credB64 string) (string, error) {
	return v.validateCredential(credB64)
}

// PaymentHashFromToken extracts the payment hash from a raw L402 token
// without performing full verification.
func (v *Verifier) PaymentHashFromToken(tokenStr string) (string, error) {
	tok, err := decodeToken(tokenStr)
	if err != nil {
		return "", err
	}
	ph, err := paymentHashFromID(tok.macaroon)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(ph), nil
}

// LookupSettlement checks LND for invoice settlement and returns the preimage.
func (v *Verifier) LookupSettlement(paymentHashHex string) (bool, string, error) {
	ph, err := hex.DecodeString(paymentHashHex)
	if err != nil {
		return false, "", fmt.Errorf("decode payment hash: %w", err)
	}
	settled, preimage, err := v.lnd.LookupSettlement(context.Background(), ph)
	if err != nil {
		return false, "", err
	}
	if !settled {
		return false, "", nil
	}
	return true, hex.EncodeToString(preimage), nil
}
