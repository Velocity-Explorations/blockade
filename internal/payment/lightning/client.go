package lightning

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/lightningnetwork/lnd/lnrpc"
)

// Client wraps the lnd gRPC connection and exposes only what the paywall needs.
type Client struct {
	lnClient lnrpc.LightningClient
	conn     *grpc.ClientConn
}

// NewClient dials lnd at host using TLS (cert from tlsCertPath) and macaroon
// auth (from macaroonPath). Call Close when done.
func NewClient(host, tlsCertPath, macaroonPath string) (*Client, error) {
	certBytes, err := os.ReadFile(tlsCertPath)
	if err != nil {
		return nil, fmt.Errorf("read tls cert: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certBytes) {
		return nil, fmt.Errorf("parse tls cert: no valid PEM block found")
	}
	tlsCreds := credentials.NewTLS(&tls.Config{RootCAs: pool})

	macaroonBytes, err := os.ReadFile(macaroonPath)
	if err != nil {
		return nil, fmt.Errorf("read macaroon: %w", err)
	}
	macCreds := macaroonCredential(hex.EncodeToString(macaroonBytes))

	conn, err := grpc.NewClient(
		host,
		grpc.WithTransportCredentials(tlsCreds),
		grpc.WithPerRPCCredentials(macCreds),
	)
	if err != nil {
		return nil, fmt.Errorf("dial lnd: %w", err)
	}

	return &Client{
		lnClient: lnrpc.NewLightningClient(conn),
		conn:     conn,
	}, nil
}

// AddInvoice creates a new Lightning invoice for amountSats satoshis.
// Returns the BOLT11 payment request and the 32-byte payment hash.
func (c *Client) AddInvoice(ctx context.Context, amountSats int64, memo string) (payReq string, paymentHash []byte, err error) {
	resp, err := c.lnClient.AddInvoice(ctx, &lnrpc.Invoice{
		Value: amountSats,
		Memo:  memo,
	})
	if err != nil {
		return "", nil, fmt.Errorf("AddInvoice: %w", err)
	}
	return resp.PaymentRequest, resp.RHash, nil
}

// IsSettled returns true if the invoice identified by paymentHash has been paid.
func (c *Client) IsSettled(ctx context.Context, paymentHash []byte) (bool, error) {
	resp, err := c.lnClient.LookupInvoice(ctx, &lnrpc.PaymentHash{
		RHash: paymentHash,
	})
	if err != nil {
		return false, fmt.Errorf("LookupInvoice: %w", err)
	}
	return resp.State == lnrpc.Invoice_SETTLED, nil
}

// LookupSettlement checks whether an invoice is settled and returns the
// preimage if so. Used by the settlement-status endpoint.
func (c *Client) LookupSettlement(ctx context.Context, paymentHash []byte) (settled bool, preimage []byte, err error) {
	resp, err := c.lnClient.LookupInvoice(ctx, &lnrpc.PaymentHash{
		RHash: paymentHash,
	})
	if err != nil {
		return false, nil, fmt.Errorf("LookupInvoice: %w", err)
	}
	if resp.State != lnrpc.Invoice_SETTLED {
		return false, nil, nil
	}
	return true, resp.RPreimage, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

// macaroonCredential implements grpc.PerRPCCredentials using an lnd macaroon.
type macaroonCredential string

func (m macaroonCredential) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{"macaroon": string(m)}, nil
}

func (m macaroonCredential) RequireTransportSecurity() bool { return true }
