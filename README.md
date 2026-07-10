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

Config lives in `~/.tork/`; downloads land in `~/Downloads/tork` (change with
`tork -d DIR`).

## Keep an eye on it

`tork doctor` is read-only by default and checks config, disk, state, and
provider reachability. Add `--engine` for an opt-in listener check or `--record`
to save provider results in `~/.tork/health.json`. Health history contains local
provider timings plus torrent names and swarm counts; it is never uploaded.

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
- **results** `enter` preview/get, `D` grab now, `/` filter, `o` sort, `v` graph
- **downloads** `p` pause/resume, `s` seed, `v` verify, `m` move, `r` relink, `x` remove, `d` delete data
- `tab` cycle, `esc` back, `^c` quit

## Autopilot (WIP)

Describe what you want and let the cat fetch it:

```sh
tork autopilot "all breaking bad seasons 1080p"      # also: --dry-run, -n N, --headless
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

MIT, see [LICENSE](LICENSE).
