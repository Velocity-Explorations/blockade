package onchain

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/TheFutonEng/btc-paywall/internal/payment"
)

const (
	authScheme    = "BTC-Onchain "
	wwwAuthHeader = "WWW-Authenticate"
)

// Verifier implements payment.PaymentVerifier using on-chain Bitcoin payments.
// Each request requires a fresh on-chain payment to a newly generated address.
// minConf controls how many block confirmations are required (0 = mempool).
type Verifier struct {
	btc     *Client
	minConf int

	mu      sync.Mutex
	pending map[string]int64 // address → required sats (issued but not yet spent)
	used    map[string]bool  // spent addresses (anti-replay)
}

// NewVerifier creates a Verifier backed by the given bitcoind Client.
// minConf is the minimum number of block confirmations required before a payment
// is accepted; pass 0 to accept unconfirmed mempool transactions.
func NewVerifier(btc *Client, minConf int) *Verifier {
	return &Verifier{
		btc:     btc,
		minConf: minConf,
		pending: make(map[string]int64),
		used:    make(map[string]bool),
	}
}

// IssueChallenge generates a fresh Bitcoin address and returns a 402 response.
// The WWW-Authenticate header carries the address and required amount so the
// client knows where to send the payment.
func (v *Verifier) IssueChallenge(w http.ResponseWriter, r *http.Request) error {
	priceSats, ok := payment.PriceFromContext(r.Context())
	if !ok || priceSats <= 0 {
		return fmt.Errorf("price not set in context")
	}

	addr, err := v.btc.GetNewAddress(r.Context())
	if err != nil {
		return fmt.Errorf("generate address: %w", err)
	}

	v.mu.Lock()
	v.pending[addr] = priceSats
	v.mu.Unlock()

	w.Header().Set(wwwAuthHeader, fmt.Sprintf(`BTC-Onchain address="%s", amount_sats="%d"`, addr, priceSats))
	w.WriteHeader(http.StatusPaymentRequired)
	return nil
}

// VerifyProof checks that the address in the token has received at least the
// required number of satoshis (including mempool). The address is then marked
// as used to prevent replay.
func (v *Verifier) VerifyProof(token string) (bool, error) {
	addr := strings.TrimSpace(token)
	if addr == "" {
		return false, nil
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	if v.used[addr] {
		return false, nil
	}

	required, ok := v.pending[addr]
	if !ok {
		return false, nil
	}

	received, err := v.btc.GetReceivedSats(context.Background(), addr, v.minConf)
	if err != nil {
		return false, fmt.Errorf("check received: %w", err)
	}
	if received < required {
		return false, nil
	}

	v.used[addr] = true
	return true, nil
}

// ExtractToken parses "BTC-Onchain <address>" from an Authorization header.
// Returns ("", false) if the header uses a different scheme.
func (v *Verifier) ExtractToken(authHeader string) (string, bool) {
	if !strings.HasPrefix(authHeader, authScheme) {
		return "", false
	}
	return strings.TrimPrefix(authHeader, authScheme), true
}
