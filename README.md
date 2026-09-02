# tork

```
 /\_/\
( ^.^ )  a cozy terminal for torrent search + verified downloads
 > ^ <
```

You name it, the cat fetches it. Search public torrent indexes, compare the
actual swarm, and keep downloads in one calm little terminal app.

![tork home screen](docs/home.png)

## What it does

- Search Knaben, YTS, Nyaa, plus your own RSS or Torznab feeds.
- Fold the same torrent listed on several indexes into a single row, and grab it
  with every tracker those indexes knew about between them.
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

Nothing starts downloading until you confirm it. `enter` takes whatever is
selected, from wherever the cursor happens to be. Tracker adverts, NFO and
checksum files, and sample clips start deselected — the header says how many
were skipped, and `a` puts them back. If a bare hash is still finding metadata
peers, wait to choose individual files or press enter to queue the whole
torrent immediately. Once metadata arrives, tork keeps a private
bounded copy under `~/.tork/metainfo/`, so reopening or resuming that magnet no
longer depends on finding a metadata peer.

Cache defaults are conservative and can be changed in `~/.tork/config.yaml`:

```yaml
metadata_cache:
  enabled: true
  max_mb: 256
  max_entries: 512
```

Advanced torrent-client limits are optional; zero or omitted values retain the
anacrolix defaults:

```yaml
torrent_tuning:
  half_open_conns_per_torrent: 25
  total_half_open_conns: 100
  piece_hashers_per_torrent: 2
  max_unverified_bytes: 67108864
  dial_rate_limit: 10
  peer_high_water: 500
  peer_low_water: 50
  download_rate_limit: 0
  upload_rate_limit: 0
  disable_aggressive_upload: false
  no_upload: false
```

Transfer rates are client-wide bytes per second; dial rate is dials per second.
`no_upload` prevents all torrent uploads, including seeding. The torrent library
does not expose its webseed concurrency as a runtime client setting, so tork
does not claim to configure it here.

Direct HTTP downloads use verified byte ranges when the server supports them:

```yaml
direct:
  max_connections: 4
  min_chunk_size: "10MB"
  enable_chunking: true
```

The connection limit is per direct download. tork confirms range support and a
stable resource before splitting; small, unknown-length, or incompatible
responses automatically use one sequential connection. Paused segmented
transfers resume from an internal `.part.meta` sidecar, and the final filename
still appears only after its published SHA-1, SHA-256, or SHA-512 digest is
verified.

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

Press `?` anywhere for the full list of keys on the current screen. The
highlights:

- **home** type to search, `↑↓` pick a destination, `enter` go
- **isos** `↑↓` browse, `enter` grab the latest official image
- **results** `enter` preview/get, `D` grab now, `Y` copy magnet, `/` smart filter, `o` sort, `v` graph
- **preview** `enter` download what's selected, `space` toggle a file or folder, `←→` fold, `a`/`n` select all or none
- **downloads** `enter` open the file, `o` show it in your file manager, `p` pause/resume, `s` seed, `v` fully verify completed data, `m` move, `r` relink, `y` copy full path, `Y` copy magnet, `x` remove, `d` delete data
- `tab` cycle, `^d` jump to downloads, `esc` back, `^c` quit

Queuing a download leaves you where you are and confirms with a small toast, so
you can grab several things from one search. The header shows live transfer
count and speed from every screen.

A result tagged `yts+2` is one torrent that three indexes listed. Rather than
spend three lines on it, tork keeps the fullest release name, the best swarm
numbers any of them reported, and — the part that matters once the download
starts — the combined tracker list, so it announces to every swarm all three
knew about instead of the slice one listing happened to carry. The status line
counts how many listings were folded away; press `v` and the detail panel names
the sources and the tracker total. Results that only link to a details page keep
their own row: without a magnet there is no infohash to compare, and tork will
not fetch every page just to find out.

Result filters compose with ordinary fuzzy title matching. Press `/` and try:

```text
res:1080p seeders:>20 size:<8gb
linux codec:x265 -source:cam
provider:nyaa category:anime is:trusted
is:hdr -is:dv
```

Available fields are `res`, `seeders`, `size`, `source`, `codec`, `provider`,
and `category`. Attributes include `is:trusted`, `is:hdr`, `is:dv`, and
`is:pack`; prefix any structured filter with `-` to negate it.

Verification rehashes completed torrent pieces and direct-download checksums.
Checksum mismatches are moved aside as `.corrupt`, `.corrupt.1`, and so on
before retrying.

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
