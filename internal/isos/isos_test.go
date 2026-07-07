package isos

import "testing"

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

func TestCatalogWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range Catalog() {
		if d.ID == "" || d.Name == "" || d.IndexURL == "" {
			t.Errorf("incomplete entry: %+v", d)
		}
		if seen[d.ID] {
			t.Errorf("duplicate id %q", d.ID)
		}
		seen[d.ID] = true
		if d.resolve == nil && len(d.Match) == 0 {
			t.Errorf("%s: generic entry needs Match tokens", d.ID)
		}
	}
}
