package provider

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestTorrentsCSVSearch(t *testing.T) {
	srv := serveFixture(t, "torrentscsv.json")
	results := collect(t, NewTorrentsCSV(srv.Client(), srv.URL), "big buck bunny")
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 (the row with a broken infohash is skipped): %+v", len(results), results)
	}
	r := results[0]
	if r.Provider != "torrentscsv" || r.Title != "Big Buck Bunny" {
		t.Errorf("unexpected first row %+v", r)
	}
	if r.Seeders != 348 || r.Leechers != 55 || r.SizeBytes != 276445467 || r.Size == "" {
		t.Errorf("counts/size wrong: %+v", r)
	}
	if r.InfoHash() != "dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c" {
		t.Errorf("InfoHash = %q", r.InfoHash())
	}
}

func TestTorrentsCSVEmptyAndMalformed(t *testing.T) {
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"torrents":[],"next":null}`))
	}))
	defer empty.Close()
	if rows := collect(t, NewTorrentsCSV(empty.Client(), empty.URL), "nothing"); len(rows) != 0 {
		t.Fatalf("empty search returned %d rows", len(rows))
	}

	wrong := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"error":"maintenance"}`))
	}))
	defer wrong.Close()
	out := make(chan Result, 4)
	if err := NewTorrentsCSV(wrong.Client(), wrong.URL).Search(t.Context(), "x", out); !errors.Is(err, ErrResponseFormat) {
		t.Fatalf("err = %v, want ErrResponseFormat", err)
	}
}

func TestSubsPleaseSearch(t *testing.T) {
	srv := serveFixture(t, "subsplease.json")
	results := collect(t, NewSubsPlease(srv.Client(), srv.URL), "frieren")
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3 (the non-magnet download is skipped): %+v", len(results), results)
	}
	// Keys are sorted, so the shorter show name comes first.
	first := results[0]
	if first.Title != "[SubsPlease] Sousou no Frieren - 01 (1080p)" {
		t.Errorf("fallback title = %q", first.Title)
	}
	batch := results[1]
	if batch.Title != "[SubsPlease] Sousou no Frieren S2 (01-10) (720p) [Batch]" {
		t.Errorf("Title = %q (should come from the magnet's dn)", batch.Title)
	}
	if batch.SizeBytes != 7488913906 || batch.Size == "" {
		t.Errorf("size from xl wrong: %d / %q", batch.SizeBytes, batch.Size)
	}
	for _, r := range results {
		if r.Provider != "subsplease" || !r.Trusted || r.Category != "Anime" || r.InfoHash() == "" {
			t.Errorf("unexpected row %+v", r)
		}
	}
}

func TestSubsPleaseNoMatches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("[]"))
	}))
	defer srv.Close()
	if rows := collect(t, NewSubsPlease(srv.Client(), srv.URL), "nothing"); len(rows) != 0 {
		t.Fatalf("got %d rows for an empty search", len(rows))
	}
}

func TestInternetArchiveSearch(t *testing.T) {
	var gotQuery url.Values
	data := mustFixture(t, "archive.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Write(data)
	}))
	defer srv.Close()

	results := collect(t, NewInternetArchive(srv.Client(), srv.URL), "duck and cover")
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 (the item without a btih is skipped): %+v", len(results), results)
	}
	if q := gotQuery.Get("q"); !strings.HasPrefix(q, "title:(duck and cover) AND") || !strings.Contains(q, "-mediatype:(collection") {
		t.Errorf("search query = %q", q)
	}
	if gotQuery.Get("sort[]") != "downloads desc" {
		t.Errorf("sort = %q", gotQuery.Get("sort[]"))
	}
	r := results[0]
	if r.Title != "Duck and Cover" || r.Category != "Movies" || !r.Trusted || r.SizeBytes != 792066548 {
		t.Errorf("unexpected row %+v", r)
	}
	if r.InfoHash() != "14680b9d2dc2332852c45d4cbc5ea34c2f1c8797" {
		t.Errorf("InfoHash = %q", r.InfoHash())
	}
	if !strings.Contains(r.Magnet, "bt1.archive.org") || !strings.Contains(r.Magnet, "&ws="+url.QueryEscape(srv.URL+"/download/")) {
		t.Errorf("magnet lacks the Archive tracker or webseed: %s", r.Magnet)
	}
	if results[1].Title != "Array Title" || results[1].Category != "Audio" {
		t.Errorf("array title / audio category not handled: %+v", results[1])
	}
}

func TestLuceneTerms(t *testing.T) {
	if got := luceneTerms(`C++ "x:y" (a) OR`); got != `c x y a or` {
		t.Errorf("luceneTerms = %q", got)
	}
	if got := luceneTerms(`"()"`); got != "" {
		t.Errorf("luceneTerms of punctuation = %q, want empty", got)
	}
}

func TestYTSSkipsRetiredMirrors(t *testing.T) {
	y := NewYTS(nil, "https://yts.mx")
	for _, b := range y.bases {
		if b == "https://yts.mx" || b == "https://yts.rs" {
			t.Fatalf("retired mirror %s still queried: %v", b, y.bases)
		}
	}
	if y.bases[0] != "https://movies-api.accel.li" {
		t.Errorf("first base = %q, want the API's announced host", y.bases[0])
	}
}
