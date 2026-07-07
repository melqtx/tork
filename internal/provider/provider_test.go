package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// serveFixture returns a test server that responds to every request with the
// named testdata file.
func serveFixture(t *testing.T, name string) *httptest.Server {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// collect runs p.Search and drains all emitted results.
func collect(t *testing.T, p Provider, query string) []Result {
	t.Helper()
	out := make(chan Result, 128)
	if err := p.Search(context.Background(), query, out); err != nil {
		t.Fatalf("%s.Search: %v", p.Name(), err)
	}
	close(out)
	var results []Result
	for r := range out {
		results = append(results, r)
	}
	return results
}

func TestYTSSearch(t *testing.T) {
	srv := serveFixture(t, "yts.json")
	results := collect(t, NewYTS(srv.Client(), srv.URL), "inception")
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	r := results[0]
	if r.Title != "Inception (2010) [1080p]" {
		t.Errorf("Title = %q", r.Title)
	}
	if r.Seeders != 512 || r.Leechers != 43 || r.SizeBytes != 2576980378 {
		t.Errorf("stats wrong: %+v", r)
	}
	if !strings.HasPrefix(r.Magnet, "magnet:?xt=urn:btih:aabbccddeeff") {
		t.Errorf("Magnet = %q", r.Magnet)
	}
	if r.Provider != "yts" {
		t.Errorf("Provider = %q", r.Provider)
	}
}

func TestNyaaSearch(t *testing.T) {
	srv := serveFixture(t, "nyaa.rss")
	results := collect(t, NewNyaa(srv.Client(), srv.URL), "frieren")
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	r := results[0]
	if !strings.Contains(r.Title, "SubsPlease") {
		t.Errorf("Title = %q", r.Title)
	}
	if r.Seeders != 1345 || r.Leechers != 89 {
		t.Errorf("seeders/leechers wrong: %+v", r)
	}
	if r.Size != "1.4 GiB" || r.SizeBytes == 0 {
		t.Errorf("size wrong: %q / %d", r.Size, r.SizeBytes)
	}
	if !strings.Contains(r.Magnet, "0123456789abcdef0123456789abcdef01234567") {
		t.Errorf("Magnet = %q", r.Magnet)
	}
	if !r.Trusted {
		t.Error("first item has <nyaa:trusted>Yes</nyaa:trusted>, want Trusted=true")
	}
	if results[1].Trusted {
		t.Error("second item has <nyaa:trusted>No</nyaa:trusted>, want Trusted=false")
	}
}

func TestRSSSearch(t *testing.T) {
	srv := serveFixture(t, "generic.rss")
	results := collect(t, NewRSS(srv.Client(), "feed", srv.URL+"/?q={query}"), "ubuntu iso")
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	r := results[0]
	if r.Title != "Ubuntu 24.04 Desktop amd64" {
		t.Errorf("Title = %q", r.Title)
	}
	if r.Seeders != 88 || r.Leechers != 7 || r.SizeBytes != 3221225472 {
		t.Errorf("stats wrong: %+v", r)
	}
	if !strings.HasPrefix(r.Magnet, "magnet:?xt=urn:btih:abcdef") {
		t.Errorf("Magnet = %q", r.Magnet)
	}
	if !r.Trusted {
		t.Error("trusted torznab attr should set Trusted=true")
	}
	if results[1].SizeBytes != 734003200 || results[1].Leechers != 3 {
		t.Errorf("enclosure/peers parse wrong: %+v", results[1])
	}
}

func TestPirateBayAPISearch(t *testing.T) {
	srv := serveFixture(t, "apibay.json")
	results := collect(t, NewPirateBayMovies(srv.Client(), srv.URL), "ubuntu")
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1: %+v", len(results), results)
	}
	r := results[0]
	if r.Provider != "tpb-movies" || r.Title != "Ubuntu 24.04 LTS Desktop amd64" {
		t.Errorf("wrong result: %+v", r)
	}
	if r.Seeders != 512 || r.Leechers != 18 || r.SizeBytes != 5905580032 {
		t.Errorf("stats wrong: %+v", r)
	}
	if !strings.HasPrefix(r.Magnet, "magnet:?xt=urn:btih:abcdefabcdef") {
		t.Errorf("Magnet = %q", r.Magnet)
	}
}

func TestEZTVSearchFiltersUnrelatedRows(t *testing.T) {
	srv := serveFixture(t, "eztv.html")
	results := collect(t, NewEZTV(srv.Client(), srv.URL), "severance")
	// fixture has 3 rows; the "Some Other Show" row must be filtered out
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2: %+v", len(results), results)
	}
	r := results[0]
	if r.Title != "Severance S02E05 1080p WEB H264" {
		t.Errorf("Title = %q", r.Title)
	}
	if !strings.HasPrefix(r.Magnet, "magnet:?xt=urn:btih:aaaa1111") {
		t.Errorf("Magnet = %q", r.Magnet)
	}
	if r.Size != "2.61 GB" || r.Seeders != 1420 {
		t.Errorf("size/seeds wrong: %+v", r)
	}
}

func TestX1337SearchAndResolve(t *testing.T) {
	search, _ := os.ReadFile(filepath.Join("testdata", "1337x_search.html"))
	detail, _ := os.ReadFile(filepath.Join("testdata", "1337x_detail.html"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/torrent/") {
			w.Write(detail)
		} else {
			w.Write(search)
		}
	}))
	defer srv.Close()

	p := NewX1337(srv.Client(), []string{srv.URL})
	results := collect(t, p, "ubuntu")
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	r := results[0]
	if r.Title != "Ubuntu 24.04 LTS Desktop amd64" {
		t.Errorf("Title = %q", r.Title)
	}
	if r.Magnet != "" || !strings.Contains(r.DetailURL, "/torrent/5555555/") {
		t.Errorf("expected lazy magnet, got %+v", r)
	}
	if r.Seeders != 981 || r.Leechers != 37 || r.Size != "5.7 GB" {
		t.Errorf("stats wrong: %+v", r)
	}

	magnet, err := p.ResolveMagnet(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(magnet, "magnet:?xt=urn:btih:deadbeef") {
		t.Errorf("resolved magnet = %q", magnet)
	}
}

func TestX1337MirrorFailover(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden) // Cloudflare-style block
	}))
	defer dead.Close()
	good := serveFixture(t, "1337x_search.html")

	p := NewX1337(nil, []string{dead.URL, good.URL})
	results := collect(t, p, "ubuntu")
	if len(results) != 2 {
		t.Fatalf("failover produced %d results, want 2", len(results))
	}
	// active mirror is remembered
	if got := p.orderedMirrors()[0]; got != good.URL {
		t.Errorf("active mirror = %q, want %q", got, good.URL)
	}
}

func TestX1337AllMirrorsBlocked(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer dead.Close()
	p := NewX1337(nil, []string{dead.URL})
	out := make(chan Result, 8)
	err := p.Search(context.Background(), "x", out)
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Errorf("expected ErrBlocked, got %v", err)
	}
}

func TestBuildMagnet(t *testing.T) {
	m := BuildMagnet("ABCDEF", "My File", []string{"udp://t.example:80/announce"})
	want := "magnet:?xt=urn:btih:abcdef&dn=My+File&tr=udp%3A%2F%2Ft.example%3A80%2Fannounce"
	if m != want {
		t.Errorf("BuildMagnet = %q, want %q", m, want)
	}
}

func TestParseHumanSize(t *testing.T) {
	cases := map[string]int64{
		"1.4 GiB":   1503238553, // 1.4 * 2^30, truncated
		"731.5 MiB": 767033344,
		"2.61 GB":   2802466160,
		"800 MB":    800 << 20,
		"garbage":   0,
		"":          0,
	}
	for in, want := range cases {
		if got := parseHumanSize(in); got != want {
			t.Errorf("parseHumanSize(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestEmitRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := make(chan Result) // unbuffered, no reader
	if err := emit(ctx, out, Result{}); err == nil {
		t.Error("emit should fail on cancelled context")
	}
}

func TestYTSMirrorFailover(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable) // first mirror down
	}))
	defer dead.Close()
	good := serveFixture(t, "yts.json")

	p := NewYTS(good.Client(), dead.URL)
	p.bases = []string{dead.URL, good.URL} // deterministic: no live mirrors
	results := collect(t, p, "inception")
	if len(results) != 2 {
		t.Fatalf("failover should have produced 2 results, got %d", len(results))
	}
}
