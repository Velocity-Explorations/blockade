package proxy

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/TheFutonEng/btc-paywall/internal/config"
	"github.com/TheFutonEng/btc-paywall/internal/payment"
	"github.com/TheFutonEng/btc-paywall/internal/payment/lightning"
)

// route pairs a path prefix with a reverse proxy to its upstream and the
// price in satoshis required for access.
type route struct {
	pathPrefix string
	priceSats  int64
	rp         *httputil.ReverseProxy
}

// Handler is an http.Handler that gates all requests behind L402 payment.
// Requests without a valid token receive a 402 with a Lightning invoice.
// Requests with a valid token are forwarded to the matching upstream.
type Handler struct {
	verifier payment.PaymentVerifier
	routes   []route
}

// New builds a Handler from the given config routes and verifier.
func New(routes []config.RouteConfig, verifier payment.PaymentVerifier) (*Handler, error) {
	h := &Handler{verifier: verifier}
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
	token, hasToken := lightning.ExtractToken(authHeader)

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
		// Don't leak the proxy's own L402 credentials to the upstream;
		// upstream may have its own Authorization scheme (e.g. Keycloak Basic).
		r.Header.Del("Authorization")
		rt.rp.ServeHTTP(w, r)
		return
	}

	ctx := lightning.WithPrice(r.Context(), rt.priceSats)
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
