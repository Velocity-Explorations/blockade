package payment

import "net/http"

// PaymentVerifier is the single seam between the proxy and any payment backend.
// Swap the implementation to change from Lightning to PoW, on-chain, or hybrid
// without touching the proxy core.
type PaymentVerifier interface {
	// IssueChallenge writes a 402 Payment Required response containing whatever
	// challenge or invoice the backend requires. The request is not forwarded.
	IssueChallenge(w http.ResponseWriter, r *http.Request) error

	// VerifyProof validates the credential presented in an Authorization: L402
	// header value (everything after the scheme prefix). Returns true if the
	// proof is valid and the request should be forwarded upstream.
	VerifyProof(token string) (bool, error)
}
