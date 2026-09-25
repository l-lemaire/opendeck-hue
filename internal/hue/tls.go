package hue

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// A Hue bridge speaks HTTPS with a certificate that no operating system
// trusts out of the box: it is issued by Signify's private "root-bridge" CA,
// its common name (CN) is the bridge id in upper case, and it carries no
// Subject Alternative Name, so Go's default verification would reject it
// even if we had the CA. The bridge also sends only its own certificate, not
// the CA, and Signify does not publish the CA in a place we could find.
//
// So we verify it ourselves, with two rules:
//
//  1. The CN must equal the bridge id we intend to talk to. The id comes from
//     discovery (mDNS TXT record or cloud) or from the bridge's own config
//     endpoint, and it is what the user confirms by pressing the link button.
//  2. After pairing, the certificate's SHA-256 fingerprint is pinned. Every
//     later connection must present the same certificate, so nothing on the
//     network can impersonate the bridge later. This is "trust on first use",
//     the same model SSH uses for host keys.
//
// Bridge certificates are valid until 2038, so pinning does not expire in
// practice. If a bridge is replaced, `hue auth` pairs again and re-pins.

// certCheck holds the expectations for one bridge and remembers the last
// certificate it saw, so a first contact can learn the fingerprint to pin.
type certCheck struct {
	// ExpectedID is the lower-case bridge id the CN must match.
	// Empty means "any bridge": used only for the probe before pairing when
	// the user gave an IP address and no id.
	ExpectedID string
	// Fingerprint is the pinned "sha256:<hex>" value. Empty on first contact.
	Fingerprint string
	Log         *log.Logger

	// mu protects the fields below: Go's HTTP client may verify several
	// connections concurrently. A sync.Mutex is the basic lock; Lock/Unlock
	// around every access of the shared fields.
	mu       sync.Mutex
	seenCN   string
	seenFP   string
	seenOnce bool
}

// tlsConfig builds the TLS settings that route verification to verify().
func (c *certCheck) tlsConfig() *tls.Config {
	return &tls.Config{
		// This flag disables the standard chain and host-name verification.
		// It is safe here ONLY because VerifyPeerCertificate below performs
		// our own checks on every handshake. Never set it alone.
		InsecureSkipVerify:    true,
		VerifyPeerCertificate: c.verify,
		MinVersion:            tls.VersionTLS12,
	}
}

// verify is called by the TLS handshake with the certificates the bridge
// sent (raw DER bytes, leaf first). Returning an error aborts the connection.
func (c *certCheck) verify(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	if len(rawCerts) == 0 {
		return errors.New("tls: bridge sent no certificate")
	}
	leaf, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return fmt.Errorf("tls: cannot parse bridge certificate: %w", err)
	}
	fp := fingerprint(rawCerts[0])
	cn := strings.ToLower(leaf.Subject.CommonName)

	debugf(c.Log, "tls: bridge certificate CN=%s issuer=%q valid %s to %s %s",
		leaf.Subject.CommonName, leaf.Issuer.CommonName,
		leaf.NotBefore.Format(time.DateOnly), leaf.NotAfter.Format(time.DateOnly), fp)

	c.mu.Lock()
	c.seenCN, c.seenFP, c.seenOnce = cn, fp, true
	c.mu.Unlock()

	now := time.Now()
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return fmt.Errorf("tls: bridge certificate is not valid at the current time (valid %s to %s); check the clock",
			leaf.NotBefore.Format(time.DateOnly), leaf.NotAfter.Format(time.DateOnly))
	}
	if c.ExpectedID != "" && cn != strings.ToLower(c.ExpectedID) {
		return fmt.Errorf("tls: certificate belongs to bridge %s, expected %s", cn, c.ExpectedID)
	}
	if c.Fingerprint != "" && fp != c.Fingerprint {
		return fmt.Errorf("tls: bridge certificate changed (got %s, pinned %s); if the bridge was replaced, run `hue auth` again to trust the new one", fp, c.Fingerprint)
	}
	switch {
	case c.Fingerprint != "":
		debugf(c.Log, "tls: certificate matches pinned fingerprint")
	case c.ExpectedID != "":
		debugf(c.Log, "tls: first contact, CN matches bridge id; fingerprint not pinned yet")
	default:
		debugf(c.Log, "tls: probe mode, certificate accepted without id check")
	}
	return nil
}

// seen returns the CN and fingerprint of the last certificate verified.
func (c *certCheck) seen() (cn, fp string, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seenCN, c.seenFP, c.seenOnce
}

// fingerprint renders the SHA-256 of a DER certificate as "sha256:<hex>".
func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return "sha256:" + hex.EncodeToString(sum[:])
}
