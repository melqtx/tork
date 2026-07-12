package engine

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anacrolix/torrent"
	"github.com/melqtx/tork/internal/intake"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestFetchMetaInfoRejectsOversizedResponse(t *testing.T) {
	eng := &Engine{torrentHTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			ContentLength: intake.MaxTorrentBytes + 1,
			Body:          io.NopCloser(strings.NewReader("unused")),
		}, nil
	})}}
	if _, err := eng.fetchMetaInfo(t.Context(), "https://example.test/download?id=1"); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized response error = %v", err)
	}
}

func TestFetchMetaInfoRejectsOversizedChunkedResponse(t *testing.T) {
	// A chunked response carries no Content-Length, so the cap must apply to
	// the bytes actually read, with the size error rather than a bencode one.
	body := io.MultiReader(
		strings.NewReader("d8:announce"),
		io.LimitReader(zeroReader{}, intake.MaxTorrentBytes+1),
	)
	eng := &Engine{torrentHTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			ContentLength: -1,
			Body:          io.NopCloser(body),
		}, nil
	})}}
	if _, err := eng.fetchMetaInfo(t.Context(), "https://example.test/big"); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized chunked response error = %v", err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func TestAddTorrentFileRejectsMalformedAndOversizedFiles(t *testing.T) {
	eng := &Engine{}
	bad := filepath.Join(t.TempDir(), "bad.torrent")
	if err := os.WriteFile(bad, []byte("not metainfo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := eng.AddTorrentFileForPreview(bad); err == nil || !strings.Contains(err.Error(), "read torrent") {
		t.Fatalf("malformed file error = %v", err)
	}
	large := filepath.Join(t.TempDir(), "large.torrent")
	f, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(intake.MaxTorrentBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, _, _, _, err := eng.AddTorrentFileForPreview(large); err == nil || !strings.Contains(err.Error(), "16 MiB") {
		t.Fatalf("oversized file error = %v", err)
	}
}

func TestMergeSpecHintsPreservesAndDeduplicatesDiscovery(t *testing.T) {
	dst := &torrent.TorrentSpec{
		Trackers: [][]string{{"https://cached/announce"}},
		Webseeds: []string{"https://cached/file"},
	}
	src := &torrent.TorrentSpec{
		Trackers:    [][]string{{"https://cached/announce", "https://magnet/announce"}},
		Webseeds:    []string{"https://cached/file", "https://magnet/file"},
		PeerAddrs:   []string{"127.0.0.1:6881"},
		DisplayName: "magnet name",
	}
	mergeSpecHints(dst, src)
	if trackerCount(dst.Trackers) != 2 {
		t.Fatalf("trackers = %#v", dst.Trackers)
	}
	if len(dst.Webseeds) != 2 || len(dst.PeerAddrs) != 1 || dst.DisplayName != "magnet name" {
		t.Fatalf("merged spec = %+v", dst)
	}
}
