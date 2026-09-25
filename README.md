# streamdeck: Philips Hue for OpenDeck

An [OpenDeck](https://github.com/nekename/OpenDeck) plugin that puts Philips
Hue lights, rooms and zones on Stream Deck keys, plus `hue`, a command-line
tool that does the pairing and can drive the lights on its own.

Everything is a single static Go binary: nothing to install on the machine
running OpenDeck, no Node.js, no Python.

## What you get

**OpenDeck plugin** (`com.github.llemaire.hue`)

- Three actions: **Toggle light**, **Toggle room**, **Toggle zone**.
- Pick the target from a dropdown filled from your bridge.
- Keys show the real state. Switch a light from the Hue app, a wall switch or
  the CLI and the key follows within a second, through the bridge's event
  stream.
- Key text: the target's name, your own text, or none.
- Errors flash the warning icon on the key and are explained in the plugin log.

**`hue` CLI**

```
hue discover                       find bridges (mDNS, then Signify's cloud lookup)
hue auth                           pair: press the bridge's link button when asked
hue auth status | forget
hue list lights | rooms | zones    [--json]
hue on|off|toggle light|room|zone  [--brightness N] [--dry-run] <name or id>
hue watch                          live changes from the bridge
hue plugin status | debug on|off   where the plugin lives, its log, debug output
```

Global flags go before the command (`hue --debug list lights`). Names are
matched case-insensitively within the given kind; a unique prefix is enough.

## Install

1. **Get the plugin zip**: `opendeck-hue-<version>.streamDeckPlugin` from a
   release, or build it with `make plugin-release` (needs Go 1.26+, `zip`).
2. **Install it in OpenDeck**: Settings > Plugins > install from file, or
   unzip into `~/.config/opendeck/plugins/` and restart OpenDeck.
3. **Pair with your bridge** once, from a terminal, with the `hue` binary
   for your platform (`make build` gives `bin/hue`; `make cross` builds all):

   ```
   hue auth
   ```

   It finds the bridge, asks you to press the round link button, and stores
   the key in your desktop keyring. `hue auth status` confirms the key works.
4. **Restart OpenDeck** if it was running during pairing, drag a Toggle
   action onto a key, and pick a light in the panel below the key.

Font, size, colour and position of the key text are OpenDeck's own key
settings. New keys default to a small title at the bottom.

## How it is put together

```
cmd/hue/              the CLI
cmd/opendeck-hue/     the plugin: settings, inspector requests, toggling, live state
plugin/               manifest, SVG icons, property inspector (HTML/CSS/JS, no build)
internal/hue/         bridge client: mDNS discovery, TLS pinning, pairing,
                      CLIP v2 lights/rooms/zones, event stream
internal/hue/huetest/ a fake bridge (HTTPS, seeded lights, event stream) for tests
internal/openaction/  plugin-side client for the OpenDeck / Stream Deck protocol
internal/secrets/     credential store: keyring (Secret Service, Keychain,
                      Credential Manager) with a 0600 file fallback
internal/config/      ~/.config/hue/config.json: bridges, certificate pins, defaults
```

The plugin and the CLI share the same packages and the same configuration,
so pairing once serves both.

Dependencies: `github.com/zalando/go-keyring` (keyring access) and
`github.com/coder/websocket` (plugin protocol). Everything else, including
the DNS/mDNS and Server-Sent Events code, is standard library.

## Development

```
make build              # bin/hue for this machine
make check              # gofmt, go vet, all tests (no bridge or deck needed)
make plugin-install     # build the plugin and copy it into ~/.config/opendeck/plugins
make opendeck-restart   # OpenDeck loads plugins at startup
make plugin-log         # follow OpenDeck's log and the plugin's log
make plugin-release     # cross-compile for 5 targets and zip; version from the git tag
make cross              # the CLI for every platform, under dist/cli/
```

Tests run against in-process fakes: an HTTPS bridge with a bridge-shaped
certificate and an event stream, an mDNS responder on localhost, and a
WebSocket host standing in for OpenDeck.

### Debugging

- `hue --debug <command>` prints to stderr every mDNS packet, every HTTP
  request and response (application key redacted), TLS decisions and
  credential store access.
- `hue plugin debug on` makes the plugin write the same detail, plus every
  protocol message, to `~/.local/state/opendeck-hue/plugin.log`. Restart
  OpenDeck for it to take effect. Off again with `hue plugin debug off`.
- OpenDeck's own log is `~/.local/share/opendeck/logs/opendeck.log`, in UTC.
- `hue watch` shows exactly the events the plugin reacts to.
- `--dry-run` on `on`, `off` and `toggle` prints the request instead of
  sending it.

### Things learned the hard way

- **mDNS and firewalld.** Answers to a query sent from a random UDP port are
  dropped by Fedora's default firewall zone. The client joins the multicast
  group on port 5353 instead, sharing it with Avahi and friends.
- **mDNS answer suppression.** A bridge will not repeat the same multicast
  answer within one second (RFC 6762 §6), so the question is re-sent every
  second inside the discovery window.
- **OpenDeck skips symlinked plugin folders**, silently. Install by copying.
- **The bridge sends only its own certificate**, issued by Signify's private
  CA, which is not published. See Security.

## Security

**Credentials.** The Hue application key is a secret. `hue auth` stores it in
the desktop keyring under the service name `streamdeck-lights`, one entry per
bridge id. If no keyring is reachable, or with `--store file`, it falls back
to `~/.config/hue/credentials.json` with 0600 permissions and says so. The key
is never written to OpenDeck's plaintext settings; the plugin reads it from
the same keyring.

**TLS.** Standard certificate verification cannot apply to the bridge (private
CA, no Subject Alternative Name). The client requires the certificate's
common name to equal the bridge id and, after pairing, pins the certificate's
SHA-256 fingerprint in `config.json`. A changed certificate is refused until
you pair again. Pairing happens while you physically press the bridge button,
which is the trust anchor.

**Cloud lookup.** `hue discover` and `hue auth` fall back to
`https://discovery.meethue.com/` only when mDNS finds nothing. It learns your
public IP and nothing else; `--mdns-only` disables it.

**Debug output** redacts the application key header; everything else is
printed verbatim.

## Platforms

Built for Linux (x86_64, aarch64), macOS (Intel, Apple Silicon) and Windows
(x86_64). Developed and tested on Fedora with KDE. On other platforms the
keyring is the OS one (Keychain, Credential Manager); run `hue auth` there
before using the plugin.
