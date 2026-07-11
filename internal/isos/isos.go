// Package isos is a curated catalog of major Linux distributions. Each entry
// knows where the project publishes its current image, so tork resolves the
// latest release live at selection time rather than shipping links that rot
// on every point release.
//
// Most distros publish official torrents: those download over BitTorrent from
// official mirrors and users seed the image back afterward. A few (Gentoo,
// openSUSE) ship no torrents at all; for them the entry resolves a direct
// https download from the official mirror plus its published sha256, which
// the engine verifies as the bytes arrive. Either way it is official images
// from official infrastructure - squarely within tork's lawful-content
// charter.
package isos

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Distro is one entry in the ISO catalog.
type Distro struct {
	ID       string // stable key, e.g. "debian"
	Name     string // display name, e.g. "Debian"
	Edition  string // the flavor tork resolves, e.g. "netinst · amd64"
	Blurb    string // one-line cozy description
	Homepage string

	// IndexURL is the official page tork scrapes for the current torrent.
	IndexURL string
	// Match narrows the candidates on the page: a torrent qualifies only if
	// its filename/URL (lowercased) contains every one of these tokens.
	Match []string
	// Prefer breaks ties between qualifying candidates; an earlier token is a
	// stronger preference (used to pick an edition when several are listed).
	Prefer []string

	// Category groups entries under a divider in the shelf ("desktop" or
	// "servers"). Empty and unknown values fall under "desktop".
	Category string

	// Direct marks a distro that publishes no torrent: IndexURL lists raw
	// .iso files instead, and the newest match is downloaded over https,
	// verified against a published checksum when one exists.
	Direct bool
	// SumSuffix overrides the sibling checksum filename for Direct entries:
	// the checksum lives at <iso><SumSuffix> (default ".sha256"). e.g. Bazzite
	// uses "-CHECKSUM".
	SumSuffix string
	// SumFile, when set, is a single checksum file (relative to IndexURL)
	// listing every image; used when there is no per-iso sibling checksum.
	SumFile string

	// resolve overrides the generic page scraper for distros that need custom
	// navigation (e.g. Ubuntu's per-version directories). nil = generic.
	resolve func(ctx context.Context, c *http.Client, d Distro) (Image, error)
}

// categoryOrder is the display order of shelf sections; entries within a
// category keep their Catalog() order.
var categoryOrder = []string{"desktop", "servers"}

// CategoryOf returns a distro's shelf section, defaulting to the first.
func CategoryOf(d Distro) string {
	switch d.Category {
	case "", "desktop", "niche & advanced":
		return categoryOrder[0]
	case "servers", "servers & homelab":
		return categoryOrder[1]
	}
	return categoryOrder[0]
}

// Image is a resolved, ready-to-add image: exactly one of URL (a .torrent
// file), Magnet, or DirectURL is set.
type Image struct {
	DistroID  string
	Title     string // resolved image name, e.g. "debian-12.7.0-amd64-netinst.iso"
	URL       string // .torrent file URL
	Magnet    string // magnet URI
	DirectURL string // plain-https ISO URL (distros without torrents)
	SHA256    string // published hex digest for DirectURL; "" = none found
}

// Catalog returns the built-in distro list, grouped by category in display
// order (desktop, then servers).
func Catalog() []Distro {
	return []Distro{
		// --- desktop ---
		// Ubuntu family
		{
			ID: "ubuntu", Name: "Ubuntu", Edition: "desktop · amd64 · LTS",
			Blurb:    "the friendly default; latest long-term-support desktop",
			Homepage: "https://ubuntu.com",
			IndexURL: "https://releases.ubuntu.com/",
			Match:    []string{".torrent", "desktop", "amd64"},
			resolve:  resolveUbuntu,
		},
		{
			ID: "kubuntu", Name: "Kubuntu", Edition: "desktop · amd64 · LTS",
			Blurb:    "Ubuntu with KDE Plasma out of the box",
			Homepage: "https://kubuntu.org",
			IndexURL: "https://cdimage.ubuntu.com/kubuntu/releases/",
			Match:    []string{".torrent", "desktop", "amd64"},
			resolve:  resolveCdimageFlavor,
		},
		{
			ID: "xubuntu", Name: "Xubuntu", Edition: "desktop · amd64 · LTS",
			Blurb:    "Ubuntu with the light Xfce desktop",
			Homepage: "https://xubuntu.org",
			IndexURL: "https://cdimage.ubuntu.com/xubuntu/releases/",
			Match:    []string{".torrent", "desktop", "amd64"},
			resolve:  resolveCdimageFlavor,
		},
		{
			ID: "lubuntu", Name: "Lubuntu", Edition: "desktop · amd64 · LTS",
			Blurb:    "Ubuntu with LXQt; easy on old hardware",
			Homepage: "https://lubuntu.me",
			IndexURL: "https://cdimage.ubuntu.com/lubuntu/releases/",
			Match:    []string{".torrent", "desktop", "amd64"},
			resolve:  resolveCdimageFlavor,
		},
		{
			ID: "ubuntu-mate", Name: "Ubuntu MATE", Edition: "desktop · amd64 · LTS",
			Blurb:    "Ubuntu with the traditional MATE desktop",
			Homepage: "https://ubuntu-mate.org",
			IndexURL: "https://cdimage.ubuntu.com/ubuntu-mate/releases/",
			Match:    []string{".torrent", "desktop", "amd64"},
			resolve:  resolveCdimageFlavor,
		},
		{
			ID: "mint", Name: "Linux Mint", Edition: "cinnamon · 64-bit",
			Blurb:    "cozy and familiar; Cinnamon edition",
			Homepage: "https://linuxmint.com",
			IndexURL: "https://torrents.linuxmint.com/",
			Match:    []string{".torrent", "cinnamon", "64bit"},
		},
		{
			// Pop!_OS ships no torrents; its build API returns the current
			// direct-download URL plus sha256 for the chosen channel.
			ID: "popos", Name: "Pop!_OS", Edition: "intel/amd · amd64",
			Blurb:    "System76's polished, GNOME-based desktop",
			Homepage: "https://pop.system76.com",
			IndexURL: "https://api.pop-os.org/builds/24.04/intel",
			resolve:  resolvePopOS,
		},
		{
			// elementary publishes magnets (not .torrent files) on its homepage;
			// the amd64 token also keeps the arm64 image from matching.
			ID: "elementary", Name: "elementary OS", Edition: "amd64",
			Blurb:    "the thoughtful, design-first desktop",
			Homepage: "https://elementary.io",
			IndexURL: "https://elementary.io/",
			Match:    []string{"magnet", "elementaryos", "amd64"},
		},

		// Debian family
		{
			ID: "debian", Name: "Debian", Edition: "netinst · amd64",
			Blurb:    "the universal OS; stable netinst image",
			Homepage: "https://www.debian.org",
			IndexURL: "https://cdimage.debian.org/debian-cd/current/amd64/bt-cd/",
			Match:    []string{".torrent", "amd64", "netinst"},
		},
		{
			ID: "debian-live", Name: "Debian Live", Edition: "gnome live · amd64",
			Blurb:    "try Debian without installing; GNOME live image",
			Homepage: "https://www.debian.org/CD/live/",
			IndexURL: "https://cdimage.debian.org/debian-cd/current-live/amd64/bt-hybrid/",
			Match:    []string{".torrent", "live", "amd64", "gnome"},
		},
		{
			// MX ships direct ISO downloads on SourceForge. The RSS endpoint avoids
			// the Cloudflare-challenged HTML listing and still gives exact file URLs.
			ID: "mxlinux", Name: "MX Linux", Edition: "xfce · x64",
			Blurb:    "midweight, Debian-based, antiX-tuned; Xfce edition",
			Homepage: "https://mxlinux.org",
			IndexURL: "https://sourceforge.net/projects/mx-linux/rss?path=/Final/Xfce",
			Match:    []string{"mx-", "_xfce_", "x64", ".iso"},
			Prefer:   []string{"_xfce_x64.iso"},
			resolve:  resolveSourceForgeRSS,
		},
		{
			// the "raspios_arm64/" path token pins the standard 64-bit desktop
			// image, excluding the full/lite/oldstable variants on the same page.
			ID: "raspios", Name: "Raspberry Pi OS", Edition: "desktop · arm64",
			Blurb:    "the Pi's own Debian; 64-bit desktop image",
			Homepage: "https://www.raspberrypi.com/software/",
			IndexURL: "https://www.raspberrypi.com/software/operating-systems/",
			Match:    []string{".torrent", "raspios_arm64/"},
		},
		{
			ID: "kali", Name: "Kali Linux", Edition: "installer · amd64",
			Blurb:    "the security toolbox; installer image",
			Homepage: "https://www.kali.org",
			IndexURL: "https://cdimage.kali.org/current/",
			Match:    []string{".torrent", "installer", "amd64"},
		},

		// Fedora family
		{
			ID: "fedora", Name: "Fedora", Edition: "workstation · x86_64",
			Blurb:    "leading-edge, upstream-first; Workstation live",
			Homepage: "https://fedoraproject.org",
			IndexURL: "https://torrent.fedoraproject.org/torrents/",
			Match:    []string{".torrent", "workstation", "x86_64"},
			Prefer:   []string{"live"},
		},
		{
			ID: "fedora-kde", Name: "Fedora KDE", Edition: "plasma · x86_64",
			Blurb:    "Fedora's KDE Plasma edition; the Desktop live image",
			Homepage: "https://fedoraproject.org/kde/",
			IndexURL: "https://torrent.fedoraproject.org/torrents/",
			Match:    []string{".torrent", "kde-desktop", "x86_64"},
		},
		{
			ID: "fedora-xfce", Name: "Fedora Xfce", Edition: "spin · x86_64",
			Blurb:    "the lightweight Xfce spin; live image",
			Homepage: "https://spins.fedoraproject.org/xfce/",
			IndexURL: "https://torrent.fedoraproject.org/torrents/",
			Match:    []string{".torrent", "xfce-live", "x86_64"},
		},
		{
			ID: "fedora-cosmic", Name: "Fedora COSMIC", Edition: "spin · x86_64",
			Blurb:    "System76's Rust COSMIC desktop; live image",
			Homepage: "https://fedoraproject.org/spins/cosmic",
			IndexURL: "https://torrent.fedoraproject.org/torrents/",
			Match:    []string{".torrent", "cosmic-live", "x86_64"},
		},
		{
			ID: "fedora-sway", Name: "Fedora Sway", Edition: "spin · x86_64",
			Blurb:    "the tiling Wayland Sway spin; live image",
			Homepage: "https://fedoraproject.org/spins/sway",
			IndexURL: "https://torrent.fedoraproject.org/torrents/",
			Match:    []string{".torrent", "sway-live", "x86_64"},
		},
		{
			ID: "fedora-i3", Name: "Fedora i3", Edition: "spin · x86_64",
			Blurb:    "the i3 tiling window-manager spin; live image",
			Homepage: "https://fedoraproject.org/spins/i3",
			IndexURL: "https://torrent.fedoraproject.org/torrents/",
			Match:    []string{".torrent", "i3-live", "x86_64"},
		},
		{
			// Bazzite ships a fixed "stable" alias plus a "-CHECKSUM" sibling;
			// IndexURL points straight at the iso (no listing to scrape).
			ID: "bazzite", Name: "Bazzite", Edition: "stable · amd64",
			Blurb:     "gaming-tuned atomic Fedora; Steam Deck & desktop",
			Homepage:  "https://bazzite.gg",
			IndexURL:  "https://download.bazzite.gg/bazzite-stable-amd64.iso",
			Direct:    true,
			SumSuffix: "-CHECKSUM",
		},

		// Arch family
		{
			ID: "archlinux", Name: "Arch Linux", Edition: "x86_64",
			Blurb:    "roll your own; the monthly install medium",
			Homepage: "https://archlinux.org",
			IndexURL: "https://archlinux.org/download/",
			Match:    []string{"archlinux", "x86_64"},
		},
		{
			ID: "endeavouros", Name: "EndeavourOS", Edition: "x86_64",
			Blurb:    "Arch made approachable; terminal-centric installer",
			Homepage: "https://endeavouros.com",
			IndexURL: "https://endeavouros.com/",
			Match:    []string{"endeavouros"},
			Prefer:   []string{"magnet"}, // mirror-agnostic magnet over a single mirror's .torrent
		},
		{
			ID: "cachyos", Name: "CachyOS", Edition: "desktop · x86_64",
			Blurb:    "performance-tuned Arch; the desktop image",
			Homepage: "https://cachyos.org",
			IndexURL: "https://cachyos.org/download/",
			Match:    []string{".torrent", "desktop"},
			resolve:  resolveCachy,
		},

		// SUSE family
		{
			ID: "opensuse", Name: "openSUSE", Edition: "tumbleweed dvd · x86_64",
			Blurb:    "rolling yet stable; the Tumbleweed DVD installer",
			Homepage: "https://www.opensuse.org",
			IndexURL: "https://download.opensuse.org/tumbleweed/iso/openSUSE-Tumbleweed-DVD-x86_64-Current.iso",
			resolve:  resolveOpenSUSE,
		},
		{
			ID: "opensuse-leap", Name: "openSUSE Leap", Edition: "15.6 dvd · x86_64",
			Blurb:    "the stable, point-release openSUSE; Leap DVD",
			Homepage: "https://get.opensuse.org/leap/",
			IndexURL: "https://download.opensuse.org/distribution/leap/15.6/iso/openSUSE-Leap-15.6-DVD-x86_64-Current.iso",
			resolve:  resolveOpenSUSE,
		},

		// independents
		{
			// NixOS ships no official torrents, so this resolves the current
			// community-published torrents (AnimMouse/NixOS-ISO-Torrents) - which
			// are web-seeded by NixOS's own servers, so the data is authentic.
			ID: "nixos", Name: "NixOS", Edition: "graphical · x86_64",
			Blurb:    "reproducible & declarative; graphical live installer",
			Homepage: "https://nixos.org",
			IndexURL: "https://api.github.com/repos/AnimMouse/NixOS-ISO-Torrents/releases/latest",
			Match:    []string{".torrent", "graphical", "x86_64"},
			resolve:  resolveNixOS,
		},
		{
			// Gentoo ships no torrents (and no credibly web-seeded community
			// ones), so this is a direct download from the official CDN,
			// verified against the published sha256.
			ID: "gentoo", Name: "Gentoo", Edition: "minimal install · amd64",
			Blurb:    "compile it your way; the weekly minimal install CD",
			Homepage: "https://www.gentoo.org",
			IndexURL: "https://distfiles.gentoo.org/releases/amd64/autobuilds/current-install-amd64-minimal/",
			Match:    []string{"install-amd64-minimal", ".iso"},
			Direct:   true,
		},
		{
			// Void ships no torrents; its live directory is an autoindex with a
			// single sha256sum.txt covering every image.
			ID: "void", Name: "Void Linux", Edition: "xfce live · x86_64",
			Blurb:    "independent, runit-init, rolling; Xfce live image",
			Homepage: "https://voidlinux.org",
			IndexURL: "https://repo-default.voidlinux.org/live/current/",
			Match:    []string{"void-live-x86_64", "xfce", ".iso"},
			Direct:   true,
			SumFile:  "sha256sum.txt",
		},
		{
			ID: "alpine", Name: "Alpine Linux", Edition: "standard · x86_64",
			Blurb:    "tiny, musl-based, security-minded; standard image",
			Homepage: "https://alpinelinux.org",
			IndexURL: "https://dl-cdn.alpinelinux.org/alpine/latest-stable/releases/x86_64/",
			Match:    []string{"alpine-standard", "x86_64", ".iso"},
			Direct:   true,
		},
		{
			ID: "qubes", Name: "Qubes OS", Edition: "x86_64",
			Blurb:    "security by compartmentalization; a reasonably secure OS",
			Homepage: "https://www.qubes-os.org",
			IndexURL: "https://www.qubes-os.org/downloads/",
			Match:    []string{".torrent", "qubes", "x86_64"},
		},

		// --- servers ---
		{
			ID: "ubuntu-server", Name: "Ubuntu Server", Edition: "live-server · amd64 · LTS",
			Blurb:    "the cloud & homelab workhorse; headless LTS installer",
			Homepage: "https://ubuntu.com/server",
			IndexURL: "https://releases.ubuntu.com/",
			Match:    []string{".torrent", "live-server", "amd64"},
			Category: "servers",
			resolve:  resolveUbuntu,
		},
		{
			ID: "fedora-server", Name: "Fedora Server", Edition: "dvd · x86_64",
			Blurb:    "Fedora for servers; the network-install DVD",
			Homepage: "https://fedoraproject.org/server/",
			IndexURL: "https://torrent.fedoraproject.org/torrents/",
			Match:    []string{".torrent", "server", "x86_64"},
			Category: "servers",
		},
		{
			ID: "rocky", Name: "Rocky Linux", Edition: "minimal · x86_64",
			Blurb:    "enterprise, RHEL-compatible; minimal image",
			Homepage: "https://rockylinux.org",
			IndexURL: "https://download.rockylinux.org/pub/rocky/10/isos/x86_64/",
			Match:    []string{".torrent", "x86_64", "minimal"},
			Category: "servers",
		},
		{
			ID: "almalinux", Name: "AlmaLinux", Edition: "x86_64",
			Blurb:    "enterprise, RHEL-compatible; installer image",
			Homepage: "https://almalinux.org",
			IndexURL: "https://repo.almalinux.org/almalinux/10/isos/x86_64/",
			Match:    []string{".torrent", "x86_64"},
			Prefer:   []string{"dvd", "minimal"},
			Category: "servers",
		},
		{
			ID: "proxmox", Name: "Proxmox VE", Edition: "installer · amd64",
			Blurb:    "the virtualization platform; bare-metal installer",
			Homepage: "https://www.proxmox.com",
			IndexURL: "https://enterprise.proxmox.com/iso/",
			Match:    []string{".torrent", "proxmox-ve_"},
			Category: "servers",
		},
	}
}

// maxIndexBytes caps a fetched listing page: these are small HTML/autoindex
// pages, so this only guards against a broken or hostile endpoint.
var maxIndexBytes int64 = 8 << 20

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// DefaultClient is shared by all resolvers: a bounded timeout and a redirect
// cap so a redirect loop can't hang.
var DefaultClient = &http.Client{
	Timeout: 25 * time.Second,
	CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) >= 6 {
			return errors.New("stopped after 6 redirects")
		}
		return nil
	},
}

// Resolve fetches the distro's current official image with the default client.
func Resolve(ctx context.Context, d Distro) (Image, error) {
	return ResolveWithClient(ctx, d, nil)
}

// ResolveWithClient fetches the distro's current official image with client.
// A nil client preserves Resolve's default client behavior.
func ResolveWithClient(ctx context.Context, d Distro, client *http.Client) (Image, error) {
	if client == nil {
		client = DefaultClient
	}
	if d.resolve != nil {
		return d.resolve(ctx, client, d)
	}
	if d.Direct {
		return resolveDirect(ctx, client, d)
	}
	return resolveGeneric(ctx, client, d)
}

func resolveGeneric(ctx context.Context, c *http.Client, d Distro) (Image, error) {
	body, err := fetchBytes(ctx, c, d.IndexURL)
	if err != nil {
		return Image{}, err
	}
	t, err := selectBest(d, parseCandidates(d.IndexURL, body))
	if err != nil {
		return Image{}, fmt.Errorf("%s: %w", d.Name, err)
	}
	return t, nil
}

func fetchBytes(ctx context.Context, c *http.Client, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: unexpected status %d", rawURL, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxIndexBytes))
}

func fetchSourceForgeBytes(ctx context.Context, c *http.Client, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: unexpected status %d", rawURL, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxIndexBytes))
}

// candidate is one torrent found on a page.
type candidate struct {
	name   string // filename or magnet display-name
	url    string // absolute .torrent URL (empty for magnets)
	magnet string // magnet URI (empty for .torrent files)
}

var (
	reTorrentHref = regexp.MustCompile(`(?i)href\s*=\s*["']([^"'\s]+\.torrent)["']`)
	reMagnet      = regexp.MustCompile(`(?i)magnet:\?[^"'\s<>]+`)
	reVersion     = regexp.MustCompile(`\d+(?:\.\d+)*`)
	// reArch strips CPU-architecture tokens before version parsing, so the "86"
	// in "x86_64" (or "64" in "amd64") is never mistaken for a version number.
	reArch = regexp.MustCompile(`(?i)x86[_-]?64|amd64|aarch64|arm64|i[36]86|64[_-]?bit`)
)

// parseCandidates extracts every .torrent link and magnet URI from an HTML or
// autoindex page, resolving relative hrefs against base.
func parseCandidates(base string, body []byte) []candidate {
	baseURL, _ := url.Parse(base)
	var out []candidate
	seen := make(map[string]bool)

	for _, m := range reTorrentHref.FindAllSubmatch(body, -1) {
		href := html.UnescapeString(string(m[1]))
		abs := href
		if baseURL != nil {
			if ref, err := url.Parse(href); err == nil {
				abs = baseURL.ResolveReference(ref).String()
			}
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		out = append(out, candidate{name: fileName(abs), url: abs})
	}

	for _, m := range reMagnet.FindAll(body, -1) {
		mag := html.UnescapeString(string(m)) // sites often escape & as &amp;
		if seen[mag] {
			continue
		}
		seen[mag] = true
		out = append(out, candidate{name: magnetName(mag), magnet: mag})
	}
	return out
}

// selectBest filters candidates by d.Match, then picks the newest version,
// breaking ties by d.Prefer order.
func selectBest(d Distro, cands []candidate) (Image, error) {
	var kept []candidate
	for _, c := range cands {
		if matchesAll(c, d.Match) {
			kept = append(kept, c)
		}
	}
	if len(kept) == 0 {
		return Image{}, errors.New("no matching torrent on the official page (mirror layout may have changed)")
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if v := compareVersions(candVersion(kept[i]), candVersion(kept[j])); v != 0 {
			return v > 0 // newer first
		}
		return preferRank(kept[i], d.Prefer) < preferRank(kept[j], d.Prefer)
	})
	best := kept[0]
	return Image{DistroID: d.ID, Title: best.name, URL: best.url, Magnet: best.magnet}, nil
}

func matchesAll(c candidate, tokens []string) bool {
	hay := strings.ToLower(c.name + " " + c.url + " " + c.magnet)
	for _, t := range tokens {
		if !strings.Contains(hay, strings.ToLower(t)) {
			return false
		}
	}
	return true
}

// preferRank is the index of the first Prefer token present in the candidate;
// candidates matching no token sort last.
func preferRank(c candidate, prefer []string) int {
	hay := strings.ToLower(c.name + " " + c.url + " " + c.magnet)
	for i, p := range prefer {
		if strings.Contains(hay, strings.ToLower(p)) {
			return i
		}
	}
	return len(prefer)
}

func candVersion(c candidate) []int {
	return parseVersion(c.name)
}

// parseVersion pulls the first dotted-number run out of s as version segments,
// after removing architecture tokens so they can't be read as a version.
func parseVersion(s string) []int {
	s = reArch.ReplaceAllString(s, "")
	m := reVersion.FindString(s)
	if m == "" {
		return nil
	}
	var out []int
	for _, p := range strings.Split(m, ".") {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}

// compareVersions returns 1 if a > b, -1 if a < b, 0 if equal.
func compareVersions(a, b []int) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}

func fileName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	name := path.Base(u.Path)
	if dec, err := url.PathUnescape(name); err == nil {
		return dec
	}
	return name
}

// magnetName returns the display-name (dn) of a magnet URI, or the raw URI.
func magnetName(mag string) string {
	q := mag
	if i := strings.IndexByte(mag, '?'); i >= 0 {
		q = mag[i+1:]
	}
	if vals, err := url.ParseQuery(q); err == nil {
		if dn := vals.Get("dn"); dn != "" {
			return dn
		}
	}
	return mag
}

// --- Ubuntu & flavors: two-step resolver (pick latest LTS dir, then image) ---

// resolveUbuntu resolves the mainline Ubuntu images from releases.ubuntu.com,
// where each version dir holds the torrents directly.
func resolveUbuntu(ctx context.Context, c *http.Client, d Distro) (Image, error) {
	return resolveUbuntuFlavor(ctx, c, d, "")
}

// resolveCdimageFlavor resolves the official Ubuntu flavors (Kubuntu, Xubuntu,
// …) from cdimage.ubuntu.com, where the torrents live one level deeper under
// <ver>/release/.
func resolveCdimageFlavor(ctx context.Context, c *http.Client, d Distro) (Image, error) {
	return resolveUbuntuFlavor(ctx, c, d, "release/")
}

// resolveUbuntuFlavor scans IndexURL for LTS version directories and, newest
// first, looks for a matching torrent under <ver>/<subPath>. Falling back to
// the previous LTS lets pre-release dirs (e.g. an empty 26.04/) exist without
// breaking resolution.
func resolveUbuntuFlavor(ctx context.Context, c *http.Client, d Distro, subPath string) (Image, error) {
	index, err := fetchBytes(ctx, c, d.IndexURL)
	if err != nil {
		return Image{}, err
	}
	versions := ubuntuLTSVersions(index)
	if len(versions) == 0 {
		return Image{}, fmt.Errorf("%s: no LTS release found on %s", d.Name, d.IndexURL)
	}
	pick := Distro{ID: d.ID, Name: d.Name, Match: d.Match, Prefer: d.Prefer}
	var lastErr error
	for _, ver := range versions {
		dirURL := d.IndexURL + ver + "/" + subPath
		page, err := fetchBytes(ctx, c, dirURL)
		if err != nil {
			lastErr = err
			continue
		}
		t, err := selectBest(pick, parseCandidates(dirURL, page))
		if err != nil {
			lastErr = fmt.Errorf("%s %s: %w", d.Name, ver, err)
			continue
		}
		return t, nil
	}
	return Image{}, lastErr
}

// --- NixOS: resolve from the latest GitHub release's torrent assets ---

func resolveNixOS(ctx context.Context, c *http.Client, d Distro) (Image, error) {
	body, err := fetchBytes(ctx, c, d.IndexURL)
	if err != nil {
		return Image{}, err
	}
	cands, err := parseGitHubReleaseTorrents(body)
	if err != nil {
		return Image{}, fmt.Errorf("%s: %w", d.Name, err)
	}
	t, err := selectBest(d, cands)
	if err != nil {
		return Image{}, fmt.Errorf("%s: %w", d.Name, err)
	}
	return t, nil
}

// parseGitHubReleaseTorrents pulls the .torrent assets out of a GitHub
// "release" JSON payload as candidates.
func parseGitHubReleaseTorrents(body []byte) ([]candidate, error) {
	var rel struct {
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("parse release json: %w", err)
	}
	var out []candidate
	for _, a := range rel.Assets {
		if strings.HasSuffix(strings.ToLower(a.Name), ".torrent") {
			out = append(out, candidate{name: a.Name, url: a.URL})
		}
	}
	return out, nil
}

// --- CachyOS: torrent URLs live in an embedded JSON blob, not in hrefs ---

var reTorrentURL = regexp.MustCompile(`(?i)https?://[^\s"'<>&]+\.torrent`)

func resolveCachy(ctx context.Context, c *http.Client, d Distro) (Image, error) {
	body, err := fetchBytes(ctx, c, d.IndexURL)
	if err != nil {
		return Image{}, err
	}
	var cands []candidate
	seen := make(map[string]bool)
	for _, u := range reTorrentURL.FindAllString(string(body), -1) {
		if seen[u] {
			continue
		}
		seen[u] = true
		cands = append(cands, candidate{name: fileName(u), url: u})
	}
	t, err := selectBest(d, cands)
	if err != nil {
		return Image{}, fmt.Errorf("%s: %w", d.Name, err)
	}
	return t, nil
}

// --- direct (no-torrent) distros: scrape .iso links, verify via .sha256 ---

var reISOHref = regexp.MustCompile(`(?i)href\s*=\s*["']([^"'\s]+\.iso)["']`)

// parseISOCandidates extracts every raw .iso link from an autoindex page,
// resolving relative hrefs against base.
func parseISOCandidates(base string, body []byte) []candidate {
	baseURL, _ := url.Parse(base)
	var out []candidate
	seen := make(map[string]bool)
	for _, m := range reISOHref.FindAllSubmatch(body, -1) {
		href := html.UnescapeString(string(m[1]))
		abs := href
		if baseURL != nil {
			if ref, err := url.Parse(href); err == nil {
				abs = baseURL.ResolveReference(ref).String()
			}
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		out = append(out, candidate{name: fileName(abs), url: abs})
	}
	return out
}

// resolveDirect picks the newest matching .iso from the index page, then
// looks for its published checksum next to it. The checksum is best-effort:
// the download stays useful (over https, from the official mirror) even if
// the sibling .sha256 file ever disappears.
func resolveDirect(ctx context.Context, c *http.Client, d Distro) (Image, error) {
	// A fixed alias: IndexURL already points straight at the .iso (e.g.
	// Bazzite's stable link), so there is no listing to scrape.
	if strings.HasSuffix(strings.ToLower(d.IndexURL), ".iso") {
		img := Image{DistroID: d.ID, Title: fileName(d.IndexURL), DirectURL: d.IndexURL}
		img.SHA256 = resolveDirectSum(ctx, c, d, img.DirectURL)
		return img, nil
	}
	body, err := fetchBytes(ctx, c, d.IndexURL)
	if err != nil {
		return Image{}, err
	}
	img, err := selectBest(d, parseISOCandidates(d.IndexURL, body))
	if err != nil {
		return Image{}, fmt.Errorf("%s: %w", d.Name, err)
	}
	img.DirectURL, img.URL = img.URL, ""
	img.SHA256 = resolveDirectSum(ctx, c, d, img.DirectURL)
	return img, nil
}

// resolveDirectSum best-effort resolves a Direct image's checksum: a shared
// SumFile listing (matched by filename) when configured, otherwise the sibling
// <iso><SumSuffix> file. Returns "" if none is found - the https download from
// the official mirror stays useful regardless.
func resolveDirectSum(ctx context.Context, c *http.Client, d Distro, isoURL string) string {
	if d.SumFile != "" {
		base := isoURL[:strings.LastIndexByte(isoURL, '/')+1]
		if body, err := fetchBytes(ctx, c, base+d.SumFile); err == nil {
			if sum := parseSHA256For(body, fileName(isoURL)); sum != "" {
				return sum
			}
		}
	}
	suffix := d.SumSuffix
	if suffix == "" {
		suffix = ".sha256"
	}
	if sum, _, err := fetchSHA256(ctx, c, isoURL+suffix); err == nil {
		return sum
	}
	return ""
}

// resolveOpenSUSE resolves Tumbleweed's stable "Current" DVD alias into the
// concrete snapshot behind it: the alias's .sha256 file names the real
// versioned image, which pins the download against a mid-transfer snapshot
// rotation (and provides the digest).
func resolveOpenSUSE(ctx context.Context, c *http.Client, d Distro) (Image, error) {
	sum, name, err := fetchSHA256(ctx, c, d.IndexURL+".sha256")
	if err != nil {
		return Image{}, fmt.Errorf("%s: %w", d.Name, err)
	}
	img := Image{DistroID: d.ID, Title: fileName(d.IndexURL), DirectURL: d.IndexURL, SHA256: sum}
	if name != "" {
		base := d.IndexURL[:strings.LastIndexByte(d.IndexURL, '/')+1]
		img.Title = name
		img.DirectURL = base + url.PathEscape(name)
	}
	return img, nil
}

// resolvePopOS resolves Pop!_OS from its build API, which returns the current
// direct-download URL and sha256 for the requested channel.
func resolvePopOS(ctx context.Context, c *http.Client, d Distro) (Image, error) {
	body, err := fetchBytes(ctx, c, d.IndexURL)
	if err != nil {
		return Image{}, err
	}
	var b struct {
		URL    string `json:"url"`
		SHASum string `json:"sha_sum"`
	}
	if err := json.Unmarshal(body, &b); err != nil {
		return Image{}, fmt.Errorf("%s: parse build json: %w", d.Name, err)
	}
	if b.URL == "" {
		return Image{}, fmt.Errorf("%s: build api returned no url", d.Name)
	}
	return Image{DistroID: d.ID, Title: fileName(b.URL), DirectURL: b.URL, SHA256: strings.ToLower(b.SHASum)}, nil
}

// sourceForgeRSS is the small part of SourceForge's files RSS feed that tork
// needs. Links are direct "/download" URLs for files in the requested folder.
type sourceForgeRSS struct {
	Items []struct {
		Title string `xml:"title"`
		Link  string `xml:"link"`
	} `xml:"channel>item"`
}

func resolveSourceForgeRSS(ctx context.Context, c *http.Client, d Distro) (Image, error) {
	body, err := fetchSourceForgeBytes(ctx, c, d.IndexURL)
	if err != nil {
		return Image{}, err
	}
	cands, err := parseSourceForgeRSSCandidates(body)
	if err != nil {
		return Image{}, fmt.Errorf("%s: %w", d.Name, err)
	}
	img, err := selectBest(d, cands)
	if err != nil {
		return Image{}, fmt.Errorf("%s: %w", d.Name, err)
	}
	img.DirectURL, img.URL = img.URL, ""
	sumURL := strings.TrimSuffix(img.DirectURL, "/download") + ".sha256/download"
	if sum, _, err := fetchSourceForgeSHA256(ctx, c, sumURL); err == nil {
		img.SHA256 = sum
	}
	return img, nil
}

func fetchSourceForgeSHA256(ctx context.Context, c *http.Client, rawURL string) (sum, name string, err error) {
	body, err := fetchSourceForgeBytes(ctx, c, rawURL)
	if err != nil {
		return "", "", err
	}
	sum, name = parseSHA256File(body)
	if sum == "" {
		return "", "", fmt.Errorf("%s: no sha256 digest found", rawURL)
	}
	return sum, name, nil
}

func parseSourceForgeRSSCandidates(body []byte) ([]candidate, error) {
	var feed sourceForgeRSS
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("parse sourceforge rss: %w", err)
	}
	var out []candidate
	seen := map[string]bool{}
	for _, item := range feed.Items {
		link := strings.TrimSpace(item.Link)
		if link == "" || seen[link] {
			continue
		}
		isoURL := strings.TrimSuffix(link, "/download")
		name := fileName(isoURL)
		if !strings.HasSuffix(strings.ToLower(name), ".iso") {
			continue
		}
		seen[link] = true
		out = append(out, candidate{name: name, url: link})
	}
	return out, nil
}

// fetchSHA256 downloads a checksum file and returns the first digest/filename
// pair found in it.
func fetchSHA256(ctx context.Context, c *http.Client, rawURL string) (sum, name string, err error) {
	body, err := fetchBytes(ctx, c, rawURL)
	if err != nil {
		return "", "", err
	}
	sum, name = parseSHA256File(body)
	if sum == "" {
		return "", "", fmt.Errorf("%s: no sha256 digest found", rawURL)
	}
	return sum, name, nil
}

var reHex64 = regexp.MustCompile(`(?i)^[0-9a-f]{64}$`)
var reBSDSHA256 = regexp.MustCompile(`(?i)^SHA256 \((.+)\) = ([0-9a-f]{64})$`)

// parseSHA256File finds the first "<hex digest> <filename>" line in a
// checksum file. Handles plain coreutils output as well as PGP-clearsigned
// files like Gentoo's (armor lines are base64 and never look like a 64-char
// lowercase-hex token).
func parseSHA256File(body []byte) (sum, name string) {
	for _, line := range strings.Split(string(body), "\n") {
		if sum, name, ok := parseSHA256Line(line); ok {
			return sum, name
		}
	}
	return "", ""
}

func parseSHA256Line(line string) (sum, name string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	if m := reBSDSHA256.FindStringSubmatch(line); m != nil {
		return strings.ToLower(m[2]), m[1], true
	}
	fields := strings.Fields(line)
	if len(fields) == 0 || !reHex64.MatchString(fields[0]) {
		return "", "", false
	}
	sum = strings.ToLower(fields[0])
	if len(fields) > 1 {
		name = strings.TrimPrefix(fields[1], "*") // coreutils binary-mode marker
	}
	return sum, name, true
}

// parseSHA256For returns the digest for a specific filename from a multi-image
// checksum file (e.g. Void's sha256sum.txt), matching either coreutils
// "<hex>  <name>" lines or BSD "SHA256 (<name>) = <hex>" lines.
func parseSHA256For(body []byte, name string) string {
	name = strings.TrimSpace(name)
	for _, line := range strings.Split(string(body), "\n") {
		sum, lineName, ok := parseSHA256Line(line)
		if ok && lineName == name {
			return sum
		}
	}
	return ""
}

var reUbuntuDir = regexp.MustCompile(`(?i)href\s*=\s*["'](\d{2}\.\d{2})/["']`)

// ubuntuLTSVersions scans a releases index for LTS version directories (an
// even year with an .04 minor) and returns them newest first, e.g.
// ["24.04", "22.04"].
func ubuntuLTSVersions(body []byte) []string {
	var vers []string
	seen := map[string]bool{}
	for _, m := range reUbuntuDir.FindAllSubmatch(body, -1) {
		ver := string(m[1])
		parts := parseVersion(ver)
		if len(parts) != 2 || parts[1] != 4 || parts[0]%2 != 0 {
			continue // LTS = even year, April (.04) release
		}
		if seen[ver] {
			continue
		}
		seen[ver] = true
		vers = append(vers, ver)
	}
	sort.SliceStable(vers, func(i, j int) bool {
		return compareVersions(parseVersion(vers[i]), parseVersion(vers[j])) > 0
	})
	return vers
}

// latestUbuntuLTS returns the newest LTS version directory, or "".
func latestUbuntuLTS(body []byte) string {
	if v := ubuntuLTSVersions(body); len(v) > 0 {
		return v[0]
	}
	return ""
}
