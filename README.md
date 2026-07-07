# tork

Terminal UI for searching and downloading torrents, plus one-key downloads of
official Linux ISOs. Searches Knaben (metasearch), YTS, Nyaa, and any
RSS/Torznab feed; streams and ranks results; downloads over BitTorrent with live
progress.

![tork home screen](docs/screenshot.png)

## Install

### Arch Linux

Install from the AUR:

```sh
yay -S tork
# or
paru -S tork
```

Manual AUR install:

```sh
git clone https://aur.archlinux.org/tork.git
cd tork
makepkg -si
```

### Nix

With flakes:

```sh
nix run github:melqtx/tork
```

Install to your profile:

```sh
nix profile install github:melqtx/tork
```

From a clone:

```sh
nix run .
nix build .
./result/bin/tork
```

### Go

```sh
go install github.com/melqtx/tork/cmd/tork@latest
```

### From Source

```sh
go build -o tork ./cmd/tork && ./tork
```

Requires Go 1.26+. Config lives in `~/.tork/`; downloads go to your OS Downloads
folder (`~/Downloads/tork`) by default - override with `tork -d DIR` or
`download_dir` in `config.yaml`. Packaging files for Homebrew, AUR, and Nix are
in `packaging/`.

## Usage

- **Home** - type to search; `↑/↓` pick a destination; `enter` to go.
- **Linux ISOs** - a shelf of distros; `enter` resolves the **current** official
  image live from the project's mirrors and downloads it over BitTorrent.
  Desktops: Ubuntu, Debian, Fedora, Arch, EndeavourOS, CachyOS, Mint, Kali,
  NixOS. Servers: Ubuntu Server, Fedora Server, Rocky, AlmaLinux, Proxmox VE.
- **Results** - `enter` preview/download · `D` download now · `/` filter · `o`
  sort · `v` source-graph.
- **Preview** - `space`/`a`/`n` pick files · `enter` download the selection.
- **Downloads** - `s` seed · `p` pause · `x` remove (`y` keep files, `d` delete).
- `tab` cycles screens · `esc` back · `^c`/`q` quit.

### Autopilot(WIP)

```sh
tork autopilot "all breaking bad seasons 1080p"
tork autopilot --dry-run "inception 2160p"     # plan only
tork autopilot -n 5 --headless "the wire"      # cap 5, no TUI
```

Searches, ranks, dedupes, and queues the best sources; skips anything already in
your library. Configure defaults under `autopilot:` in `config.yaml`.

## Ranking (WIP)

Results are scored, not just seeder-sorted: swarm size (log-scaled) and health,
quality tags (2160p/1080p, REMUX/WEB-DL/BluRay, x265/AV1), and trust, minus dead
torrents and CAM/TS rips. `o` cycles score → seeders → size; weights are tunable
under `ranking:` in `config.yaml`.

## Files (`~/.tork/`)

- `config.yaml` - download dir, seeding, limits, ranking/autopilot weights, and
  per-provider settings (written with defaults on first run).
- `state.json` - added torrents, re-added on startup so downloads resume.
- `.torrent.bolt.db` - piece completion; restarts verify instead of re-downloading.

Add an RSS/Torznab source:

```yaml
providers:
  my_indexer:
    enabled: true
    type: rss
    search_url: "https://example.invalid/rss?q={query}"
```

Pirate Bay/Apibay, EZTV, and 1337x exist in `config.yaml` but are off by default
(unreliable or bot-walled). Only distros with official torrents are listed on the
ISOs screen (NixOS uses the community torrents web-seeded by NixOS's own servers).

## Develop

```sh
go vet ./... && go test -race ./...
```

Single-purpose packages under `internal/`: `provider`, `aggregator`, `engine`,
`rank`, `autopilot`, `isos`, `state`, `config`, `tui`. Add a fixture in
`internal/provider/testdata/` when you add a provider.


ps;
tork is a client for public indexers and the BitTorrent network; it hosts and
indexes nothing. You are responsible for complying with the laws of your
jurisdiction and the rights of content owners.
## License

MIT - see [LICENSE](LICENSE).
