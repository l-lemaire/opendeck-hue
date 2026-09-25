# streamdeck

Control Philips Hue (and later Nanoleaf) lights from an OpenDeck plugin.

The first milestone is `hue`, a command-line tool to pair with a bridge, list
lights and toggle them. The plugin will reuse the same Go packages.

## Layout

```
cmd/hue/            the CLI executable
internal/hue/       Hue bridge client (discovery, TLS, pairing, lights)
internal/secrets/   credential storage (desktop keyring, file fallback)
internal/config/    non-secret state: known bridges, certificate pins
Makefile            build, test, cross-compile for OpenDeck target triples
```

## Requirements

Go 1.26 or newer for development. End users need nothing: the build produces
a static binary.

## Usage

```
make build              # -> bin/hue
make check              # gofmt, go vet, tests
make cross              # one binary per OS/arch under dist/

bin/hue discover                 # find bridges (mDNS, then cloud fallback)
bin/hue discover --json
bin/hue auth                     # pair: press the bridge's link button when asked
bin/hue auth status              # list paired bridges and check their keys
bin/hue auth forget              # delete a bridge's key and configuration
bin/hue list lights              # also: list rooms, list zones
bin/hue toggle light kitchen     # name (case-insensitive, unique prefix ok) or id
bin/hue on light --brightness 40 kitchen
bin/hue off room salon
bin/hue toggle zone "zone ordi"
bin/hue toggle light --dry-run kitchen    # print the request, send nothing
bin/hue --debug discover         # show every packet and HTTP exchange
```

Lights, rooms and zones are separate namespaces; a name is only matched
within the kind you name. Command flags go before the entity name. Writes use the CLIP v2 API;
only the pairing calls still use the original v1 endpoints, which have no v2
equivalent.

Global flags such as `--debug` go before the command name.

### Debug output

`--debug` prints to stderr: the mDNS query and every record in each answer,
whether the cloud fallback ran, and a full dump of every HTTP request and
response. The Hue application key header is redacted in those dumps.

### mDNS and firewalls

The bridge is found by joining the mDNS multicast group on UDP port 5353,
sharing the port with Avahi and other clients. This is deliberate: Fedora's
firewalld only lets mDNS traffic in on port 5353, so a reply to a query sent
from a random port would be dropped.

## Security notes

**Credentials.** The Hue application key is a secret. `hue auth` stores it in
the desktop keyring (Secret Service: GNOME Keyring, KDE Wallet) under the
service name `streamdeck-lights`, one entry per bridge id. When no keyring is
reachable, or with `--store file`, it falls back to
`~/.config/hue/credentials.json` with 0600 permissions and prints a warning.
The key is never written to OpenDeck's plaintext settings.

**TLS.** The bridge's certificate is issued by Signify's private CA, which is
not published, so standard verification cannot apply. Instead the client
requires the certificate's common name to equal the bridge id and, after
pairing, pins the certificate's SHA-256 fingerprint in `~/.config/hue/config.json`.
A changed certificate is refused until you pair again.

**Debug output** redacts the application key header; everything else is
printed verbatim.
