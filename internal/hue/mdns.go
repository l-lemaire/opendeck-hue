package hue

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// Hue bridges advertise themselves with multicast DNS (mDNS, RFC 6762) under
// the service type "_hue._tcp.local.". Any host on the LAN can ask "who offers
// _hue._tcp?" by sending a DNS question to the well-known multicast group
// 224.0.0.251 on UDP port 5353, and each bridge answers with:
//
//	PTR  _hue._tcp.local.                  -> "Philips Hue - 1A2B3C._hue._tcp.local."
//	SRV  Philips Hue - 1A2B3C._hue._tcp.  -> target ecb5fafffe1a2b3c.local., port 443
//	TXT  Philips Hue - 1A2B3C._hue._tcp.  -> bridgeid=ecb5fafffe1a2b3c, modelid=BSB002
//	A    ecb5fafffe1a2b3c.local.           -> 192.168.1.42
//
// How we listen matters. The simplest approach, sending the question from a
// random UDP port and waiting for a direct ("legacy unicast") reply, does not
// work on a typical Fedora desktop: firewalld allows mDNS traffic only on
// port 5353, and a reply to a multicast query does not match the outgoing
// packet in the firewall's connection tracking, so it is silently dropped.
//
// Instead we join the multicast group on port 5353 ourselves, with the
// address-reuse socket options that let us share the port with Avahi,
// Spotify, Chrome and every other mDNS user on the machine. Answers to a
// normal multicast question are multicast back to the group, so all of them
// reach us. The cost is that we also hear every other mDNS packet on the LAN,
// so the read loop filters aggressively.

const hueServiceName = "_hue._tcp.local."

// mdnsGroup is the IPv4 mDNS multicast address and port. IPv6 (ff02::fb) is
// left out: Hue bridges advertise on IPv4 and one socket keeps things simple.
var mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// The two variables below exist so tests can substitute a plain socket and a
// fake responder on localhost without multicast. Production code never
// changes them.
var (
	mdnsDestination = mdnsGroup
	mdnsListen      = listenMulticast
)

// listenMulticast opens the shared mDNS socket. `ifi` nil means "let the
// kernel pick the interface from the routing table", which is right on a
// machine with one network card. Machines with VPNs or container bridges may
// need Discovery.Interface.
func listenMulticast(ifi *net.Interface) (*net.UDPConn, error) {
	// ListenMulticastUDP sets SO_REUSEADDR (and SO_REUSEPORT on Linux),
	// binds port 5353 and joins the group on ifi.
	return net.ListenMulticastUDP("udp4", ifi, mdnsGroup)
}

// DefaultMDNSTimeout is how long MDNS waits for answers when the caller does
// not set Discovery.MDNSTimeout. Bridges answer within a few hundred ms.
const DefaultMDNSTimeout = 2 * time.Second

// MDNS looks for bridges on the local network and returns every distinct
// bridge that answered before the timeout. An empty result with a nil error
// means the query worked but nobody answered.
//
// `ctx context.Context` is the standard Go way to carry cancellation and
// deadlines through a call chain. If the caller cancels ctx we stop early.
func (d Discovery) MDNS(ctx context.Context) ([]Bridge, error) {
	timeout := d.MDNSTimeout
	if timeout <= 0 {
		timeout = DefaultMDNSTimeout
	}

	var ifi *net.Interface
	if d.Interface != "" {
		var err error
		if ifi, err = net.InterfaceByName(d.Interface); err != nil {
			return nil, fmt.Errorf("mdns: interface %q: %w", d.Interface, err)
		}
	}
	conn, err := mdnsListen(ifi)
	if err != nil {
		return nil, fmt.Errorf("mdns: open socket: %w", err)
	}
	// defer runs the call when this function returns, whichever path it
	// takes. It is Go's equivalent of try/finally for cleanup.
	defer conn.Close()

	query, err := buildQuery(hueServiceName, dnsTypePTR)
	if err != nil {
		return nil, err
	}
	if _, err := conn.WriteToUDP(query, mdnsDestination); err != nil {
		return nil, fmt.Errorf("mdns: send query: %w", err)
	}
	debugf(d.Log, "mdns: sent %d-byte PTR query for %s to %s from %s (interface: %s)",
		len(query), hueServiceName, mdnsDestination, conn.LocalAddr(), interfaceName(ifi))

	// Stop reading at the timeout, or earlier if the context's own deadline
	// is sooner or it gets cancelled. A read deadline makes the blocking
	// ReadFromUDP below return with a timeout error.
	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	// context.AfterFunc runs the function when ctx is cancelled. Moving the
	// deadline to "now" unblocks the pending read. The returned stop function
	// deregisters it; deferring stop avoids a leak on the normal path.
	stop := context.AfterFunc(ctx, func() { conn.SetReadDeadline(time.Now()) })
	defer stop()

	// A map keyed by bridge id de-duplicates repeated answers from one bridge.
	found := make(map[string]Bridge)
	buf := make([]byte, 9000) // larger than any sane mDNS packet
	ignored := 0              // unrelated mDNS packets, counted for the debug summary

	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			// The deadline passing is the normal way out of this loop.
			if errors.Is(err, os.ErrDeadlineExceeded) {
				break
			}
			return nil, err
		}
		packet := buf[:n]

		// On the shared port we see other clients' questions too. Skip them
		// (and anything too short to have a header).
		if !isResponse(packet) {
			ignored++
			continue
		}
		records, err := parseMessage(packet)
		if err != nil {
			debugf(d.Log, "mdns: ignoring %d-byte packet from %s: %v", n, from, err)
			continue
		}
		if !mentionsHue(records) {
			ignored++
			continue
		}

		debugf(d.Log, "mdns: %d-byte answer from %s with %d records", n, from, len(records))
		for _, r := range records {
			debugf(d.Log, "mdns:   %s", describeRecord(r))
		}
		bridge, ok := bridgeFromRecords(records, from.IP)
		if !ok {
			debugf(d.Log, "mdns: answer from %s has no bridgeid, skipped", from)
			continue
		}
		found[bridge.ID] = bridge
	}
	debugf(d.Log, "mdns: done, %d bridge(s), %d unrelated mDNS packet(s) ignored", len(found), ignored)

	// If the caller cancelled us, report that rather than a partial result.
	if ctx.Err() != nil && len(found) == 0 {
		return nil, ctx.Err()
	}

	bridges := make([]Bridge, 0, len(found))
	for _, b := range found {
		bridges = append(bridges, b)
	}
	sortBridges(bridges)
	return bridges, nil
}

// isResponse reports whether the QR bit (top bit of the flags field) is set.
func isResponse(msg []byte) bool {
	return len(msg) >= dnsHeaderLen && msg[2]&0x80 != 0
}

// mentionsHue reports whether any record belongs to the Hue service type.
func mentionsHue(records []dnsRecord) bool {
	for _, r := range records {
		if strings.HasSuffix(r.Name, hueServiceName) || strings.HasSuffix(r.PTR, hueServiceName) {
			return true
		}
	}
	return false
}

// bridgeFromRecords assembles one Bridge from the records of a single mDNS
// answer. The bridge id comes from the TXT record and is mandatory; the other
// fields have fallbacks (the packet's source address, port 443).
func bridgeFromRecords(records []dnsRecord, from net.IP) (Bridge, bool) {
	b := Bridge{Source: "mdns"}
	for _, r := range records {
		switch r.Type {
		case dnsTypeTXT:
			for _, kv := range r.TXT {
				// strings.Cut splits at the first "=": key, value, found.
				key, value, ok := strings.Cut(kv, "=")
				if !ok {
					continue
				}
				switch key {
				case "bridgeid":
					b.ID = strings.ToLower(value)
				case "modelid":
					b.Model = value
				}
			}
			b.Name = instanceName(r.Name)
		case dnsTypeSRV:
			b.Port = int(r.SRV.Port)
			b.Name = instanceName(r.Name)
		case dnsTypeA:
			if b.Host == "" {
				b.Host = r.A.String()
			}
		}
	}
	if b.ID == "" {
		return Bridge{}, false
	}
	if b.Host == "" && from != nil {
		b.Host = from.String()
	}
	if b.Port == 0 {
		b.Port = 443
	}
	return b, true
}

// instanceName turns "Philips Hue - 1A2B3C._hue._tcp.local." into
// "Philips Hue - 1A2B3C".
func instanceName(fullName string) string {
	return strings.TrimSuffix(fullName, "."+hueServiceName)
}

func interfaceName(ifi *net.Interface) string {
	if ifi == nil {
		return "system default"
	}
	return ifi.Name
}

// describeRecord renders a record on one line for the debug output.
func describeRecord(r dnsRecord) string {
	switch r.Type {
	case dnsTypeA:
		return "A    " + r.Name + " -> " + r.A.String()
	case dnsTypePTR:
		return "PTR  " + r.Name + " -> " + r.PTR
	case dnsTypeSRV:
		return "SRV  " + r.Name + " -> " + net.JoinHostPort(r.SRV.Target, itoa(int(r.SRV.Port)))
	case dnsTypeTXT:
		return "TXT  " + r.Name + " -> " + strings.Join(r.TXT, " ")
	case dnsTypeAAAA:
		return "AAAA " + r.Name + " (IPv6, not decoded)"
	case dnsTypeNSEC:
		return "NSEC " + r.Name
	default:
		return "type " + itoa(int(r.Type)) + " " + r.Name
	}
}
