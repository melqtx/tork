package intake

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectCommonInputs(t *testing.T) {
	cases := []struct {
		raw  string
		kind Kind
	}{
		{"magnet:?xt=urn:btih:ABCDEF&dn=My+File", Magnet},
		{"0123456789abcdef0123456789abcdef01234567", InfoHash},
		{"ABCDEFGHIJKLMNOPQRSTUVWXYZ234567", InfoHash},
		{"https://example.test/path/My%20File.torrent?download=1", TorrentURL},
	}
	for _, tc := range cases {
		got, ok, err := DetectCLI(tc.raw)
		if err != nil || !ok || got.Kind != tc.kind {
			t.Errorf("DetectCLI(%q) = %+v, %v, %v; want kind %v", tc.raw, got, ok, err, tc.kind)
		}
	}
}

func TestDetectLocalFiles(t *testing.T) {
	dir := t.TempDir()
	torrentPath := filepath.Join(dir, "sample.torrent")
	plainPath := filepath.Join(dir, "no-extension")
	for _, path := range []string{torrentPath, plainPath} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, "linked.torrent")
	if err := os.Symlink(torrentPath, link); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{torrentPath, plainPath, link} {
		got, ok, err := DetectCLI(path)
		if err != nil || !ok || got.Kind != TorrentFile || !filepath.IsAbs(got.Value) {
			t.Errorf("DetectCLI(%q) = %+v, %v, %v", path, got, ok, err)
		}
	}
	if _, ok, err := DetectHome(plainPath); err != nil || ok {
		t.Fatalf("home field classified extensionless local file: ok=%v err=%v", ok, err)
	}
	if got, ok, err := DetectHome(torrentPath); err != nil || !ok || got.Kind != TorrentFile {
		t.Fatalf("DetectHome(.torrent) = %+v, %v, %v", got, ok, err)
	}
}

func TestDetectExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "sample.torrent")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok, err := DetectCLI("~/sample.torrent")
	if err != nil || !ok || got.Value != path {
		t.Fatalf("DetectCLI(home) = %+v, %v, %v", got, ok, err)
	}
}

func TestDetectResolvesRelativePathFromLaunchDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("relative.torrent", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok, err := DetectCLI("relative.torrent")
	want := filepath.Join(dir, "relative.torrent")
	if err != nil || !ok || got.Value != want {
		t.Fatalf("DetectCLI(relative) = %+v, %v, %v; want %s", got, ok, err, want)
	}
}

func TestDetectRejectsBadLocalInputs(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := DetectCLI(filepath.Join(dir, "missing.torrent")); !ok || err == nil {
		t.Fatalf("missing .torrent = ok=%v err=%v", ok, err)
	}
	// The home field must fall back to a normal search for a query that only
	// looks like a torrent path.
	if _, ok, err := DetectHome(filepath.Join(dir, "missing.torrent")); ok || err != nil {
		t.Fatalf("home missing .torrent should search: ok=%v err=%v", ok, err)
	}
	if _, ok, err := DetectCLI(dir); !ok || err == nil {
		t.Fatalf("directory = ok=%v err=%v", ok, err)
	}
	large := filepath.Join(dir, "large.torrent")
	f, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxTorrentBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, ok, err := DetectCLI(large); !ok || err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("large file = ok=%v err=%v", ok, err)
	}
}

func TestExplicitTorrentURL(t *testing.T) {
	got, err := ExplicitTorrentURL("https://example.test/download?id=12")
	if err != nil || got.Kind != TorrentURL {
		t.Fatalf("ExplicitTorrentURL = %+v, %v", got, err)
	}
	if _, err := ExplicitTorrentURL("file:///tmp/a.torrent"); err == nil {
		t.Fatal("accepted non-http URL")
	}
	if _, ok, _ := DetectCLI("https://example.test/download?id=12"); ok {
		t.Fatal("ambiguous URL was auto-detected")
	}
}
