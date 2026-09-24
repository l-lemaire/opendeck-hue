# streamdeck

Control Philips Hue (and later Nanoleaf) lights from an OpenDeck plugin.

The first milestone is `hue`, a command-line tool to pair with a bridge, list
lights and toggle them. The plugin will reuse the same Go packages.

## Layout

```
cmd/hue/            the CLI executable
internal/hue/       Hue bridge client (discovery, pairing, TLS, lights)
internal/secrets/   credential storage (desktop keyring, file fallback)
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
bin/hue --debug discover         # show every packet and HTTP exchange
```

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

Hue application keys are secrets. They are stored in the desktop keyring by
default and never written to OpenDeck's plaintext settings. See
`internal/secrets`.
