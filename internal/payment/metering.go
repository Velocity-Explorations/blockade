package payment

import "net/http"

// CredentialIssuer extends PaymentVerifier with enrollment and credential
// management for the staked-credential cost curve. A PaymentVerifier that
// also implements CredentialIssuer enables v2 metering in the proxy.
// Backends that do not support metering (e.g. on-chain) ignore this interface.
type CredentialIssuer interface {
	PaymentVerifier

	// IssueEnrollmentChallenge writes a 402 response whose challenge demands
	// the enrollment stake. The type="enrollment" signal in WWW-Authenticate
	// tells the client this payment mints a credential, not a per-request toll.
	// Returns the payment hash hex so the proxy can track pending enrollments.
	IssueEnrollmentChallenge(w http.ResponseWriter, r *http.Request) (paymentHashHex string, err error)

	// CompleteEnrollment verifies an enrollment payment token and mints a
	// long-lived credential macaroon carrying a fresh principal identifier.
	CompleteEnrollment(tokenStr string) (principalID string, credentialB64 string, err error)

	// ValidateCredential checks a credential macaroon's signature and returns
	// the principal identifier embedded in it.
	ValidateCredential(credentialB64 string) (principalID string, err error)

	// PaymentHashFromToken extracts the hex-encoded payment hash from a raw
	// L402 token string without performing full verification. Used by the proxy
	// to check whether a token corresponds to a pending enrollment.
	PaymentHashFromToken(tokenStr string) (string, error)

	// LookupSettlement checks whether the invoice identified by paymentHashHex
	// has been settled. If settled, returns the hex-encoded preimage.
	LookupSettlement(paymentHashHex string) (settled bool, preimageHex string, err error)
}
