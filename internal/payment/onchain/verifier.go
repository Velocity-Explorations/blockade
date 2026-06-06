package onchain

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/TheFutonEng/btc-paywall/internal/payment"
	"github.com/TheFutonEng/btc-paywall/internal/store"
)

const (
	authScheme    = "BTC-Onchain "
	wwwAuthHeader = "WWW-Authenticate"

	// pendingTTL is how long an issued address remains valid for payment.
	// Addresses not paid within this window are evicted from the pending store.
	pendingTTL = time.Hour

	// cleanupInterval controls how often the background goroutine sweeps for
	// expired pending entries.
	cleanupInterval = 5 * time.Minute
)

// Verifier implements payment.PaymentVerifier using on-chain Bitcoin payments.
// Each request requires a fresh on-chain payment to a newly generated address.
// minConf controls how many block confirmations are required (0 = mempool).
// Issued addresses expire after pendingTTL if no payment is received.
type Verifier struct {
	btc     *Client
	minConf int
	st      store.Store
	ps      store.PendingStore

	// mu serialises VerifyProof to prevent TOCTOU on concurrent calls for
	// the same address (get-pending → check-payment → delete-pending → mark-used).
	mu sync.Mutex
}

// NewVerifier creates a Verifier backed by the given bitcoind Client.
// minConf is the minimum number of block confirmations required before a payment
// is accepted; pass 0 to accept unconfirmed mempool transactions.
// st records spent addresses for anti-replay; ps persists issued-but-unpaid
// addresses so they survive proxy restarts. Pass store.NewMemStore() for both
// for in-process-only state, or store.OpenSQLite(path) for persistence.
// A background goroutine evicts unpaid addresses after pendingTTL.
func NewVerifier(btc *Client, minConf int, st store.Store, ps store.PendingStore) *Verifier {
	v := &Verifier{
		btc:     btc,
		minConf: minConf,
		st:      st,
		ps:      ps,
	}
	go v.cleanupLoop()
	return v
}

// cleanupLoop periodically evicts expired entries from the pending store.
// It runs for the lifetime of the process — no shutdown signal is needed for a POC.
func (v *Verifier) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		_ = v.ps.PruneExpiredPending(time.Now())
	}
}

// IssueChallenge generates a fresh Bitcoin address and returns a 402 response.
// The WWW-Authenticate header carries the address, required amount, and expiry
// so the client knows where to send the payment and for how long the address is valid.
func (v *Verifier) IssueChallenge(w http.ResponseWriter, r *http.Request) error {
	priceSats, ok := payment.PriceFromContext(r.Context())
	if !ok || priceSats <= 0 {
		return fmt.Errorf("price not set in context")
	}

	addr, err := v.btc.GetNewAddress(r.Context())
	if err != nil {
		return fmt.Errorf("generate address: %w", err)
	}

	expiresAt := time.Now().Add(pendingTTL)
	if err := v.ps.AddPending(addr, priceSats, expiresAt); err != nil {
		return fmt.Errorf("store pending address: %w", err)
	}

	w.Header().Set(wwwAuthHeader, fmt.Sprintf(
		`BTC-Onchain address="%s", amount_sats="%d", expires_in="%d"`,
		addr, priceSats, int(pendingTTL.Seconds()),
	))
	w.WriteHeader(http.StatusPaymentRequired)
	return nil
}

// VerifyProof checks that the address in the token has received at least the
// required number of satoshis. Expired and already-used addresses are rejected.
func (v *Verifier) VerifyProof(token string) (bool, error) {
	addr := strings.TrimSpace(token)
	if addr == "" {
		return false, nil
	}

	used, err := v.st.IsUsed(addr)
	if err != nil {
		return false, fmt.Errorf("check used address: %w", err)
	}
	if used {
		return false, nil
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	entry, ok, err := v.ps.GetPending(addr)
	if err != nil {
		return false, fmt.Errorf("get pending address: %w", err)
	}
	if !ok {
		return false, nil
	}

	if time.Now().After(entry.ExpiresAt) {
		_ = v.ps.DeletePending(addr)
		return false, nil
	}

	received, err := v.btc.GetReceivedSats(context.Background(), addr, v.minConf)
	if err != nil {
		return false, fmt.Errorf("check received: %w", err)
	}
	if received < entry.Sats {
		return false, nil
	}

	if err := v.ps.DeletePending(addr); err != nil {
		return false, fmt.Errorf("delete pending address: %w", err)
	}
	if err := v.st.MarkUsed(addr); err != nil {
		return false, fmt.Errorf("mark address used: %w", err)
	}
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
