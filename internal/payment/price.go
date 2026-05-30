package payment

import "context"

type priceSatsKey struct{}

// WithPrice returns a copy of ctx carrying the per-request price in satoshis.
// Called by the proxy handler before invoking IssueChallenge.
func WithPrice(ctx context.Context, sats int64) context.Context {
	return context.WithValue(ctx, priceSatsKey{}, sats)
}

// PriceFromContext extracts the satoshi price set by WithPrice.
func PriceFromContext(ctx context.Context) (int64, bool) {
	v, ok := ctx.Value(priceSatsKey{}).(int64)
	return v, ok
}
