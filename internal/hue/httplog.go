package hue

import (
	"log"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"
)

// appKeyHeader carries the Hue application key on every authenticated
// request. It is the secret we must never print in full.
const appKeyHeader = "hue-application-key"

// loggingTransport is the single place where debug output for HTTP lives.
//
// In Go, an http.Client sends requests through a RoundTripper: an interface
// with one method, RoundTrip(*Request) (*Response, error). Wrapping the real
// transport in our own type lets us see every request and response without
// touching any call site, and guarantees no request can bypass the logging.
type loggingTransport struct {
	next http.RoundTripper // the real transport that talks to the network
	log  *log.Logger       // nil means silent
}

// RoundTrip implements http.RoundTripper.
func (t loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.log == nil {
		return t.next.RoundTrip(req)
	}

	// DumpRequestOut renders the request as it will appear on the wire. With
	// body=true it reads req.Body and replaces it with an in-memory copy so
	// the request can still be sent. We dump a shallow clone with the secret
	// header redacted, then hand the refilled body back to the original.
	clone := req.Clone(req.Context())
	redactHeader(clone.Header, appKeyHeader)
	dump, err := httputil.DumpRequestOut(clone, true)
	if err != nil {
		t.log.Printf("http: could not dump request: %v", err)
	} else {
		t.log.Printf("http: request\n%s", indent(dump))
	}
	req.Body = clone.Body

	start := time.Now()
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		t.log.Printf("http: %s %s failed after %s: %v", req.Method, req.URL, time.Since(start).Round(time.Millisecond), err)
		return nil, err
	}

	// DumpResponse also reads and refills the body. Not for an event
	// stream, whose body never ends: dump its headers only.
	withBody := resp.Header.Get("Content-Type") != "text/event-stream" && req.Header.Get("Accept") != "text/event-stream"
	dump, err = httputil.DumpResponse(resp, withBody)
	if err != nil {
		t.log.Printf("http: could not dump response: %v", err)
	} else {
		t.log.Printf("http: response after %s\n%s", time.Since(start).Round(time.Millisecond), indent(dump))
	}
	return resp, nil
}

// redactHeader keeps the first four characters of a header value so two
// keys can still be told apart in a log, and hides the rest.
func redactHeader(h http.Header, name string) {
	for i, v := range h.Values(name) {
		if len(v) > 4 {
			v = v[:4] + "…(redacted)"
		} else if v != "" {
			v = "…(redacted)"
		}
		h[http.CanonicalHeaderKey(name)][i] = v
	}
}

// indent prefixes every line so multi-line dumps stand out from the rest of
// the debug output.
func indent(b []byte) string {
	s := strings.TrimRight(string(b), "\r\n")
	return "    " + strings.ReplaceAll(s, "\n", "\n    ")
}
