package payment

// CurvePrice computes the per-request price for the Nth request under a
// staked credential. price(N) = floor + slope * (N - 1), so cumulative cost
// grows quadratically with volume.
func CurvePrice(n, floor, slope int64) int64 {
	if n <= 1 {
		return floor
	}
	return floor + slope*(n-1)
}
