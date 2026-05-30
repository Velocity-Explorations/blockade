package proxy

import (
	"fmt"
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
)

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
	verifier payment.PaymentVerifier
	routes   []route

	// Per-IP rate limiting on the challenge (402) path. nil = disabled.
	limitRPS   float64
	limitBurst int
	mu         sync.Mutex
	limiters   map[string]*ipLimiter
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
		h.routes = append(h.routes, route{
			pathPrefix: r.PathPrefix,
			priceSats:  r.PriceSats,
			rp:         httputil.NewSingleHostReverseProxy(upstream),
		})
	}
	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := h.matchRoute(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

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
		// Don't leak the proxy's own credentials to the upstream;
		// upstream may have its own Authorization scheme (e.g. Keycloak Basic).
		r.Header.Del("Authorization")
		rt.rp.ServeHTTP(w, r)
		return
	}

	// Rate-limit unauthenticated (challenge) requests per source IP.
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

func (h *Handler) matchRoute(path string) (*route, bool) {
	for i := range h.routes {
		if strings.HasPrefix(path, h.routes[i].pathPrefix) {
			return &h.routes[i], true
		}
	}
	return nil, false
}

// getLimiter returns the rate limiter for ip, creating one if needed.
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

// startLimiterCleanup runs a background goroutine that evicts IP entries not
// seen in the last 10 minutes, preventing unbounded map growth.
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

// clientIP extracts the client IP address from a request, stripping the port.
// For deployments behind a trusted reverse proxy, consider reading
// X-Real-IP or X-Forwarded-For instead.
func clientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
