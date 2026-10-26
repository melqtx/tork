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

- Search Knaben, YTS, Nyaa, TheRARBG, LimeTorrents, and all Pirate Bay categories,
  plus your own RSS or Torznab feeds.
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
go install github.com/melqtx/tork/cmd/tork@latest    # Go 1.26.10+
```

If Go on macOS is picking Nix's compiler, use Apple Clang for the install:

```sh
CC=/usr/bin/clang CXX=/usr/bin/clang++ go install github.com/melqtx/tork/cmd/tork@latest
```

## Build from source

Building tork requires Go 1.26.10 or newer. From the repository root, run it
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
checksum files, and sample clips start deselected in a collapsed Extras group — the header says how many
were grouped, and `a` puts them back. Expand the group with `→` to inspect them,
or select the entire group with Space. The preview shows selected size, the save
folder, and the Enter-to-download action together above the file list. If a bare hash is still finding metadata
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

## Search sources

Open **sources** from the home menu to see every built-in index, including
TheRARBG, LimeTorrents, The Pirate Bay (all categories), EZTV, and 1337x.
Use `↑↓` to choose a source and
`space` or `enter` to enable or disable it. Changes save to
`~/.tork/config.yaml` and apply to the next search. Current search results and
their magnet resolvers remain usable, even if their source is disabled or
removed. Knaben, YTS, and Nyaa remain enabled by default; existing choices
survive upgrades.

Enabled sources are checked automatically when you open Sources or enable one.
Recent readings are reused for five minutes. Press `r` to recheck all enabled
sources, or `t` to test the selected source (including a disabled one). Checks
send the `1080p` canary using the configured proxy and search timeout. A valid
empty feed is **working · 0 results**. Authentication failures, blocks,
timeouts, non-feed responses, and unsupported torrent links are separate
outcomes. Individual tests never change enabled/disabled choices. Search
reachability does not guarantee that a download will work.

A source blocked by site protection pauses for ten minutes in the current
session; other enabled sources keep returning results. Searches retry it after
the pause expires. `t` retries immediately in Sources or the `s` search-status
panel. A successful check clears the pause. No source is silently disabled,
and retrying never changes your proxy or mirror settings. Rebuilding the source
configuration resets pauses so a changed endpoint can be tried immediately.

The default EZTV integration uses its [official JSON API](https://eztvx.to/api/)
instead of the blocked HTML search. Show names are resolved to IMDb IDs using
[TVmaze's public API](https://www.tvmaze.com/api); those lookups use the same
configured proxy. Searches inspect up to 200 releases for each of the three
closest matching shows. Explicit custom EZTV HTML mirrors remain supported.
1337x tries its maintained public domains; site-side blocking is reported as
**blocked**, and never as a successful empty response. Its operator publishes
domain and availability notices on the [official status page](https://1337x-status.org/).

### Add a source

Press `a` in Sources to open the ready-to-use source catalog. Type a name to
filter, choose with `↑↓`, and press `enter` to add it. Tork saves it enabled,
checks it in the background, and keeps the picker open so you can add another.
Press `esc` when finished to see source check outcomes. No endpoint URL, account,
API key, or additional software is needed for a built-in source. A failed
network check leaves your explicit enabled choice intact; disable it with
`enter` or `space` if you do not want to search it.

- **TheRARBG** adds broad movie, TV, games, software, music and book search.
  This is TheRARBG, a separate public index, not the original RARBG service.
- **LimeTorrents** adds broad movie, TV, games, software, music and anime search.
  Search returns listings immediately; a selected listing's magnet is fetched
  on demand. Sponsored download links are not search results.
- **The Pirate Bay** searches every category, including applications and games.
  The older movie-only and TV-only configurations remain compatible, but the
  add catalog offers the all-category option to avoid confusing duplicates.

The new sources are opt-in. Updating Tork does not silently expand the list of
sites an existing installation searches. Source categories continue through the
shared content filter, including adult categories from all-category searches.

### Advanced: connect your own feed

Choose **Custom RSS / Torznab…** at the bottom of the add catalog. Enter a name,
choose the type with `←→` on the Type row, and enter its endpoint. `tab` moves
between fields; `esc` cancels without saving.

- **Torznab API:** use a public indexer's Torznab endpoint, including one copied
  from Jackett or Prowlarr. Enter its API key in the masked field. If a copied
  URL already includes `apikey=...`, testing moves that key into the masked
  field. Tork supplies `t=search`, `q`, and `extended=1` itself, preserving other
  query parameters such as categories.
- **RSS search feed:** use a search URL with `{query}` where the escaped search
  text belongs. A regular website address is not a search feed.

Press `ctrl+s` to test and save in one step, or `ctrl+t` to test first.
If the check fails, the editor stays open for repair; press `ctrl+s` again to
save it disabled. A successful check
saves the source enabled. Failed or partially supported sources can
be saved **disabled** for later repair. Changing a draft invalidates its check;
an old response cannot validate new settings. Custom-source saves make the
config owner-readable/writable only (`0600` on Unix), including RSS URLs that
contain credentials. Never share a credential-bearing config file.

Use `e` to edit a custom source; it keeps the same result identity when renamed.
Retest edited settings before saving if you want the source enabled. Use `x` to
remove a custom source, then `y` to confirm or `esc` to cancel. Built-ins can be
disabled; their mirror URLs are still edited in `config.yaml`.

Supported feed entries carry a valid v1 magnet in a link, GUID, enclosure, or
Torznab `magneturl` attribute, or a valid v1 infohash attribute. Custom hash-only
entries do not get extra public trackers injected. Private trackers and feeds
that only supply `.torrent` download URLs are unsupported in this release.
Unsupported entries are reported instead of silently appearing as an empty
successful search. Usable results from a partially supported feed still reach
search, without retrying the same permanent format error.

Advanced config example (a Torznab endpoint does not need `{query}`):

```yaml
providers:
  my_public_index:
    name: My public index
    enabled: false
    type: torznab
    search_url: http://localhost:9117/api/v2.0/indexers/INDEXER/results/torznab/api
    api_key: YOUR_API_KEY
  my_rss_index:
    name: My RSS index
    enabled: false
    type: rss
    search_url: 'https://example.org/search/rss?q={query}'
```

Configure and maintain indexers in your indexer manager, then add their
individual Torznab endpoints here. See the [Torznab specification](https://torznab.github.io/spec-1.3-draft/torznab/Specification-v1.3.html)
and [Jackett API documentation](https://github.com/Jackett/Jackett#api-usage).
Authentication, site protection, and upstream network routing managed by that
service remain its responsibility. Tork's requests to the configured endpoint
and its downloads continue to use Tork's configured route.

The screen shows checks from the current session, or the latest saved health
check for a source when available. Individual source tests are kept in memory;
`tork doctor --record` and the health screen retain their existing history flow.
To run live metadata-only probes of built-ins without starting a download engine
or editing your config:

```sh
TORK_LIVE_SOURCES=1 go test ./internal/sources -run '^TestLiveBuiltinSources$' -v -count=1
TORK_LIVE_SOURCES=1 go test ./internal/sources -run '^TestLiveBroadSources$' -v -count=1
```

## Keys

Press `?` anywhere for the full list of keys on the current screen. The
highlights:

- **home** type to search, `↓` focus the destination menu, `↑↓` move, `enter` activate the focused control; `esc` returns to the search field, then clears it
- **sources** `↑↓` choose, `space` / `enter` enable or disable, `t` test, `a` add, `e` edit custom, `x` remove custom, `esc` home
- **isos** `↑↓` browse, `enter` grab the latest official image
- **results** `enter` preview (or download when preview is disabled), `/` edit search, `tab` browse categories and destinations, `f` text filter, `o` sort, `z` hide zero seeders, `r` reset filters, `ctrl+f` resolution/size filters, `s` source status, `D` grab now, `Y` copy magnet
- **preview** `enter` download what's selected, `space` toggle a file or folder, `←→` fold, `a`/`n` select all or none
- **downloads** `enter` open the file, `o` show it in your file manager, `p` pause/resume, `s` seed, `v` fully verify completed data, `m` move, `r` relink, `y` copy full path, `Y` copy magnet, `x` remove, `d` delete data
- On home, `tab` / `shift+tab` moves between the search field and menu choices.
- In results, `tab` switches between the list and browse menu; choose Downloads there, or use `^d`. From Linux ISOs, `tab` opens downloads. In downloads, `tab` or `esc` returns to where you were.
- `F1` home, `F2` latest results, `F3` downloads, `F4` sources. These also work from the search field.
- `esc` goes back or cancels the current dialog; `^c` closes the UI
- Preview, move/relink, and removal dialogs keep focus until finished or cancelled.
- The `?` help panel scrolls with `↑↓` or `pgup/pgdn` on smaller terminals.

Queuing a download leaves you where you are and confirms with a small toast, so
you can grab several things from one search. Live transfer count and speed stay
visible beside the screen title, or in the footer on home. The top bar has no
function-key navigation labels; shortcuts remain available in `?` help. Home has no duplicate
wordmark/header above its search area.

On macOS and Linux, the interactive UI connects to a persistent local session.
Closing the UI with `^c` (or `q` where offered), closing the terminal, or losing
the terminal connection leaves approved downloads and seeding running. Reopen
`tork` to reconnect to the same search, selection, and downloads. Pending previews
still require approval; detaching never starts downloading their files.

```sh
tork          # start or reconnect to the UI
tork status   # check the background session without opening the UI
tork stop     # save state and stop transfers, keeping downloaded files
```

One terminal can attach at a time. The worker uses a user-private Unix socket;
it does not expose a remote-control TCP port. It owns the engine and resume state
until `tork stop`, and saves progress while the UI is closed. After stopping or
rebooting, start `tork` to resume saved downloads; this does not install an OS
startup service. The computer must stay awake for transfers to run.

Use `tork --foreground` for the previous behavior (closing the UI stops transfers).
Windows currently retains that foreground behavior. Stop the background session
before running foreground/autopilot transfers, changing `-d` or `--evil`, changing
proxy/engine settings, or testing a rebuilt binary. `--evil` applies to new downloads
in that worker session; it preserves previously saved seeding choices. A rebuilt
client detects an older worker and asks you to stop it before reconnecting.
`--config-dir DIR` gives the interactive UI, `status`, and `stop` a separate config
and session, which is useful for testing. Worker startup diagnostics go to
`~/.tork/session/worker.log` (or the equivalent custom config directory).

Clipboard copies run in the attached terminal, using its clipboard and tmux/SSH
environment. The UI confirms a copy only after that terminal reports the write;
closing the terminal cancels pending copies without stopping downloads.

A result tagged `yts+2` is one torrent that three indexes listed. Rather than
spend three lines on it, tork keeps the fullest release name, the best swarm
numbers any of them reported, and — the part that matters once the download
starts — the combined tracker list, so it announces to every swarm all three
knew about instead of the slice one listing happened to carry. The status line
counts how many listings were folded away; press `v` and the detail panel names
the sources and the tracker total. Results that only link to a details page keep
their own row: without a magnet there is no infohash to compare, and tork will
not fetch every page just to find out.

Search opens a browse workspace with an editable search field above one ranked
results table. Tork keeps its centered layout and green palette. On wide
terminals, a sidebar filters by All, Games, Movies, TV, Anime, Software, Music,
Books, or Other and opens Downloads, Sources, or Home. On narrow terminals,
Tab opens the same menu without squeezing the result titles. Categories use
individual result metadata; unrecognized categories remain under Other.

Press `/` to edit the search in place, `f` to filter the current list, `o` to
cycle relevance/seeders/size, and `z` to show only results with reported seeders.
Category changes do not repeat network searches. `r` clears text, size,
resolution and seeder filters while keeping the selected category. Enter keeps
the existing file preview flow, and Escape returns to the same results.

The selected-result details stay below a fixed-height list. Optional resolution
picks and release groups remain available with `v` and `V`.

Press `ctrl+f` for resolution and size preferences: choose multiple resolutions
with Space, set a maximum size, and choose Balanced, Smaller files, or Higher quality.
Enter applies; Escape cancels. No checked resolutions means all. Choices persist across searches
in the current session. Size limits exclude unknown sizes. Title, year and episode
matches rank before reported swarm counts; these counts do not guarantee speed.
A selected release stays visible while new results stream in. Result rows use
shorter readable titles, with source, size and reported seed counts alongside
them on wider terminals. The original release filename stays in the details.
The source summary separates indexes with results, empty responses, failures,
and searches still running. Press `s` to inspect each source's outcome for this
search, then `s` or Escape to return to the selected release. Search results keep
arriving while the status panel is open.

Advanced text filters still compose with
these choices. Press `f` and try:

```text
res:1080p,2160p seeders:>20 size:<8gb
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
