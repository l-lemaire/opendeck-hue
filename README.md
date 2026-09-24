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
make run ARGS="--help"
make check              # gofmt, go vet, tests
make cross              # one binary per OS/arch under dist/
```

## Security notes

Hue application keys are secrets. They are stored in the desktop keyring by
default and never written to OpenDeck's plaintext settings. See
`internal/secrets`.
