# Development notes

For people changing the code. Users should read the README.

## Layout

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

The plugin and the CLI share the same packages and configuration, so pairing
once serves both.

Dependencies: `github.com/zalando/go-keyring` (keyring access) and
`github.com/coder/websocket` (plugin protocol). Everything else, including
the DNS/mDNS and Server-Sent Events code, is standard library. Binaries are
static (CGO disabled).

## Make targets

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

## Debugging

- `hue --debug <command>` prints every mDNS packet, HTTP request and response
  (application key redacted), TLS decision and credential store access.
- `hue plugin debug on` makes the plugin write the same, plus every protocol
  message, to `~/.local/state/opendeck-hue/plugin.log`. Restart OpenDeck.
- OpenDeck's own log is `~/.local/share/opendeck/logs/opendeck.log`, in UTC.
- `hue watch` shows exactly the events the plugin reacts to.

## Release

```
git tag -a vX.Y.Z -m "..."
make plugin-release          # dist/opendeck-hue-X.Y.Z.streamDeckPlugin
make cross                   # dist/cli/<triple>/bin/hue[.exe]
```

The manifest version is taken from the exact tag on HEAD (`vX.Y.Z` -> `X.Y.Z`);
without one it is `0.0.0`.

## Design notes and lessons

- **TLS.** The bridge sends only its own certificate, issued by Signify's
  private CA, which is not published, and it has no Subject Alternative Name.
  Standard verification cannot work, so the client checks that the
  certificate's common name equals the bridge id and pins the fingerprint at
  pairing time (`internal/hue/tls.go`).
- **mDNS and firewalld.** Answers to a query sent from a random UDP port are
  dropped by Fedora's default firewall zone, because a multicast query creates
  no matching connection-tracking entry. The client joins the multicast group
  on port 5353 instead, sharing it with Avahi and other clients.
- **mDNS answer suppression.** A responder will not repeat the same multicast
  answer within one second (RFC 6762 §6), so the question is re-sent every
  second inside the discovery window.
- **OpenDeck skips symlinked plugin folders**, silently. Install by copying.
- **OpenDeck passes the directory name as the plugin UUID** and runs the
  plugin with the plugin folder as working directory.
- **Scenes** are `scene` resources owned by a room or zone (`group`), recalled
  with `PUT scene/<id> {"recall":{"action":"active"|"dynamic_palette"}}`.
  `status.active` (`inactive`, `static`, `dynamic_palette`) is kept by the
  bridge and pushed on the event stream, which is how scene keys follow it.
  Names repeat across rooms, so a scene is always matched within its group.
- **Only two v1 API calls remain**: the unauthenticated `/api/0/config` probe
  and `POST /api` for creating a key. Everything else is CLIP v2.
- **Dry run lives in the HTTP client**, so no code path can write to the
  bridge by accident when it is on.
