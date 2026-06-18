package proxy

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/TheFutonEng/btc-paywall/internal/config"
	"github.com/TheFutonEng/btc-paywall/internal/payment"
	"github.com/TheFutonEng/btc-paywall/internal/store"
)

// corsHeaders are the response headers this proxy manages itself. They are
// stripped from upstream responses (see New) so the browser never sees the
// proxy's value and the upstream's value duplicated on the same response.
var corsHeaders = []string{
	"Access-Control-Allow-Origin",
	"Access-Control-Allow-Methods",
	"Access-Control-Allow-Headers",
	"Access-Control-Allow-Credentials",
	"Access-Control-Expose-Headers",
	"Access-Control-Max-Age",
}

// route pairs a path prefix with a reverse proxy to its upstream and the
// price in satoshis required for access.
type route struct {
	pathPrefix string
	priceSats  int64
	rp         *httputil.ReverseProxy
}

// ipLimiter wraps a token-bucket rate limiter with a last-seen timestamp
// used to evict stale entries from the per-IP map.
type ipLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// Handler is an http.Handler that gates all requests behind payment.
// Unauthenticated requests receive a 402 challenge. Authenticated requests
// are verified and forwarded to the matching upstream.
type Handler struct {
	verifier   payment.PaymentVerifier
	credIssuer payment.CredentialIssuer // nil when curve is disabled
	routes     []route

	// Per-IP rate limiting on the challenge (402) path. nil = disabled.
	limitRPS   float64
	limitBurst int
	mu         sync.Mutex
	limiters   map[string]*ipLimiter

	// v2 cost curve state. All nil/zero when curve is disabled.
	curve              *config.CurveConfig
	principals         store.PrincipalStore
	anonMu             sync.Mutex
	anonCounts         map[string]int
	enrollMu           sync.Mutex
	pendingEnrollments map[string]bool // payment hash hex -> true
}

// New builds a Handler from the given config routes, verifier, and optional
// rate-limit config. Pass nil for rl to disable rate limiting.
func New(routes []config.RouteConfig, verifier payment.PaymentVerifier, rl *config.RateLimitConfig) (*Handler, error) {
	h := &Handler{verifier: verifier}
	if rl != nil {
		h.limitRPS = rl.RequestsPerSecond
		h.limitBurst = rl.Burst
		h.limiters = make(map[string]*ipLimiter)
		h.startLimiterCleanup()
	}
	for _, r := range routes {
		upstream, err := url.Parse(r.Upstream)
		if err != nil {
			return nil, fmt.Errorf("parse upstream %q: %w", r.Upstream, err)
		}
		rp := httputil.NewSingleHostReverseProxy(upstream)
		// This proxy sets its own CORS headers on every response (see ServeHTTP).
		// Strip any CORS headers the upstream emits so the browser never sees
		// duplicate values — httpbin, for example, reflects the request Origin
		// into Access-Control-Allow-Origin, which would collide with our "*" and
		// cause the browser to reject the response ("multiple values ... only one
		// is allowed").
		rp.ModifyResponse = func(resp *http.Response) error {
			for _, h := range corsHeaders {
				resp.Header.Del(h)
			}
			return nil
		}
		h.routes = append(h.routes, route{
			pathPrefix: r.PathPrefix,
			priceSats:  r.PriceSats,
			rp:         rp,
		})
	}
	return h, nil
}

// EnableCurve activates v2 metering. The verifier must implement
// payment.CredentialIssuer.
func (h *Handler) EnableCurve(curve *config.CurveConfig, ps store.PrincipalStore) {
	ci, ok := h.verifier.(payment.CredentialIssuer)
	if !ok {
		log.Fatal("curve metering requires a verifier that implements CredentialIssuer")
	}
	h.credIssuer = ci
	h.curve = curve
	h.principals = ps
	h.anonCounts = make(map[string]int)
	h.pendingEnrollments = make(map[string]bool)
	h.startAnonCleanup()
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// CORS — required for the browser demo, which fetches from a different port.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, L402-Credential")
	w.Header().Set("Access-Control-Expose-Headers", "WWW-Authenticate, L402-Credential")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Settlement-status endpoint (available when CredentialIssuer is present).
	if h.credIssuer != nil && r.URL.Path == "/l402/settlement" {
		h.handleSettlement(w, r)
		return
	}

	rt, ok := h.matchRoute(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	if h.curve != nil {
		h.serveMetered(w, r, rt)
		return
	}

	h.serveFlat(w, r, rt)
}

// serveFlat is the v1 path: flat toll, single-use tokens.
func (h *Handler) serveFlat(w http.ResponseWriter, r *http.Request, rt *route) {
	authHeader := r.Header.Get("Authorization")
	token, hasToken := h.verifier.ExtractToken(authHeader)

	if hasToken {
		valid, err := h.verifier.VerifyProof(token)
		if err != nil {
			http.Error(w, "payment verification error", http.StatusInternalServerError)
			return
		}
		if !valid {
			http.Error(w, "invalid or already-used payment token", http.StatusUnauthorized)
			return
		}
		r.Header.Del("Authorization")
		rt.rp.ServeHTTP(w, r)
		return
	}

	if h.limiters != nil {
		if !h.getLimiter(clientIP(r)).Allow() {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
	}

	ctx := payment.WithPrice(r.Context(), rt.priceSats)
	if err := h.verifier.IssueChallenge(w, r.WithContext(ctx)); err != nil {
		http.Error(w, "failed to create payment challenge", http.StatusInternalServerError)
	}
}

// serveMetered is the v2 path: three-tier routing with the cost curve.
func (h *Handler) serveMetered(w http.ResponseWriter, r *http.Request, rt *route) {
	credB64 := r.Header.Get("L402-Credential")
	authHeader := r.Header.Get("Authorization")
	token, hasToken := h.verifier.ExtractToken(authHeader)

	// --- Tier 3: credentialed request ---
	if credB64 != "" {
		principalID, err := h.credIssuer.ValidateCredential(credB64)
		if err != nil {
			http.Error(w, "invalid credential", http.StatusUnauthorized)
			return
		}

		rec, found, err := h.principals.GetPrincipal(principalID)
		if err != nil {
			http.Error(w, "principal lookup error", http.StatusInternalServerError)
			return
		}
		if !found {
			http.Error(w, "unknown principal", http.StatusUnauthorized)
			return
		}

		nextN := rec.RequestCount + 1
		price := payment.CurvePrice(nextN, h.curve.FloorTollSats, h.curve.EscalationSlope)

		if !hasToken {
			ctx := payment.WithPrice(r.Context(), price)
			if err := h.verifier.IssueChallenge(w, r.WithContext(ctx)); err != nil {
				http.Error(w, "failed to create payment challenge", http.StatusInternalServerError)
			}
			return
		}

		valid, err := h.verifier.VerifyProof(token)
		if err != nil {
			http.Error(w, "payment verification error", http.StatusInternalServerError)
			return
		}
		if !valid {
			http.Error(w, "invalid or already-used payment token", http.StatusUnauthorized)
			return
		}

		if _, err := h.principals.IncrementRequestCount(principalID); err != nil {
			http.Error(w, "metering error", http.StatusInternalServerError)
			return
		}

		r.Header.Del("Authorization")
		r.Header.Del("L402-Credential")
		rt.rp.ServeHTTP(w, r)
		return
	}

	// --- No credential ---
	if hasToken {
		// Check if this is an enrollment payment completion.
		phHex, err := h.credIssuer.PaymentHashFromToken(token)
		if err == nil && h.isPendingEnrollment(phHex) {
			h.completeEnrollment(w, token, phHex)
			return
		}

		// Anonymous access attempt with a valid floor-price token.
		valid, err := h.verifier.VerifyProof(token)
		if err != nil {
			http.Error(w, "payment verification error", http.StatusInternalServerError)
			return
		}
		if !valid {
			http.Error(w, "invalid or already-used payment token", http.StatusUnauthorized)
			return
		}
		h.incrementAnonCount(clientIP(r))
		r.Header.Del("Authorization")
		rt.rp.ServeHTTP(w, r)
		return
	}

	// Rate-limit unauthenticated requests.
	if h.limiters != nil {
		if !h.getLimiter(clientIP(r)).Allow() {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
	}

	// Decide: floor challenge or enrollment challenge?
	ip := clientIP(r)
	if h.getAnonCount(ip) >= h.curve.AnonymousCap {
		ctx := payment.WithPrice(r.Context(), h.curve.EnrollmentStakeSats)
		phHex, err := h.credIssuer.IssueEnrollmentChallenge(w, r.WithContext(ctx))
		if err != nil {
			http.Error(w, "failed to create enrollment challenge", http.StatusInternalServerError)
			return
		}
		h.trackPendingEnrollment(phHex)
		return
	}

	ctx := payment.WithPrice(r.Context(), h.curve.FloorTollSats)
	if err := h.verifier.IssueChallenge(w, r.WithContext(ctx)); err != nil {
		http.Error(w, "failed to create payment challenge", http.StatusInternalServerError)
	}
}

func (h *Handler) completeEnrollment(w http.ResponseWriter, token, phHex string) {
	principalID, credB64, err := h.credIssuer.CompleteEnrollment(token)
	if err != nil {
		http.Error(w, "enrollment verification failed: "+err.Error(), http.StatusUnauthorized)
		return
	}

	h.removePendingEnrollment(phHex)

	if err := h.principals.CreatePrincipal(principalID, time.Now().Unix()); err != nil {
		http.Error(w, "failed to create principal", http.StatusInternalServerError)
		return
	}

	w.Header().Set("L402-Credential", credB64)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"credential":   credB64,
		"principal_id": principalID,
	})
}

// handleSettlement serves the settlement-status endpoint used by the manual
// fallback payment path.
func (h *Handler) handleSettlement(w http.ResponseWriter, r *http.Request) {
	phHex := r.URL.Query().Get("payment_hash")
	if phHex == "" {
		http.Error(w, "payment_hash query parameter required", http.StatusBadRequest)
		return
	}
	if _, err := hex.DecodeString(phHex); err != nil {
		http.Error(w, "invalid payment_hash hex", http.StatusBadRequest)
		return
	}

	settled, preimageHex, err := h.credIssuer.LookupSettlement(phHex)
	if err != nil {
		http.Error(w, "settlement lookup error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if settled {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"settled":  true,
			"preimage": preimageHex,
		})
	} else {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"settled": false,
		})
	}
}

// --- Anonymous count tracking ---

func (h *Handler) getAnonCount(ip string) int {
	h.anonMu.Lock()
	defer h.anonMu.Unlock()
	return h.anonCounts[ip]
}

func (h *Handler) incrementAnonCount(ip string) {
	h.anonMu.Lock()
	defer h.anonMu.Unlock()
	h.anonCounts[ip]++
}

func (h *Handler) startAnonCleanup() {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			h.anonMu.Lock()
			for ip := range h.anonCounts {
				delete(h.anonCounts, ip)
			}
			h.anonMu.Unlock()
		}
	}()
}

// --- Enrollment tracking ---

func (h *Handler) trackPendingEnrollment(phHex string) {
	h.enrollMu.Lock()
	defer h.enrollMu.Unlock()
	h.pendingEnrollments[phHex] = true
}

func (h *Handler) isPendingEnrollment(phHex string) bool {
	h.enrollMu.Lock()
	defer h.enrollMu.Unlock()
	return h.pendingEnrollments[phHex]
}

func (h *Handler) removePendingEnrollment(phHex string) {
	h.enrollMu.Lock()
	defer h.enrollMu.Unlock()
	delete(h.pendingEnrollments, phHex)
}

// --- Shared helpers ---

func (h *Handler) matchRoute(path string) (*route, bool) {
	for i := range h.routes {
		if strings.HasPrefix(path, h.routes[i].pathPrefix) {
			return &h.routes[i], true
		}
	}
	return nil, false
}

func (h *Handler) getLimiter(ip string) *rate.Limiter {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.limiters[ip]
	if !ok {
		e = &ipLimiter{limiter: rate.NewLimiter(rate.Limit(h.limitRPS), h.limitBurst)}
		h.limiters[ip] = e
	}
	e.lastSeen = time.Now()
	return e.limiter
}

func (h *Handler) startLimiterCleanup() {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			cutoff := time.Now().Add(-10 * time.Minute)
			h.mu.Lock()
			for ip, e := range h.limiters {
				if e.lastSeen.Before(cutoff) {
					delete(h.limiters, ip)
				}
			}
			h.mu.Unlock()
		}
	}()
}

func clientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
