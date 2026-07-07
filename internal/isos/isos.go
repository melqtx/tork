// Package isos is a curated catalog of major Linux distributions whose
// official images are published as torrents. Each entry knows where the
// project publishes its current torrent, so tork resolves the latest release
// live at selection time rather than shipping magnet links that rot on every
// point release.
//
// Everything here downloads over BitTorrent from official mirrors, so it stays
// squarely within tork's lawful-content charter - and users seed the image
// back to the distro afterward.
package isos

import (
	"context"
	"encoding/json"
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

	// Server groups headless/homelab images under a divider in the UI.
	Server bool

	// resolve overrides the generic page scraper for distros that need custom
	// navigation (e.g. Ubuntu's per-version directories). nil = generic.
	resolve func(ctx context.Context, c *http.Client, d Distro) (Torrent, error)
}

// Torrent is a resolved, ready-to-add image: exactly one of URL (a .torrent
// file) or Magnet is set.
type Torrent struct {
	DistroID string
	Title    string // resolved image name, e.g. "debian-12.7.0-amd64-netinst.iso"
	URL      string // .torrent file URL
	Magnet   string // magnet URI
}

// Catalog returns the built-in distro list, in display order.
func Catalog() []Distro {
	return []Distro{
		{
			ID: "ubuntu", Name: "Ubuntu", Edition: "desktop · amd64 · LTS",
			Blurb:    "the friendly default; latest long-term-support desktop",
			Homepage: "https://ubuntu.com",
			IndexURL: "https://releases.ubuntu.com/",
			Match:    []string{".torrent", "desktop", "amd64"},
			resolve:  resolveUbuntu,
		},
		{
			ID: "debian", Name: "Debian", Edition: "netinst · amd64",
			Blurb:    "the universal OS; stable netinst image",
			Homepage: "https://www.debian.org",
			IndexURL: "https://cdimage.debian.org/debian-cd/current/amd64/bt-cd/",
			Match:    []string{".torrent", "amd64", "netinst"},
		},
		{
			ID: "fedora", Name: "Fedora", Edition: "workstation · x86_64",
			Blurb:    "leading-edge, upstream-first; Workstation live",
			Homepage: "https://fedoraproject.org",
			IndexURL: "https://torrent.fedoraproject.org/torrents/",
			Match:    []string{".torrent", "workstation", "x86_64"},
			Prefer:   []string{"live"},
		},
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
		{
			ID: "mint", Name: "Linux Mint", Edition: "cinnamon · 64-bit",
			Blurb:    "cozy and familiar; Cinnamon edition",
			Homepage: "https://linuxmint.com",
			IndexURL: "https://torrents.linuxmint.com/",
			Match:    []string{".torrent", "cinnamon", "64bit"},
		},
		{
			ID: "kali", Name: "Kali Linux", Edition: "installer · amd64",
			Blurb:    "the security toolbox; installer image",
			Homepage: "https://www.kali.org",
			IndexURL: "https://cdimage.kali.org/current/",
			Match:    []string{".torrent", "installer", "amd64"},
		},
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

		// --- servers & homelab ---
		{
			ID: "ubuntu-server", Name: "Ubuntu Server", Edition: "live-server · amd64 · LTS",
			Blurb:    "the cloud & homelab workhorse; headless LTS installer",
			Homepage: "https://ubuntu.com/server",
			IndexURL: "https://releases.ubuntu.com/",
			Match:    []string{".torrent", "live-server", "amd64"},
			Server:   true,
			resolve:  resolveUbuntu,
		},
		{
			ID: "fedora-server", Name: "Fedora Server", Edition: "dvd · x86_64",
			Blurb:    "Fedora for servers; the network-install DVD",
			Homepage: "https://fedoraproject.org/server/",
			IndexURL: "https://torrent.fedoraproject.org/torrents/",
			Match:    []string{".torrent", "server", "x86_64"},
			Server:   true,
		},
		{
			ID: "rocky", Name: "Rocky Linux", Edition: "minimal · x86_64",
			Blurb:    "enterprise, RHEL-compatible; minimal image",
			Homepage: "https://rockylinux.org",
			IndexURL: "https://download.rockylinux.org/pub/rocky/10/isos/x86_64/",
			Match:    []string{".torrent", "x86_64", "minimal"},
			Server:   true,
		},
		{
			ID: "almalinux", Name: "AlmaLinux", Edition: "x86_64",
			Blurb:    "enterprise, RHEL-compatible; installer image",
			Homepage: "https://almalinux.org",
			IndexURL: "https://repo.almalinux.org/almalinux/10/isos/x86_64/",
			Match:    []string{".torrent", "x86_64"},
			Prefer:   []string{"dvd", "minimal"},
			Server:   true,
		},
		{
			ID: "proxmox", Name: "Proxmox VE", Edition: "installer · amd64",
			Blurb:    "the virtualization platform; bare-metal installer",
			Homepage: "https://www.proxmox.com",
			IndexURL: "https://enterprise.proxmox.com/iso/",
			Match:    []string{".torrent", "proxmox-ve_"},
			Server:   true,
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

// Resolve fetches the distro's current official torrent.
func Resolve(ctx context.Context, d Distro) (Torrent, error) {
	if d.resolve != nil {
		return d.resolve(ctx, DefaultClient, d)
	}
	return resolveGeneric(ctx, DefaultClient, d)
}

func resolveGeneric(ctx context.Context, c *http.Client, d Distro) (Torrent, error) {
	body, err := fetchBytes(ctx, c, d.IndexURL)
	if err != nil {
		return Torrent{}, err
	}
	t, err := selectBest(d, parseCandidates(d.IndexURL, body))
	if err != nil {
		return Torrent{}, fmt.Errorf("%s: %w", d.Name, err)
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
func selectBest(d Distro, cands []candidate) (Torrent, error) {
	var kept []candidate
	for _, c := range cands {
		if matchesAll(c, d.Match) {
			kept = append(kept, c)
		}
	}
	if len(kept) == 0 {
		return Torrent{}, errors.New("no matching torrent on the official page (mirror layout may have changed)")
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if v := compareVersions(candVersion(kept[i]), candVersion(kept[j])); v != 0 {
			return v > 0 // newer first
		}
		return preferRank(kept[i], d.Prefer) < preferRank(kept[j], d.Prefer)
	})
	best := kept[0]
	return Torrent{DistroID: d.ID, Title: best.name, URL: best.url, Magnet: best.magnet}, nil
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

// --- Ubuntu: two-step resolver (pick latest LTS dir, then the desktop image) ---

func resolveUbuntu(ctx context.Context, c *http.Client, d Distro) (Torrent, error) {
	index, err := fetchBytes(ctx, c, d.IndexURL)
	if err != nil {
		return Torrent{}, err
	}
	ver := latestUbuntuLTS(index)
	if ver == "" {
		return Torrent{}, fmt.Errorf("%s: no LTS release found on %s", d.Name, d.IndexURL)
	}
	dirURL := d.IndexURL + ver + "/"
	page, err := fetchBytes(ctx, c, dirURL)
	if err != nil {
		return Torrent{}, err
	}
	pick := Distro{ID: d.ID, Name: d.Name, Match: d.Match, Prefer: d.Prefer}
	t, err := selectBest(pick, parseCandidates(dirURL, page))
	if err != nil {
		return Torrent{}, fmt.Errorf("%s %s: %w", d.Name, ver, err)
	}
	return t, nil
}

// --- NixOS: resolve from the latest GitHub release's torrent assets ---

func resolveNixOS(ctx context.Context, c *http.Client, d Distro) (Torrent, error) {
	body, err := fetchBytes(ctx, c, d.IndexURL)
	if err != nil {
		return Torrent{}, err
	}
	cands, err := parseGitHubReleaseTorrents(body)
	if err != nil {
		return Torrent{}, fmt.Errorf("%s: %w", d.Name, err)
	}
	t, err := selectBest(d, cands)
	if err != nil {
		return Torrent{}, fmt.Errorf("%s: %w", d.Name, err)
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

func resolveCachy(ctx context.Context, c *http.Client, d Distro) (Torrent, error) {
	body, err := fetchBytes(ctx, c, d.IndexURL)
	if err != nil {
		return Torrent{}, err
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
		return Torrent{}, fmt.Errorf("%s: %w", d.Name, err)
	}
	return t, nil
}

var reUbuntuDir = regexp.MustCompile(`(?i)href\s*=\s*["'](\d{2}\.\d{2})/["']`)

// latestUbuntuLTS scans the releases index for LTS version directories (an
// even year with an .04 minor) and returns the newest, e.g. "24.04".
func latestUbuntuLTS(body []byte) string {
	best := ""
	var bestVer []int
	for _, m := range reUbuntuDir.FindAllSubmatch(body, -1) {
		ver := string(m[1])
		parts := parseVersion(ver)
		if len(parts) != 2 || parts[1] != 4 || parts[0]%2 != 0 {
			continue // LTS = even year, April (.04) release
		}
		if best == "" || compareVersions(parts, bestVer) > 0 {
			best, bestVer = ver, parts
		}
	}
	return best
}
