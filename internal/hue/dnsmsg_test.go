package hue

// Go tests live next to the code in files ending in _test.go and are run by
// `go test`. Each test is a function named TestXxx taking *testing.T.
// Failures are reported with t.Errorf (continue) or t.Fatalf (stop this test).

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// The helpers below build DNS messages by hand so tests do not depend on the
// code under test to produce their input.

// appendRecord appends one resource record with the given raw RDATA.
func appendRecord(t *testing.T, buf []byte, name string, typ uint16, rdata []byte) []byte {
	t.Helper() // failures are reported at the caller's line, not here
	buf, err := appendName(buf, name)
	if err != nil {
		t.Fatal(err)
	}
	buf = binary.BigEndian.AppendUint16(buf, typ)
	buf = binary.BigEndian.AppendUint16(buf, dnsClassIN|0x8000) // cache-flush bit set, like real mDNS
	buf = binary.BigEndian.AppendUint32(buf, 120)               // TTL
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(rdata)))
	return append(buf, rdata...)
}

// txtRData encodes strings as TXT RDATA.
func txtRData(items ...string) []byte {
	var out []byte
	for _, s := range items {
		out = append(out, byte(len(s)))
		out = append(out, s...)
	}
	return out
}

// header builds a 12-byte header with the given record count in ANCOUNT.
func header(answers int) []byte {
	h := make([]byte, dnsHeaderLen)
	binary.BigEndian.PutUint16(h[2:4], 0x8400) // response, authoritative
	binary.BigEndian.PutUint16(h[6:8], uint16(answers))
	return h
}

// fakeBridgeAnswer builds the kind of answer a real bridge sends, including
// a compression pointer in the SRV target so that path is exercised.
func fakeBridgeAnswer(t *testing.T) []byte {
	t.Helper()
	msg := header(4)

	// PTR _hue._tcp.local. -> Philips Hue - 1A2B3C._hue._tcp.local.
	ptrTarget, _ := appendName(nil, "Philips Hue - 1A2B3C._hue._tcp.local.")
	msg = appendRecord(t, msg, hueServiceName, dnsTypePTR, ptrTarget)

	// A ecb5fafffe1a2b3c.local. -> 192.168.1.42 ; remember where the name starts
	hostNameOffset := len(msg)
	msg = appendRecord(t, msg, "ecb5fafffe1a2b3c.local.", dnsTypeA, []byte{192, 168, 1, 42})

	// SRV instance -> port 443, target = pointer to the A record's name
	srv := []byte{0, 0, 0, 0, 0x01, 0xBB} // priority 0, weight 0, port 443
	srv = binary.BigEndian.AppendUint16(srv, 0xC000|uint16(hostNameOffset))
	msg = appendRecord(t, msg, "Philips Hue - 1A2B3C._hue._tcp.local.", dnsTypeSRV, srv)

	// TXT instance -> bridgeid, modelid (bridges send the id in upper case)
	msg = appendRecord(t, msg, "Philips Hue - 1A2B3C._hue._tcp.local.", dnsTypeTXT,
		txtRData("bridgeid=ECB5FAFFFE1A2B3C", "modelid=BSB002"))
	return msg
}

func TestBuildQuery(t *testing.T) {
	got, err := buildQuery("_hue._tcp.local.", dnsTypePTR)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, // header, QDCOUNT=1
		4, '_', 'h', 'u', 'e',
		4, '_', 't', 'c', 'p',
		5, 'l', 'o', 'c', 'a', 'l',
		0,
		0, 12, // PTR
		0, 1, // IN
	}
	if !bytes.Equal(got, want) {
		t.Errorf("query bytes\n got % x\nwant % x", got, want)
	}
}

func TestParseMessage(t *testing.T) {
	records, err := parseMessage(fakeBridgeAnswer(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 {
		t.Fatalf("got %d records, want 4", len(records))
	}

	if r := records[0]; r.Type != dnsTypePTR || r.PTR != "Philips Hue - 1A2B3C._hue._tcp.local." {
		t.Errorf("PTR record = %+v", r)
	}
	if r := records[1]; r.Type != dnsTypeA || r.A.String() != "192.168.1.42" {
		t.Errorf("A record = %+v", r)
	}
	if r := records[2]; r.Type != dnsTypeSRV || r.SRV.Port != 443 || r.SRV.Target != "ecb5fafffe1a2b3c.local." {
		t.Errorf("SRV record = %+v (compression pointer not followed?)", r)
	}
	if r := records[3]; r.Type != dnsTypeTXT || len(r.TXT) != 2 || r.TXT[0] != "bridgeid=ECB5FAFFFE1A2B3C" {
		t.Errorf("TXT record = %+v", r)
	}
}

func TestParseMessageRejectsGarbage(t *testing.T) {
	// A table-driven test: one loop over cases is the idiomatic Go way to
	// cover many inputs without repeating the assertion code.
	cases := map[string][]byte{
		"empty":           {},
		"short header":    {0, 0, 0},
		"count but no rr": header(1),
		"pointer loop":    append(header(1), 0xC0, 12, 0, 1, 0, 1, 0, 0, 0, 0, 0, 0),
	}
	for name, msg := range cases {
		if _, err := parseMessage(msg); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestBridgeFromRecords(t *testing.T) {
	records, err := parseMessage(fakeBridgeAnswer(t))
	if err != nil {
		t.Fatal(err)
	}
	b, ok := bridgeFromRecords(records, nil)
	if !ok {
		t.Fatal("no bridge extracted")
	}
	want := Bridge{
		ID: "ecb5fafffe1a2b3c", Host: "192.168.1.42", Port: 443,
		Name: "Philips Hue - 1A2B3C", Model: "BSB002", Source: "mdns",
	}
	if b != want {
		t.Errorf("\n got %+v\nwant %+v", b, want)
	}
}
