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
	// Addresses not paid within this window are evicted from the pending map.
	pendingTTL = time.Hour

	// cleanupInterval controls how often the background goroutine sweeps for
	// expired pending entries.
	cleanupInterval = 5 * time.Minute
)

type pendingEntry struct {
	sats      int64
	expiresAt time.Time
}

// Verifier implements payment.PaymentVerifier using on-chain Bitcoin payments.
// Each request requires a fresh on-chain payment to a newly generated address.
// minConf controls how many block confirmations are required (0 = mempool).
// Issued addresses expire after pendingTTL if no payment is received.
type Verifier struct {
	btc     *Client
	minConf int
	st      store.Store

	mu      sync.Mutex
	pending map[string]pendingEntry // address → entry (issued but not yet spent)
}

// NewVerifier creates a Verifier backed by the given bitcoind Client.
// minConf is the minimum number of block confirmations required before a payment
// is accepted; pass 0 to accept unconfirmed mempool transactions.
// st records spent addresses for anti-replay; pass store.NewMemStore() for
// in-process-only state or store.OpenSQLite(path) to persist across restarts.
// A background goroutine evicts unpaid addresses after pendingTTL.
func NewVerifier(btc *Client, minConf int, st store.Store) *Verifier {
	v := &Verifier{
		btc:     btc,
		minConf: minConf,
		st:      st,
		pending: make(map[string]pendingEntry),
	}
	go v.cleanupLoop()
	return v
}

// cleanupLoop periodically evicts expired entries from the pending map.
// It runs for the lifetime of the process — no shutdown signal is needed for a POC.
func (v *Verifier) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		v.mu.Lock()
		for addr, entry := range v.pending {
			if now.After(entry.expiresAt) {
				delete(v.pending, addr)
			}
		}
		v.mu.Unlock()
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

	v.mu.Lock()
	v.pending[addr] = pendingEntry{sats: priceSats, expiresAt: expiresAt}
	v.mu.Unlock()

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

	entry, ok := v.pending[addr]
	if !ok {
		return false, nil
	}

	if time.Now().After(entry.expiresAt) {
		delete(v.pending, addr)
		return false, nil
	}

	received, err := v.btc.GetReceivedSats(context.Background(), addr, v.minConf)
	if err != nil {
		return false, fmt.Errorf("check received: %w", err)
	}
	if received < entry.sats {
		return false, nil
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
