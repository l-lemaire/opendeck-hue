package hue

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Client talks to one bridge. Build it with NewClient; the zero value is not
// usable because the TLS verifier must be wired into the HTTP transport.
type Client struct {
	addr   string // host:port
	appKey string
	log    *log.Logger
	check  *certCheck
	http   *http.Client
}

// ClientOptions is everything NewClient needs. Only Addr is mandatory.
type ClientOptions struct {
	// Addr is "host:port", e.g. "10.0.10.57:443".
	Addr string
	// ID is the lower-case bridge id the certificate must match. Leave empty
	// only for the pre-pairing probe when the id is not known yet.
	ID string
	// Fingerprint is the pinned certificate fingerprint from a previous
	// pairing, or empty on first contact.
	Fingerprint string
	// AppKey authenticates requests. Empty for discovery/pairing calls.
	AppKey string
	// Log receives debug output (TLS decisions, full HTTP dumps). nil = silent.
	Log *log.Logger
	// Timeout caps one whole request. Default 15 s.
	Timeout time.Duration
}

// NewClient wires the certificate check and the debug logger into an HTTP
// client. Returning a pointer (*Client) is the norm for a type that owns
// resources and state.
func NewClient(o ClientOptions) *Client {
	check := &certCheck{ExpectedID: strings.ToLower(o.ID), Fingerprint: o.Fingerprint, Log: o.Log}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	// http.Transport is the component that opens connections. We copy the
	// defaults and swap in our TLS configuration, then wrap the transport in
	// the debug logger so every request goes through it.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = check.tlsConfig()

	return &Client{
		addr:   o.Addr,
		appKey: o.AppKey,
		log:    o.Log,
		check:  check,
		http: &http.Client{
			Timeout:   timeout,
			Transport: loggingTransport{next: transport, log: o.Log},
		},
	}
}

// Addr returns the host:port this client talks to.
func (c *Client) Addr() string { return c.addr }

// SeenCertificate reports the CN (lower case) and fingerprint of the last
// bridge certificate this client verified. ok is false before any request.
func (c *Client) SeenCertificate() (cn, fingerprint string, ok bool) {
	return c.check.seen()
}

// do performs one request and returns the status code and body. It is the
// only place that builds URLs and sets headers; higher-level methods decode
// the body according to the endpoint they called.
//
// body may be nil, or any value that encoding/json can marshal.
func (c *Client) do(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("encode request body: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, "https://"+c.addr+path, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.appKey != "" {
		req.Header.Set(appKeyHeader, c.appKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	// Bridge responses are small; 4 MiB is a generous ceiling.
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("read response body: %w", err)
	}
	return resp.StatusCode, data, nil
}

// BridgeInfo is what the bridge tells anyone who asks, no key needed.
type BridgeInfo struct {
	ID         string `json:"bridgeid"`
	Name       string `json:"name"`
	Model      string `json:"modelid"`
	APIVersion string `json:"apiversion"`
	SWVersion  string `json:"swversion"`
	MAC        string `json:"mac"`
}

// Info fetches the public configuration. Because it needs no application
// key, it is the way to learn (or confirm) a bridge's id before pairing.
func (c *Client) Info(ctx context.Context) (BridgeInfo, error) {
	status, body, err := c.do(ctx, http.MethodGet, "/api/0/config", nil)
	if err != nil {
		return BridgeInfo{}, err
	}
	if status != http.StatusOK {
		return BridgeInfo{}, fmt.Errorf("GET /api/0/config: HTTP %d: %s", status, trim(body))
	}
	var info BridgeInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return BridgeInfo{}, fmt.Errorf("GET /api/0/config: bad JSON: %w", err)
	}
	info.ID = strings.ToLower(info.ID)
	return info, nil
}

// CheckKey verifies that the client's application key is accepted, by
// requesting the bridge resource on the v2 API.
func (c *Client) CheckKey(ctx context.Context) error {
	status, body, err := c.do(ctx, http.MethodGet, "/clip/v2/resource/bridge", nil)
	if err != nil {
		return err
	}
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("application key rejected by the bridge (HTTP %d)", status)
	default:
		return fmt.Errorf("GET /clip/v2/resource/bridge: HTTP %d: %s", status, trim(body))
	}
}

// trim shortens a body for inclusion in an error message.
func trim(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
