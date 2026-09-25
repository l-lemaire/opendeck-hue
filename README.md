# opendeck-hue: Philips Hue for OpenDeck

Put your Philips Hue lights, rooms and zones on Stream Deck keys with
[OpenDeck](https://github.com/nekename/OpenDeck).

- **Three actions**: Toggle light, Toggle room, Toggle zone.
- **Pick from a list**: the key's panel shows your lights, rooms or zones,
  read from your bridge.
- **Keys show the real state**. Switch a light from the Hue app, a wall switch
  or a motion sensor and the key follows within a second.
- **Your text on the key**: the light's name, your own label, or nothing.
- **Nothing to install** besides the plugin. It is a single program with no
  runtime dependencies, for Linux, macOS and Windows.
- **Your bridge key stays private**: stored in your desktop keyring, never in
  plain text.

## Install

1. Download `opendeck-hue-<version>.streamDeckPlugin` from the
   [releases page](../../releases), together with the `hue` command-line
   tool for your platform.
2. In OpenDeck, open **Settings > Plugins** and install the downloaded file.
   Alternatively unzip it into OpenDeck's plugin folder
   (`~/.config/opendeck/plugins/` on Linux) and restart OpenDeck.

## Pair with your bridge

Pairing is done once, from a terminal, with the `hue` tool:

```
hue auth
```

It finds your bridge on the network, asks you to press the round button on
the bridge, and saves the resulting key in your keyring. Your desktop may ask
you to allow access to the keyring; accept it.

Check that everything works:

```
hue auth status
```

If OpenDeck was running during pairing, restart it once.

Several bridges? `hue auth` pairs one at a time; run it again with `--id` to
choose. If your bridge is not found automatically, pass its address:
`hue auth --ip 192.168.1.42`.

## Use the plugin

1. In OpenDeck, find the **Philips Hue** category in the action list.
2. Drag **Toggle light**, **Toggle room** or **Toggle zone** onto a key.
3. In the panel below the key, choose the target from the list. The list
   shows the current state of each entry; **Refresh** reloads it.
4. Press the key. It shows a lit icon when the target is on and a grey one
   when it is off. A room or zone counts as on when any of its lights is on.

**Key text.** The panel's *Key text* setting chooses what the plugin writes on
the key: the target's name, a custom text, or none. Font, size, colour and
position are OpenDeck's own key settings, in its key panel.

**Something wrong?** A key flashes a warning triangle when the plugin could
not reach the bridge or the target no longer exists. See Troubleshooting.

## The `hue` command-line tool

The same tool that pairs can also control lights, handy for scripts or a quick
check from a terminal.

```
hue discover                        find bridges on the network
hue auth                            pair (press the bridge button when asked)
hue auth status                     list paired bridges and check their keys
hue auth forget                     remove a bridge's key and settings

hue list lights | rooms | zones     show names, state and brightness (--json for scripts)
hue on     light|room|zone <name>   also: --brightness 1..100
hue off    light|room|zone <name>
hue toggle light|room|zone <name>
hue watch                           print changes as the bridge reports them

hue plugin status                   where the plugin is installed and logs
hue plugin debug on | off           detailed plugin log for troubleshooting
```

Names are matched without regard to case, and a unique beginning is enough:
`hue toggle light kitch`. Flags go right after the command, before the name:
`hue on light --brightness 30 kitchen`. Add `--dry-run` to see what would be
sent without sending it. `hue --debug <command>` shows every exchange with the
bridge; the key is redacted.

## Troubleshooting

- **The list in the key panel is empty or shows an error.** The message is
  the actual cause. Most often the bridge is not paired yet: run `hue auth`
  and restart OpenDeck.
- **"No bridge found".** The bridge must be on the same network. If your
  network blocks discovery, use `hue auth --ip <bridge address>`; the Hue app
  shows the address under Settings > My Hue system.
- **A key flashes a warning triangle.** Run `hue auth status` to check the
  bridge connection, and `hue list lights` to see whether the target still
  exists. If you replaced your bridge, pair again with `hue auth --force`.
- **The key does not follow changes made elsewhere.** Restart OpenDeck. The
  plugin reconnects to the bridge automatically, but a change of network can
  need a fresh start.
- **Logs.** `hue plugin status` prints the plugin's log location. `hue plugin
  debug on` followed by an OpenDeck restart records every exchange for a bug
  report; turn it off again afterwards.

## Privacy and security

- The bridge key is stored in your desktop keyring (GNOME Keyring, KDE Wallet,
  macOS Keychain, Windows Credential Manager). On a machine without a keyring
  it falls back to a file readable only by your user, and tells you.
- The plugin talks to the bridge directly on your local network. Nothing is
  sent to any server, with one exception: if the bridge cannot be found by
  network discovery, `hue discover` and `hue auth` ask Signify's discovery
  service for bridges seen from your internet address. `--mdns-only` disables
  this.
- The bridge's identity is pinned at pairing time; a bridge presenting a
  different certificate is refused until you pair again.
- OpenDeck itself never sees the key; the plugin's settings in your OpenDeck
  profile contain only names and identifiers.

## Building from source

Go 1.26 or newer, then `make build` for the `hue` tool and
`make plugin-install` to build the plugin into OpenDeck. See
[DEVELOPMENT.md](DEVELOPMENT.md) for the layout, tests and release process.

## License

MIT, see `LICENSE`.
