package isos

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// a Debian-style Apache autoindex listing.
const debianIndex = `<html><head><title>Index of /debian-cd/current/amd64/bt-cd</title></head>
<body><h1>Index of /debian-cd/current/amd64/bt-cd</h1>
<table>
<tr><td><a href="../">Parent Directory</a></td></tr>
<tr><td><a href="debian-12.7.0-amd64-netinst.iso.torrent">debian-12.7.0-amd64-netinst.iso.torrent</a></td></tr>
<tr><td><a href="debian-edu-12.7.0-amd64-netinst.iso.torrent">debian-edu-12.7.0-amd64-netinst.iso.torrent</a></td></tr>
<tr><td><a href="debian-mac-12.7.0-amd64-netinst.iso.torrent">debian-mac-12.7.0-amd64-netinst.iso.torrent</a></td></tr>
<tr><td><a href="SHA256SUMS">SHA256SUMS</a></td></tr>
</table></body></html>`

func TestResolveDebianAutoindex(t *testing.T) {
	d := Distro{ID: "debian", Match: []string{".torrent", "amd64", "netinst"}}
	got, err := selectBest(d, parseCandidates("https://cdimage.debian.org/debian-cd/current/amd64/bt-cd/", []byte(debianIndex)))
	if err != nil {
		t.Fatal(err)
	}
	want := "https://cdimage.debian.org/debian-cd/current/amd64/bt-cd/debian-12.7.0-amd64-netinst.iso.torrent"
	if got.URL != want {
		t.Errorf("URL = %q, want %q", got.URL, want)
	}
	if got.Magnet != "" {
		t.Errorf("expected a .torrent URL, got magnet %q", got.Magnet)
	}
	if got.Title != "debian-12.7.0-amd64-netinst.iso.torrent" {
		t.Errorf("Title = %q", got.Title)
	}
}

// Mint lists every release ever; the newest version must win.
const mintIndex = `<html><body>
<a href="linuxmint-21.3-cinnamon-64bit.iso.torrent">linuxmint-21.3-cinnamon-64bit</a>
<a href="linuxmint-22-cinnamon-64bit.iso.torrent">linuxmint-22-cinnamon-64bit</a>
<a href="linuxmint-22.1-cinnamon-64bit.iso.torrent">linuxmint-22.1-cinnamon-64bit</a>
<a href="linuxmint-22.1-mate-64bit.iso.torrent">linuxmint-22.1-mate-64bit</a>
<a href="linuxmint-22.1-xfce-64bit.iso.torrent">linuxmint-22.1-xfce-64bit</a>
</body></html>`

func TestResolveMintPicksNewestVersion(t *testing.T) {
	d := Distro{ID: "mint", Match: []string{".torrent", "cinnamon", "64bit"}}
	got, err := selectBest(d, parseCandidates("https://torrents.linuxmint.com/", []byte(mintIndex)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "linuxmint-22.1-cinnamon-64bit.iso.torrent" {
		t.Errorf("Title = %q, want the newest cinnamon build", got.Title)
	}
}

// Arch publishes a magnet (and a torrent link without a .torrent suffix) on
// its download page; the magnet must be selected.
const archPage = `<html><body>
<a href="/releng/releases/2024.12.01/torrent/">Torrent</a>
<a href="magnet:?xt=urn:btih:abc123&dn=archlinux-2024.12.01-x86_64.iso&tr=udp%3A%2F%2Ftracker.archlinux.org%3A6969">Magnet</a>
</body></html>`

func TestResolveArchPrefersMagnet(t *testing.T) {
	d := Distro{ID: "archlinux", Match: []string{"archlinux", "x86_64"}}
	got, err := selectBest(d, parseCandidates("https://archlinux.org/download/", []byte(archPage)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Magnet == "" {
		t.Fatalf("expected a magnet, got URL %q", got.URL)
	}
	if got.Title != "archlinux-2024.12.01-x86_64.iso" {
		t.Errorf("Title = %q", got.Title)
	}
}

func TestSelectBestNoMatch(t *testing.T) {
	d := Distro{ID: "x", Match: []string{"nonexistent"}}
	if _, err := selectBest(d, parseCandidates("https://x/", []byte(debianIndex))); err == nil {
		t.Fatal("expected an error when nothing matches")
	}
}

const ubuntuIndex = `<html><body>
<a href="20.04/">Ubuntu 20.04 LTS</a>
<a href="22.04/">Ubuntu 22.04 LTS</a>
<a href="23.10/">Ubuntu 23.10</a>
<a href="24.04/">Ubuntu 24.04 LTS</a>
<a href="25.04/">Ubuntu 25.04</a>
</body></html>`

func TestLatestUbuntuLTS(t *testing.T) {
	if got := latestUbuntuLTS([]byte(ubuntuIndex)); got != "24.04" {
		t.Errorf("latestUbuntuLTS = %q, want 24.04 (newest even-year .04)", got)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b []int
		want int
	}{
		{[]int{22, 1}, []int{22}, 1},
		{[]int{12, 7, 0}, []int{12, 7, 0}, 0},
		{[]int{21, 3}, []int{22}, -1},
		{[]int{2024, 12, 1}, []int{2024, 11, 1}, 1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%v,%v) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// a trimmed GitHub "latest release" payload, mirroring the real asset names.
const nixosRelease = `{
  "tag_name": "v26.05.4193.a50de1b7d8a5",
  "assets": [
    {"name": "nixos-graphical-26.05.4193.a50de1b7d8a5-aarch64-linux.iso.torrent",
     "browser_download_url": "https://github.com/AnimMouse/NixOS-ISO-Torrents/releases/download/v26.05/nixos-graphical-26.05.4193.a50de1b7d8a5-aarch64-linux.iso.torrent"},
    {"name": "nixos-graphical-26.05.4193.a50de1b7d8a5-x86_64-linux.iso.torrent",
     "browser_download_url": "https://github.com/AnimMouse/NixOS-ISO-Torrents/releases/download/v26.05/nixos-graphical-26.05.4193.a50de1b7d8a5-x86_64-linux.iso.torrent"},
    {"name": "nixos-minimal-26.05.4193.a50de1b7d8a5-x86_64-linux.iso.torrent",
     "browser_download_url": "https://github.com/AnimMouse/NixOS-ISO-Torrents/releases/download/v26.05/nixos-minimal-26.05.4193.a50de1b7d8a5-x86_64-linux.iso.torrent"},
    {"name": "SHA256SUMS", "browser_download_url": "https://github.com/x/SHA256SUMS"}
  ]
}`

func TestResolveNixOSGraphicalX86(t *testing.T) {
	cands, err := parseGitHubReleaseTorrents([]byte(nixosRelease))
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 3 {
		t.Fatalf("expected 3 .torrent assets, got %d", len(cands))
	}
	d := Distro{ID: "nixos", Match: []string{".torrent", "graphical", "x86_64"}}
	got, err := selectBest(d, cands)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "nixos-graphical-26.05.4193.a50de1b7d8a5-x86_64-linux.iso.torrent" {
		t.Errorf("Title = %q, want the graphical x86_64 build", got.Title)
	}
}

// Fedora's autoindex: version comes AFTER the arch, and both Workstation and
// Server are present. The arch "86"/"64" must not be read as the version.
const fedoraIndex = `<html><body>
<a href="Fedora-Workstation-Live-x86_64-43.torrent">ws43</a>
<a href="Fedora-Workstation-Live-x86_64-44.torrent">ws44</a>
<a href="Fedora-Server-dvd-x86_64-43.torrent">srv43</a>
<a href="Fedora-Server-dvd-x86_64-44.torrent">srv44</a>
<a href="Fedora-KDE-Live-x86_64-44.torrent">kde44</a>
</body></html>`

func TestResolveFedoraWorkstationNewest(t *testing.T) {
	base := "https://torrent.fedoraproject.org/torrents/"
	cands := parseCandidates(base, []byte(fedoraIndex))
	ws := Distro{ID: "fedora", Match: []string{".torrent", "workstation", "x86_64"}, Prefer: []string{"live"}}
	got, err := selectBest(ws, cands)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Fedora-Workstation-Live-x86_64-44.torrent" {
		t.Errorf("workstation Title = %q, want v44 (arch must not parse as version)", got.Title)
	}
	srv := Distro{ID: "fedora-server", Match: []string{".torrent", "server", "x86_64"}}
	gotSrv, err := selectBest(srv, cands)
	if err != nil {
		t.Fatal(err)
	}
	if gotSrv.Title != "Fedora-Server-dvd-x86_64-44.torrent" {
		t.Errorf("server Title = %q, want v44", gotSrv.Title)
	}
}

func TestParseVersionIgnoresArch(t *testing.T) {
	got := parseVersion("Fedora-Server-dvd-x86_64-44.torrent")
	if len(got) != 1 || got[0] != 44 {
		t.Errorf("parseVersion = %v, want [44]", got)
	}
}

// Proxmox's ISO page lists several products; VE must win over backup/mail, and
// the newest VE version must be chosen.
const proxmoxIndex = `<html><body>
<a href="./proxmox-ve_8.4-1.iso.torrent">ve84</a>
<a href="./proxmox-ve_9.1-1.iso.torrent">ve91</a>
<a href="./proxmox-ve_9.2-1.iso.torrent">ve92</a>
<a href="./proxmox-backup-server_4.2-1.iso.torrent">pbs</a>
<a href="./proxmox-mail-gateway_9.1-1.iso.torrent">pmg</a>
</body></html>`

func TestResolveProxmoxVE(t *testing.T) {
	d := Distro{ID: "proxmox", Match: []string{".torrent", "proxmox-ve_"}}
	got, err := selectBest(d, parseCandidates("https://enterprise.proxmox.com/iso/", []byte(proxmoxIndex)))
	if err != nil {
		t.Fatal(err)
	}
	want := "https://enterprise.proxmox.com/iso/proxmox-ve_9.2-1.iso.torrent"
	if got.URL != want {
		t.Errorf("URL = %q, want %q", got.URL, want)
	}
}

// Ubuntu's per-version dir carries both desktop and live-server torrents.
const ubuntuDir = `<html><body>
<a href="ubuntu-24.04.4-desktop-amd64.iso.torrent">desktop</a>
<a href="ubuntu-24.04.4-live-server-amd64.iso.torrent">server</a>
</body></html>`

func TestUbuntuServerVsDesktopSelection(t *testing.T) {
	base := "https://releases.ubuntu.com/24.04/"
	cands := parseCandidates(base, []byte(ubuntuDir))
	server := Distro{ID: "ubuntu-server", Match: []string{".torrent", "live-server", "amd64"}}
	got, err := selectBest(server, cands)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "ubuntu-24.04.4-live-server-amd64.iso.torrent" {
		t.Errorf("Title = %q, want the live-server image", got.Title)
	}
	// "desktop" Match must not accidentally grab the server image
	desktop := Distro{ID: "ubuntu", Match: []string{".torrent", "desktop", "amd64"}}
	gotD, _ := selectBest(desktop, cands)
	if gotD.Title != "ubuntu-24.04.4-desktop-amd64.iso.torrent" {
		t.Errorf("desktop Title = %q", gotD.Title)
	}
}

// Fedora's index carries both the KDE Desktop edition and the KDE Mobile spin;
// the kde-desktop token must exclude Mobile.
const fedoraKDEIndex = `<html><body>
<a href="Fedora-KDE-Desktop-Live-x86_64-43.torrent">kde43</a>
<a href="Fedora-KDE-Desktop-Live-x86_64-44.torrent">kde44</a>
<a href="Fedora-KDE-Mobile-Live-x86_64-44.torrent">mobile44</a>
</body></html>`

func TestResolveFedoraKDEExcludesMobile(t *testing.T) {
	d := Distro{ID: "fedora-kde", Match: []string{".torrent", "kde-desktop", "x86_64"}}
	got, err := selectBest(d, parseCandidates("https://torrent.fedoraproject.org/torrents/", []byte(fedoraKDEIndex)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Fedora-KDE-Desktop-Live-x86_64-44.torrent" {
		t.Errorf("Title = %q, want the newest KDE Desktop build", got.Title)
	}
}

// Qubes versions carry an R prefix ("R4.3.1"); the dotted run must still parse.
const qubesPage = `<html><body>
<a href="https://mirrors.edge.kernel.org/qubes/iso/Qubes-R4.2.4-x86_64.torrent">old</a>
<a href="https://mirrors.edge.kernel.org/qubes/iso/Qubes-R4.3.1-x86_64.torrent">new</a>
</body></html>`

func TestResolveQubesNewest(t *testing.T) {
	d := Distro{ID: "qubes", Match: []string{".torrent", "qubes", "x86_64"}}
	got, err := selectBest(d, parseCandidates("https://www.qubes-os.org/downloads/", []byte(qubesPage)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Qubes-R4.3.1-x86_64.torrent" {
		t.Errorf("Title = %q, want R4.3.1", got.Title)
	}
}

// elementary publishes magnets only; amd64 must win over arm64.
const elementaryPage = `<html><body>
<a href="magnet:?xt=urn:btih:aaa&amp;dn=elementaryos-8.1-stable-arm64.20260219.iso&amp;tr=x">arm</a>
<a href="magnet:?xt=urn:btih:bbb&amp;dn=elementaryos-8.1-stable-amd64.20260219.iso&amp;tr=x">amd</a>
</body></html>`

func TestResolveElementaryMagnetAmd64(t *testing.T) {
	d := Distro{ID: "elementary", Match: []string{"magnet", "elementaryos", "amd64"}}
	got, err := selectBest(d, parseCandidates("https://elementary.io/", []byte(elementaryPage)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Magnet == "" {
		t.Fatalf("expected a magnet, got URL %q", got.URL)
	}
	if got.Title != "elementaryos-8.1-stable-amd64.20260219.iso" {
		t.Errorf("Title = %q", got.Title)
	}
}

// The Raspberry Pi OS page lists standard/full/lite plus oldstable variants;
// the "raspios_arm64/" path token must pin the standard 64-bit desktop image.
const raspiosPage = `<html><body>
<a href="https://downloads.raspberrypi.com/raspios_arm64/images/raspios_arm64-2026-06-19/2026-06-18-raspios-trixie-arm64.img.xz.torrent">std</a>
<a href="https://downloads.raspberrypi.com/raspios_full_arm64/images/raspios_full_arm64-2026-06-19/2026-06-18-raspios-trixie-arm64-full.img.xz.torrent">full</a>
<a href="https://downloads.raspberrypi.com/raspios_lite_arm64/images/raspios_lite_arm64-2026-06-19/2026-06-18-raspios-trixie-arm64-lite.img.xz.torrent">lite</a>
<a href="https://downloads.raspberrypi.com/raspios_oldstable_arm64/images/raspios_oldstable_arm64-2026-06-19/2026-06-18-raspios-bookworm-arm64.img.xz.torrent">oldstable</a>
</body></html>`

func TestResolveRaspiosStandardOnly(t *testing.T) {
	d := Distro{ID: "raspios", Match: []string{".torrent", "raspios_arm64/"}}
	got, err := selectBest(d, parseCandidates("https://www.raspberrypi.com/software/operating-systems/", []byte(raspiosPage)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "2026-06-18-raspios-trixie-arm64.img.xz.torrent" {
		t.Errorf("Title = %q, want the standard arm64 image", got.Title)
	}
}

func TestCatalogWellFormed(t *testing.T) {
	seen := map[string]bool{}
	if got := categoryOrder; !slices.Equal(got, []string{"desktop", "servers"}) {
		t.Fatalf("categoryOrder = %v, want [desktop servers]", got)
	}
	validCat := map[string]bool{}
	for _, c := range categoryOrder {
		validCat[c] = true
	}
	for _, d := range Catalog() {
		if d.ID == "" || d.Name == "" || d.IndexURL == "" {
			t.Errorf("incomplete entry: %+v", d)
		}
		if seen[d.ID] {
			t.Errorf("duplicate id %q", d.ID)
		}
		seen[d.ID] = true
		if d.resolve == nil && !d.Direct && len(d.Match) == 0 {
			t.Errorf("%s: generic entry needs Match tokens", d.ID)
		}
		if d.Category != "" && !validCat[d.Category] {
			t.Errorf("%s: unknown category %q", d.ID, d.Category)
		}
	}
}

func TestCategoryOfNormalizesLegacyNames(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "desktop"},
		{"desktop", "desktop", "desktop"},
		{"legacy niche", "niche & advanced", "desktop"},
		{"servers", "servers", "servers"},
		{"legacy servers", "servers & homelab", "servers"},
		{"unknown", "weird", "desktop"},
	}
	for _, tc := range cases {
		if got := CategoryOf(Distro{Category: tc.in}); got != tc.want {
			t.Errorf("%s: CategoryOf(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// The catalog is grouped by category in display order so the shelf can insert
// dividers on change without reordering; a category must not reappear later.
func TestCatalogGroupedByCategory(t *testing.T) {
	rank := map[string]int{}
	for i, c := range categoryOrder {
		rank[c] = i
	}
	last, seen := -1, map[string]bool{}
	for _, d := range Catalog() {
		cat := CategoryOf(d)
		r, ok := rank[cat]
		if !ok {
			t.Fatalf("%s: category %q not in categoryOrder", d.ID, cat)
		}
		if r != last && seen[cat] {
			t.Errorf("category %q reappears out of order (entry %s)", cat, d.ID)
		}
		seen[cat] = true
		last = r
	}
}

// Kubuntu-style flavor: torrents live under <ver>/release/, and a pre-release
// LTS dir (26.04/) with no release/ must fall back to the previous LTS.
func TestResolveCdimageFlavorFallback(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/kubuntu/releases/", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<a href="22.04/">x</a><a href="24.04/">x</a><a href="26.04/">x</a>`))
	})
	// 26.04 exists but its release/ dir is empty (pre-release)
	mux.HandleFunc("/kubuntu/releases/26.04/release/", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<html>nothing yet</html>`))
	})
	mux.HandleFunc("/kubuntu/releases/24.04/release/", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<a href="kubuntu-24.04.4-desktop-amd64.iso.torrent">x</a>`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	d := Distro{ID: "kubuntu", Name: "Kubuntu", IndexURL: ts.URL + "/kubuntu/releases/",
		Match: []string{".torrent", "desktop", "amd64"}, resolve: resolveCdimageFlavor}
	got, err := Resolve(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "kubuntu-24.04.4-desktop-amd64.iso.torrent" {
		t.Errorf("Title = %q, want the 24.04 image (26.04 empty → fallback)", got.Title)
	}
}

func TestResolvePopOS(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/builds/24.04/intel", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"version":"24.04","url":"https://iso.pop-os.org/24.04/amd64/intel/20/pop-os_24.04_amd64_intel_20.iso","sha_sum":"A0EF3842AB710DB4","channel":"intel"}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	d := Distro{ID: "popos", Name: "Pop!_OS", IndexURL: ts.URL + "/builds/24.04/intel", resolve: resolvePopOS}
	got, err := Resolve(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if got.DirectURL != "https://iso.pop-os.org/24.04/amd64/intel/20/pop-os_24.04_amd64_intel_20.iso" {
		t.Errorf("DirectURL = %q", got.DirectURL)
	}
	if got.SHA256 != "a0ef3842ab710db4" { // lowercased
		t.Errorf("SHA256 = %q, want lowercased digest", got.SHA256)
	}
	if got.Title != "pop-os_24.04_amd64_intel_20.iso" {
		t.Errorf("Title = %q", got.Title)
	}
}

func TestResolveSourceForgeRSS(t *testing.T) {
	const sum = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	var base string
	mux := http.NewServeMux()
	mux.HandleFunc("/rss", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(`<?xml version="1.0"?>
<rss><channel>
  <item><title>/Final/Xfce/MX-25.2_Xfce_x64.iso.zsync</title><link>` + "http://example.test/Final/Xfce/MX-25.2_Xfce_x64.iso.zsync/download" + `</link></item>
  <item><title>/Final/Xfce/MX-25.2_Xfce_x64.iso.sig</title><link>` + "http://example.test/Final/Xfce/MX-25.2_Xfce_x64.iso.sig/download" + `</link></item>
  <item><title>/Final/Xfce/MX-25.2_Xfce_x64.iso.sha256</title><link>` + "http://example.test/Final/Xfce/MX-25.2_Xfce_x64.iso.sha256/download" + `</link></item>
  <item><title>/Final/Xfce/MX-25.2_Xfce_x64.iso</title><link>` + base + `/Final/Xfce/MX-25.2_Xfce_x64.iso/download</link></item>
  <item><title>/Final/Xfce/MX-25.2_Xfce_ahs_x64.iso</title><link>` + base + `/Final/Xfce/MX-25.2_Xfce_ahs_x64.iso/download</link></item>
  <item><title>/Final/Xfce/mx25.2_rpi_respin_arm64.zip</title><link>` + "http://example.test/Final/Xfce/mx25.2_rpi_respin_arm64.zip/download" + `</link></item>
</channel></rss>`))
	})
	mux.HandleFunc("/Final/Xfce/MX-25.2_Xfce_x64.iso.sha256/download", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(sum + "  MX-25.2_Xfce_x64.iso\n"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	base = ts.URL

	d := Distro{ID: "mxlinux", Name: "MX Linux", IndexURL: ts.URL + "/rss?path=/Final/Xfce",
		Match: []string{"mx-", "_xfce_", "x64", ".iso"}, Prefer: []string{"_xfce_x64.iso"}, resolve: resolveSourceForgeRSS}
	got, err := Resolve(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "MX-25.2_Xfce_x64.iso" {
		t.Errorf("Title = %q, want newest non-ahs x64 build", got.Title)
	}
	if got.DirectURL != ts.URL+"/Final/Xfce/MX-25.2_Xfce_x64.iso/download" {
		t.Errorf("DirectURL = %q", got.DirectURL)
	}
	if got.SHA256 != sum {
		t.Errorf("SHA256 = %q", got.SHA256)
	}
}

func TestParseSourceForgeRSSCandidatesExactISOOnly(t *testing.T) {
	body := []byte(`<?xml version="1.0"?>
<rss><channel>
  <item><title>/Final/Xfce/MX-25.2_Xfce_x64.iso.zsync</title><link>https://sourceforge.net/projects/mx-linux/files/Final/Xfce/MX-25.2_Xfce_x64.iso.zsync/download</link></item>
  <item><title>/Final/Xfce/MX-25.2_Xfce_x64.iso.sig</title><link>https://sourceforge.net/projects/mx-linux/files/Final/Xfce/MX-25.2_Xfce_x64.iso.sig/download</link></item>
  <item><title>/Final/Xfce/MX-25.2_Xfce_x64.iso.sha256</title><link>https://sourceforge.net/projects/mx-linux/files/Final/Xfce/MX-25.2_Xfce_x64.iso.sha256/download</link></item>
  <item><title>/Final/Xfce/MX-25.2_Xfce_x64.iso</title><link>https://sourceforge.net/projects/mx-linux/files/Final/Xfce/MX-25.2_Xfce_x64.iso/download</link></item>
  <item><title>/Final/Xfce/MX-25.2_Xfce_ahs_x64.iso</title><link>https://sourceforge.net/projects/mx-linux/files/Final/Xfce/MX-25.2_Xfce_ahs_x64.iso/download</link></item>
  <item><title>/Final/Xfce/mx25.2_rpi_respin_arm64.zip</title><link>https://sourceforge.net/projects/mx-linux/files/Final/Xfce/mx25.2_rpi_respin_arm64.zip/download</link></item>
</channel></rss>`)
	got, err := parseSourceForgeRSSCandidates(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2 ISO files: %#v", len(got), got)
	}
	if got[0].name != "MX-25.2_Xfce_x64.iso" || got[1].name != "MX-25.2_Xfce_ahs_x64.iso" {
		t.Errorf("candidates = %#v", got)
	}
}

// Void-style: a single sha256sum.txt covers every image in the directory.
func TestResolveDirectWithSumFile(t *testing.T) {
	const iso = "void-live-x86_64-20250202-xfce.iso"
	const sum = "1111111111111111111111111111111111111111111111111111111111111111"
	mux := http.NewServeMux()
	mux.HandleFunc("/live/current/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/live/current/sha256sum.txt" {
			w.Write([]byte("SHA256 (void-live-x86_64-20250202-base.iso) = 2222222222222222222222222222222222222222222222222222222222222222\n" +
				"SHA256 (" + iso + ") = " + sum + "\n"))
			return
		}
		w.Write([]byte(`<a href="void-live-x86_64-20250202-base.iso">base</a><a href="` + iso + `">xfce</a>`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	d := Distro{ID: "void", Name: "Void", Direct: true, SumFile: "sha256sum.txt",
		IndexURL: ts.URL + "/live/current/", Match: []string{"void-live-x86_64", "xfce", ".iso"}}
	got, err := Resolve(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != iso {
		t.Errorf("Title = %q, want %q", got.Title, iso)
	}
	if got.SHA256 != sum {
		t.Errorf("SHA256 = %q, want the xfce digest from the shared sumfile", got.SHA256)
	}
}

// Bazzite-style: the checksum is a "-CHECKSUM" sibling, not ".sha256".
func TestResolveDirectSumSuffix(t *testing.T) {
	const sum = "9c8d06cd8e57f2274678edeb14b4b13a79b8117c70571a65199919a66305b5c7"
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bazzite-stable-amd64.iso":
			w.WriteHeader(http.StatusOK)
		case "/bazzite-stable-amd64.iso-CHECKSUM":
			w.Write([]byte(sum + "  bazzite-stable-amd64.iso\n"))
		default:
			http.NotFound(w, r)
		}
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	d := Distro{ID: "bazzite", Name: "Bazzite", Direct: true, SumSuffix: "-CHECKSUM",
		IndexURL: ts.URL + "/bazzite-stable-amd64.iso"}
	got, err := Resolve(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if got.DirectURL != ts.URL+"/bazzite-stable-amd64.iso" {
		t.Errorf("DirectURL = %q", got.DirectURL)
	}
	if got.Title != "bazzite-stable-amd64.iso" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.SHA256 != sum {
		t.Errorf("SHA256 = %q, want the -CHECKSUM digest", got.SHA256)
	}
}

func TestParseSHA256For(t *testing.T) {
	body := []byte("aaaa  other.iso\n" +
		"2222222222222222222222222222222222222222222222222222222222222222  target.iso.sig\n" +
		"1111111111111111111111111111111111111111111111111111111111111111  target.iso\n" +
		"SHA256 (bsd.iso.sig) = 4444444444444444444444444444444444444444444444444444444444444444\n" +
		"SHA256 (bsd.iso) = 3333333333333333333333333333333333333333333333333333333333333333\n")
	if got := parseSHA256For(body, "target.iso"); got != "1111111111111111111111111111111111111111111111111111111111111111" {
		t.Errorf("coreutils line: got %q", got)
	}
	if got := parseSHA256For(body, "bsd.iso"); got != "3333333333333333333333333333333333333333333333333333333333333333" {
		t.Errorf("bsd line: got %q", got)
	}
	if got := parseSHA256For(body, "missing.iso"); got != "" {
		t.Errorf("missing: got %q, want empty", got)
	}
}

func TestUbuntuLTSVersionsNewestFirst(t *testing.T) {
	got := ubuntuLTSVersions([]byte(ubuntuIndex))
	want := []string{"24.04", "22.04", "20.04"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
		}
	}
}
