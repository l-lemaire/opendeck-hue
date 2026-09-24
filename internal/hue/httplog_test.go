package hue

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoggingTransportRedactsKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var out bytes.Buffer
	client := &http.Client{Transport: loggingTransport{
		next: http.DefaultTransport,
		log:  log.New(&out, "", 0),
	}}

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/clip/v2/resource/light/1", strings.NewReader(`{"on":{"on":true}}`))
	req.Header.Set(appKeyHeader, "SUPERSECRETKEY123")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	logged := out.String()
	if strings.Contains(logged, "SUPERSECRETKEY123") {
		t.Errorf("secret leaked into debug output:\n%s", logged)
	}
	for _, want := range []string{"PUT /clip/v2/resource/light/1", `{"on":{"on":true}}`, "SUPE…(redacted)", "200 OK", `{"ok":true}`} {
		if !strings.Contains(logged, want) {
			t.Errorf("debug output missing %q:\n%s", want, logged)
		}
	}
	// The original request must still have carried the real key.
	if got := req.Header.Get(appKeyHeader); got != "SUPERSECRETKEY123" {
		t.Errorf("original request header was modified: %q", got)
	}
}
