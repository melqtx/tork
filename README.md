# tork

```
 /\_/\
( ^.^ )  a cozy terminal for torrent search + one-key linux isos
 > ^ <
```

You name it, the cat fetches it. Search public torrent indexes, compare the
actual swarm, and keep downloads in one calm little terminal app.

![tork home screen](docs/home.png)

## What it does

- Search Knaben, YTS, Nyaa, plus your own RSS or Torznab feeds.
- Group duplicate releases, rank the useful ones, and surface seeders, size,
  source, and noisy results before you download.
- Preview a magnet, queue it with one key, then pause, verify, seed, move, or
  relink it from the downloads screen.
- Browse and grab current official Linux ISOs from Ubuntu, Debian, Fedora,
  Arch, NixOS, Proxmox, and more. No sketchy mirror hunting.
- Run `tork doctor` when a provider or local setup feels off, and use the swarm
  compass to see whether your sources and downloads are healthy over time.

![tork search results](docs/results.png)

## Install

```sh
brew tap melqtx/tap && brew install tork             # macOS (Homebrew)
yay -S tork                                          # Arch (AUR)
nix run github:melqtx/tork                           # Nix
go install github.com/melqtx/tork/cmd/tork@latest    # Go 1.26+
```

If Go on macOS is picking Nix's compiler, use Apple Clang for the install:

```sh
CC=/usr/bin/clang CXX=/usr/bin/clang++ go install github.com/melqtx/tork/cmd/tork@latest
```

## Build from source

Building tork requires Go 1.26 or newer. From the repository root, run it
directly without creating a binary:

```sh
go run ./cmd/tork
```

Or build and run a local binary:

```sh
go build -o ./bin/tork ./cmd/tork
./bin/tork
```

Config lives in `~/.tork/`; downloads land in `~/Downloads/tork` (change with
`tork -d DIR`).

Already have a torrent? Paste its magnet link, bare infohash, local `.torrent`
path, or `.torrent` URL into the same home search box. To go straight to the
quiet file preview:

```sh
tork 'magnet:?xt=urn:btih:…'
tork ~/Downloads/linux.iso.torrent
tork 'https://example.org/linux.iso.torrent'
tork --torrent-url 'https://example.org/download?id=123'
```

Nothing starts downloading until you confirm it. If a bare hash is still
finding metadata peers, wait to choose individual files or press enter to queue
the whole torrent immediately. Once metadata arrives, tork keeps a private
bounded copy under `~/.tork/metainfo/`, so reopening or resuming that magnet no
longer depends on finding a metadata peer.

Cache defaults are conservative and can be changed in `~/.tork/config.yaml`:

```yaml
metadata_cache:
  enabled: true
  max_mb: 256
  max_entries: 512
```

## SOCKS5 proxy

For the usual local Tor setup, one command is enough:

```sh
tork proxy tor
tork doctor --proxy-check
```

`tork proxy status` shows the redacted endpoint and strict-mode limits without
making a network request. To use another unauthenticated SOCKS5 proxy:

```sh
tork proxy set socks5://127.0.0.1:1080
```

For an authenticated endpoint, run `tork proxy set` without an argument and
enter the URL through hidden terminal input, so it never appears in shell
history or a process list. You can also configure tork by hand:

```yaml
proxy:
  socks5: "socks5://user:password@127.0.0.1:9050"
```

`socks5://` and `socks5h://` both resolve destination names at the proxy.
Username and password are optional and must be URL-encoded. For Tor, the usual
local endpoint is `socks5://127.0.0.1:9050`.

Credentials require `~/.tork/config.yaml` to be a regular file with mode
`0600`. A normal tork launch tightens an insecure regular config once; `tork
doctor` stays read-only and reports the problem instead.

Proxy mode is strict: searches, ISO requests, HTTP trackers, and outgoing TCP
peers use the proxy. tork disables DHT, uTP, UDP trackers, inbound peers, port
forwarding, and WebTorrent rather than risk a direct connection. Downloads may
find fewer peers as a result. The TUI keeps a visible `SOCKS strict` badge; it
becomes `((o)) Tor strict` only after a live verification confirms a Tor exit. A
check-service outage is shown as unavailable, not as a leak, and tork never
falls back to a direct connection.

## Keep an eye on it

`tork doctor` is read-only by default and checks config, disk, state, cached
metadata, and provider reachability. Add `--engine` for an opt-in listener
check, `--record` to save provider results in `~/.tork/health.json`, or
`--proxy-check` to ask the Tor Project's check service what egress IP it sees
through your configured proxy. That proves this explicit HTTP check used the
route; it is not a promise of anonymity or a claim about your normal
connection. Health history contains local provider timings plus torrent names
and swarm counts; it is never uploaded.

Automatic checks are off by default. Enable a local daily check with:

```yaml
health:
  enabled: true
  interval_hours: 24
```

The check sends the generic `1080p` canary query to enabled providers. Press
`H` in tork to view saved source and swarm history, or `r` there to record a
manual check.

## Keys

- **home** type to search, `↑↓` pick a destination, `enter` go
- **isos** `↑↓` browse, `enter` grab the latest official image
- **results** `enter` preview/get, `D` grab now, `Y` copy magnet, `/` filter, `o` sort, `v` graph
- **downloads** `p` pause/resume, `s` seed, `v` fully verify completed data, `m` move, `r` relink, `y` copy full path, `Y` copy magnet, `x` remove, `d` delete data, `o` reveal in Finder (macOS)
- `tab` cycle, `esc` back, `^c` quit

Verification rehashes completed torrent pieces. Direct downloads require a published SHA256; mismatches are moved aside as `.corrupt`, `.corrupt.1`, and so on before retrying.

## Autopilot (WIP)

Describe what you want and let the cat make a plan:

```sh
tork autopilot "all breaking bad seasons 1080p under 40GB"
tork autopilot --dry-run --min-seeders 20 "dune 2024 2160p"
```

Autopilot shows its picks, total known size, reasons, and a summary of rejected
results before asking to queue anything. Use `-n N` to cap the picks,
`--max-size 8GB` to cap each download, or `--category movies,anime` to narrow
provider categories. When the same limit appears in several places, the flag
wins over the request text, which wins over config defaults. `--headless` skips
the TUI but still asks before queuing; scripts and cron jobs need `--yes`.
Decisions stay local in `~/.tork/autopilot.jsonl` so a strange choice can be
inspected later.

Persistent defaults can live under `autopilot` in `~/.tork/config.yaml`:

```yaml
autopilot:
  max_downloads: 3
  min_seeders: 10
  max_size_gb: 40
  allowed_categories: [movies, anime]
```

## Legal

tork is a BitTorrent client and search tool. It does not host files, operate
trackers, or control the third-party providers it can search.

Use it only for content you are allowed to download and share, such as official
Linux ISOs, public-domain media, open-source software, and your own files. You
are responsible for following local law and the terms of any provider you
enable.

Provider availability and results can change without notice. tork does not
endorse or guarantee third-party content.

A proxy routes tork's traffic, but it is not a promise of anonymity or legal
protection.

MIT, see [LICENSE](LICENSE).
