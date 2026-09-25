// Package hue talks to a Philips Hue bridge over its local HTTPS API (v2).
//
// The directory name "internal" is special in Go: packages below it can only
// be imported by code inside this module (github.com/l-lemaire/streamdeck).
// That is exactly what we want. Both the CLI (cmd/hue) and the future
// OpenDeck plugin (cmd/opendeck-lights) share this package, but nobody
// outside the repository can depend on it, so we are free to change it.
//
// Planned contents, one file each so every concern is easy to find:
//
//	discover.go   find bridges on the LAN (mDNS, then cloud fallback)
//	pair.go       obtain an application key by pressing the link button
//	tls.go        verify the bridge certificate (Signify CA, CN = bridge id)
//	client.go     the HTTP client: base URL, auth header, debug logging
//	lights.go     list lights and set on/off/brightness
//
// A file named doc.go that only holds the package comment is a common Go
// convention. It gives the package documentation a stable home.
package hue
