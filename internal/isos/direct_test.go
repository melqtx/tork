package isos

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// a Gentoo-style PGP-clearsigned checksum file: the armor is base64 and must
// not be mistaken for a digest.
const gentooSHA256 = `-----BEGIN PGP SIGNED MESSAGE-----
Hash: SHA256

# SHA256 HASH
54ad48e83d84ebab95f5d50fe5afe4426798a5aaceb252d1ca4812201aca8668  install-amd64-minimal-20260705T170105Z.iso
-----BEGIN PGP SIGNATURE-----

iQFPBAEBCAA5FiEEU05CCatJ7uHBnZYWLERpXbn2BD0FAmpKqfgbFIAAAAAABAAO
-----END PGP SIGNATURE-----
`

func TestParseSHA256File(t *testing.T) {
	cases := []struct {
		in        string
		sum, name string
	}{
		{gentooSHA256, "54ad48e83d84ebab95f5d50fe5afe4426798a5aaceb252d1ca4812201aca8668", "install-amd64-minimal-20260705T170105Z.iso"},
		{"4B46CCFBDA3627D39B7C5182346FA88C1AF68A4EE9D83C26734D9D7C014C25C9  openSUSE-Tumbleweed-DVD-x86_64-Snapshot20260703-Media.iso\n",
			"4b46ccfbda3627d39b7c5182346fa88c1af68a4ee9d83c26734d9d7c014c25c9", "openSUSE-Tumbleweed-DVD-x86_64-Snapshot20260703-Media.iso"},
		{"abc123  file.iso\n", "", ""}, // too short to be a digest
		{"", "", ""},
	}
	for _, c := range cases {
		sum, name := parseSHA256File([]byte(c.in))
		if sum != c.sum || name != c.name {
			t.Errorf("parseSHA256File(%.30q) = (%q, %q), want (%q, %q)", c.in, sum, name, c.sum, c.name)
		}
	}
}

// a Gentoo-style autoindex: the .iso must be picked (newest), never the
// signature/digest siblings.
const gentooIndex = `<html><body>
<a href="install-amd64-minimal-20260510T170106Z.iso">old</a>
<a href="install-amd64-minimal-20260705T170105Z.iso">iso</a>
<a href="install-amd64-minimal-20260705T170105Z.iso.asc">sig</a>
<a href="install-amd64-minimal-20260705T170105Z.iso.sha256">sum</a>
</body></html>`

func TestResolveDirectGentooStyle(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/dir/", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(gentooIndex)) })
	mux.HandleFunc("/dir/install-amd64-minimal-20260705T170105Z.iso.sha256", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(gentooSHA256))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	d := Distro{ID: "gentoo", Name: "Gentoo", Direct: true,
		IndexURL: ts.URL + "/dir/",
		Match:    []string{"install-amd64-minimal", ".iso"},
	}
	got, err := Resolve(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if got.DirectURL != ts.URL+"/dir/install-amd64-minimal-20260705T170105Z.iso" {
		t.Errorf("DirectURL = %q, want the newest iso", got.DirectURL)
	}
	if got.URL != "" || got.Magnet != "" {
		t.Errorf("URL/Magnet must be empty for a direct image, got %q / %q", got.URL, got.Magnet)
	}
	if got.SHA256 != "54ad48e83d84ebab95f5d50fe5afe4426798a5aaceb252d1ca4812201aca8668" {
		t.Errorf("SHA256 = %q", got.SHA256)
	}
}

// resolveDirect must still succeed (unverified) when no .sha256 sibling exists.
func TestResolveDirectWithoutChecksum(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/dir/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dir/" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(gentooIndex))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	d := Distro{ID: "gentoo", Direct: true, IndexURL: ts.URL + "/dir/",
		Match: []string{"install-amd64-minimal", ".iso"}}
	got, err := Resolve(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if got.SHA256 != "" {
		t.Errorf("SHA256 = %q, want empty when the checksum is missing", got.SHA256)
	}
	if got.DirectURL == "" {
		t.Error("DirectURL must still resolve without a checksum")
	}
}

// openSUSE's Current alias: the .sha256 names the real snapshot image, which
// becomes both the title and the pinned download URL.
func TestResolveOpenSUSECurrentAlias(t *testing.T) {
	const sha = "4b46ccfbda3627d39b7c5182346fa88c1af68a4ee9d83c26734d9d7c014c25c9"
	mux := http.NewServeMux()
	mux.HandleFunc("/iso/openSUSE-Tumbleweed-DVD-x86_64-Current.iso.sha256", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(sha + "  openSUSE-Tumbleweed-DVD-x86_64-Snapshot20260703-Media.iso\n"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	d := Distro{ID: "opensuse", Name: "openSUSE",
		IndexURL: ts.URL + "/iso/openSUSE-Tumbleweed-DVD-x86_64-Current.iso"}
	got, err := resolveOpenSUSE(context.Background(), ts.Client(), d)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "openSUSE-Tumbleweed-DVD-x86_64-Snapshot20260703-Media.iso" {
		t.Errorf("Title = %q, want the versioned snapshot name", got.Title)
	}
	if got.DirectURL != ts.URL+"/iso/openSUSE-Tumbleweed-DVD-x86_64-Snapshot20260703-Media.iso" {
		t.Errorf("DirectURL = %q, want the pinned snapshot URL", got.DirectURL)
	}
	if got.SHA256 != sha {
		t.Errorf("SHA256 = %q", got.SHA256)
	}
}
