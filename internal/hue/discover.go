package hue

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// Bridge is a Hue bridge found on the network, before any pairing.
type Bridge struct {
	// ID is the 16-hex-character bridge id, e.g. "ecb5fafffe1a2b3c".
	// It is also the common name of the bridge's TLS certificate and the
	// key under which credentials are stored, so it is the stable identity.
	ID string `json:"id"`
	// Host is the IP address to connect to.
	Host string `json:"host"`
	// Port is the HTTPS port, 443 unless something exotic is going on.
	Port int `json:"port"`
	// Name is the mDNS instance name, e.g. "Philips Hue - 1A2B3C".
	// Empty when the bridge was found through the cloud service.
	Name string `json:"name,omitempty"`
	// Model is the hardware model, e.g. "BSB002" (square v2 bridge).
	Model string `json:"model,omitempty"`
	// Source records which mechanism found the bridge: "mdns" or "cloud".
	Source string `json:"source"`
}

// The `json:"..."` strings above are struct tags. The encoding/json package
// reads them to decide the key names when (de)serialising; "omitempty" drops
// the field when it holds its zero value.

// Addr returns "host:port" ready for a URL or a dialer.
func (b Bridge) Addr() string {
	return net.JoinHostPort(b.Host, strconv.Itoa(b.Port))
}

// CloudDiscoveryURL is Signify's discovery service. The bridge periodically
// reports its LAN address there, so a request from the same public IP gets a
// list of bridges on this network. It is strictly rate limited (about one
// request every 15 minutes per IP), which is why it is only a fallback.
const CloudDiscoveryURL = "https://discovery.meethue.com/"

// Discovery finds bridges. The zero value is usable: Discovery{}.Discover(ctx).
// Fields are exported (capitalised) so callers can tune behaviour; every one
// of them has a sensible default when left empty.
type Discovery struct {
	// Log receives debug output. nil disables it.
	Log *log.Logger
	// MDNSTimeout bounds the mDNS listen window. Default DefaultMDNSTimeout.
	MDNSTimeout time.Duration
	// Interface is the network interface name to query on (e.g. "eno1").
	// Empty lets the kernel choose, which is right for most machines.
	Interface string
	// CloudURL overrides CloudDiscoveryURL, mainly for tests.
	CloudURL string
	// HTTPClient is used for the cloud request. Default: a client with a
	// 10 s timeout whose requests are logged to Log.
	HTTPClient *http.Client
}

// Discover tries mDNS first and falls back to the cloud service only when
// mDNS finds nothing. The result may be empty with a nil error when both
// mechanisms worked but know of no bridge.
func (d Discovery) Discover(ctx context.Context) ([]Bridge, error) {
	bridges, mdnsErr := d.MDNS(ctx)
	if mdnsErr != nil {
		debugf(d.Log, "mdns: failed: %v", mdnsErr)
	}
	if len(bridges) > 0 {
		return bridges, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	debugf(d.Log, "discover: nothing via mdns, trying cloud discovery")
	bridges, cloudErr := d.Cloud(ctx)
	if cloudErr != nil {
		if mdnsErr != nil {
			return nil, fmt.Errorf("mdns: %v; cloud: %w", mdnsErr, cloudErr)
		}
		return nil, fmt.Errorf("mdns found nothing; cloud: %w", cloudErr)
	}
	return bridges, nil
}

// Cloud asks Signify's discovery service for bridges seen from this public IP.
func (d Discovery) Cloud(ctx context.Context) ([]Bridge, error) {
	url := d.CloudURL
	if url == "" {
		url = CloudDiscoveryURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("cloud discovery rate limited (HTTP 429); wait ~15 minutes or pass the bridge address manually")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cloud discovery returned HTTP %d", resp.StatusCode)
	}

	// The response is a JSON array like:
	// [{"id":"ecb5fafffe1a2b3c","internalipaddress":"192.168.1.42","port":443}]
	// An anonymous struct type declared inline keeps the wire format local to
	// the one place that reads it.
	var entries []struct {
		ID   string `json:"id"`
		IP   string `json:"internalipaddress"`
		Port int    `json:"port"`
	}
	// LimitReader caps how much we are willing to read from a remote server.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&entries); err != nil {
		return nil, fmt.Errorf("cloud discovery: bad JSON: %w", err)
	}

	bridges := make([]Bridge, 0, len(entries))
	for _, e := range entries {
		port := e.Port
		if port == 0 {
			port = 443
		}
		bridges = append(bridges, Bridge{
			ID:     e.ID,
			Host:   e.IP,
			Port:   port,
			Source: "cloud",
		})
	}
	sortBridges(bridges)
	return bridges, nil
}

// httpClient returns the configured client or builds the default one.
func (d Discovery) httpClient() *http.Client {
	if d.HTTPClient != nil {
		return d.HTTPClient
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: loggingTransport{next: http.DefaultTransport, log: d.Log},
	}
}

// sortBridges orders by id so output is stable between runs.
func sortBridges(bridges []Bridge) {
	sort.Slice(bridges, func(i, j int) bool { return bridges[i].ID < bridges[j].ID })
}

// itoa is a short alias for strconv.Itoa used in debug strings.
func itoa(n int) string { return strconv.Itoa(n) }
