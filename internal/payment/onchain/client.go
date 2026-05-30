package onchain

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
)

// Client wraps the bitcoind JSON-RPC interface for the calls the paywall needs.
// All wallet operations target the named wallet "paywall" (created by setup-onchain.sh).
type Client struct {
	endpoint string
	authHdr  string
	http     *http.Client
}

// NewClient creates a Client that talks to bitcoind at host (e.g. "bitcoind:18443")
// using the given RPC credentials.
func NewClient(host, user, pass string) *Client {
	return &Client{
		endpoint: "http://" + host + "/wallet/paywall",
		authHdr:  "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass)),
		http:     &http.Client{},
	}
}

type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      string        `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) call(ctx context.Context, method string, params []interface{}, result interface{}) error {
	body, err := json.Marshal(rpcRequest{JSONRPC: "1.1", ID: "btc-paywall", Method: method, Params: params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.authHdr)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("rpc %s: %w", method, err)
	}
	defer resp.Body.Close()

	var rpcResp rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return fmt.Errorf("rpc %s: decode response: %w", method, err)
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("rpc %s: code %d: %s", method, rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return json.Unmarshal(rpcResp.Result, result)
}

// GetNewAddress generates a new bech32 (P2WPKH) address in the paywall wallet.
func (c *Client) GetNewAddress(ctx context.Context) (string, error) {
	var addr string
	if err := c.call(ctx, "getnewaddress", []interface{}{"", "bech32"}, &addr); err != nil {
		return "", fmt.Errorf("getnewaddress: %w", err)
	}
	return addr, nil
}

// GetReceivedSats returns the total satoshis received by addr with at least
// minConf confirmations. Pass minConf=0 to include mempool (unconfirmed) payments.
func (c *Client) GetReceivedSats(ctx context.Context, addr string, minConf int) (int64, error) {
	var btcAmount float64
	if err := c.call(ctx, "getreceivedbyaddress", []interface{}{addr, minConf}, &btcAmount); err != nil {
		return 0, fmt.Errorf("getreceivedbyaddress: %w", err)
	}
	return int64(math.Round(btcAmount * 1e8)), nil
}
