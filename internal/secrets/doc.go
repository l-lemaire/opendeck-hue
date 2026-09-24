// Package secrets stores credentials such as the Hue application key.
//
// It exposes a small Store interface with Get, Set and Delete, and two
// implementations:
//
//	keyring.go   the desktop keyring via the Secret Service D-Bus API
//	             (GNOME Keyring, KWallet). The default when reachable.
//	file.go      a JSON file with 0600 permissions under $XDG_CONFIG_HOME.
//	             A fallback for headless machines, chosen explicitly.
//
// Non-secret configuration (bridge id, bridge address) does not belong here;
// it lives in a plain config file next to the credentials file.
package secrets
