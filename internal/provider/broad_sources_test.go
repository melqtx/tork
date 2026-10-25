package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRARBGSearchValidatesResultsAndPreservesCategories(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "keywords:ubuntu format xml:paginate_by:100:format:json/") {
			t.Error("query escaped into API options or was incorrectly normalized")
		}
		w.Write([]byte(`{"results":[
   {"n":"Ubuntu Linux","h":"0123456789abcdef0123456789abcdef01234567","c":"Apps","s":1073741824,"se":17,"le":2},
   {"n":"Open game","h":"89abcdef0123456789abcdef0123456789abcdef","c":"Games","s":42,"se":3,"le":1},
   {"n":"Filtered category","h":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","c":"XXX","s":42},
   {"n":"Broken","h":"invalid","c":"Apps"}
  ]}`))
	}))
	defer srv.Close()
	got := collect(t, NewRARBG(srv.Client(), srv.URL), "ubuntu:format:xml")
	if len(got) != 3 || got[0].Provider != "therarbg" || got[0].Category != "Apps" || got[1].Category != "Games" || got[0].SizeBytes != 1<<30 || got[0].Seeders != 17 {
		t.Fatalf("unexpected results: %+v", got)
	}
	if (ContentFilter{HideNSFW: true}).Allow("ubuntu", got[2]) {
		t.Fatal("source category bypassed shared adult filter")
	}
	if got[0].Trusted {
		t.Fatal("public source listing was incorrectly marked trusted")
	}
}

func TestRARBGRejectsUnexpectedResponsesAndHonorsCancellation(t *testing.T) {
	for _, body := range []string{`{}`, `{"results":null}`, `<html>blocked</html>`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body)) }))
		err := NewRARBG(srv.Client(), srv.URL).Search(context.Background(), "ubuntu", make(chan Result, 1))
		srv.Close()
		if !errors.Is(err, ErrResponseFormat) {
			t.Fatalf("unexpected response was accepted: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := NewRARBG(DefaultClient, "http://127.0.0.1:1")
	if err := p.Search(ctx, "ubuntu", make(chan Result)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled search: %v", err)
	}
}

const limeSearchFixture = `<html><title>Search results</title><body>
 <a href="/ad-torrent-999.html">Sponsored download</a>
 <table class="table2"><tbody>
 <tr><th>Torrent Name</th></tr>
 <tr bgcolor="white"><td><div class="tt-name"><a href="/Ubuntu-Linux-torrent-1.html">Ubuntu Linux</a></div></td><td>1 day ago - in Applications</td><td>1.5 GB</td><td class="tdseed">1,234</td><td class="tdleech">23</td></tr>
 <tr bgcolor="gray"><td><div class="tt-name"><a href="/Open-game-torrent-2.html">Open game</a></div></td><td>2 days ago - in Games</td><td>700 MB</td><td class="tdseed">42</td><td class="tdleech">3</td></tr>
 <tr bgcolor="white"><td><div class="tt-name"><a href="https://external.invalid/Other-torrent-3.html">External redirect</a></div></td></tr>
 </tbody></table></body></html>`

func TestLimeTorrentsSearchAndSelectedMagnet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/search/") {
			w.Write([]byte(limeSearchFixture))
			return
		}
		w.Write([]byte(`<html><a href="magnet:?xt=urn:btih:invalid">bad</a><a class="csprite_dltorrent" href="magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&amp;tr=https%3A%2F%2Fexample.org%2Fannounce">Magnet</a></html>`))
	}))
	defer srv.Close()
	p := NewLimeTorrents(srv.Client(), srv.URL)
	got := collect(t, p, "ubuntu")
	if len(got) != 2 || got[0].Title != "Ubuntu Linux" || got[0].Category != "Applications" || got[0].Seeders != 1234 || got[0].SizeBytes != 1610612736 || got[1].Category != "Games" {
		t.Fatalf("unexpected rows: %+v", got)
	}
	if got[0].Magnet != "" {
		t.Fatal("search eagerly resolved detail pages")
	}
	magnet, err := p.ResolveMagnet(context.Background(), got[0])
	if err != nil || len((Result{Magnet: magnet}).Trackers()) != 1 {
		t.Fatalf("selected magnet: %v", err)
	}
	if _, err := p.ResolveMagnet(context.Background(), Result{DetailURL: "https://external.invalid/torrent.html"}); err == nil {
		t.Fatal("resolver followed an unrelated detail URL")
	}
}

func TestLimeTorrentsDoesNotReportChallengesAsEmptySearch(t *testing.T) {
	for _, tc := range []struct {
		body string
		want error
	}{
		{`<html><title>Just a moment...</title></html>`, ErrBlocked},
		{`<html><title>Login</title></html>`, ErrResponseFormat},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(tc.body)) }))
		err := NewLimeTorrents(srv.Client(), srv.URL).Search(context.Background(), "ubuntu", make(chan Result, 1))
		srv.Close()
		if !errors.Is(err, tc.want) {
			t.Fatalf("error=%v, want %v", err, tc.want)
		}
	}
}

func TestPirateBayGeneralSearchIncludesAppsAndGamesAndFiltersAdult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`[
 {"id":"1","name":"Ubuntu Linux","info_hash":"0123456789abcdef0123456789abcdef01234567","category":"301","size":"1024","seeders":"12"},
 {"id":"2","name":"Open game","info_hash":"89abcdef0123456789abcdef0123456789abcdef","category":"401","size":"2048","seeders":"3"},
 {"id":"3","name":"Category-only adult","info_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","category":"501","size":"42","seeders":"1"}
 ]`))
	}))
	defer srv.Close()
	got := collect(t, NewPirateBay(srv.Client(), srv.URL), "ubuntu")
	if len(got) != 3 || got[0].Category != "Applications" || got[1].Category != "Games" {
		t.Fatalf("general search was still limited to movie/TV categories: %+v", got)
	}
	if (ContentFilter{HideNSFW: true}).Allow("ubuntu", got[2]) {
		t.Fatal("all-category search lost adult category information")
	}
	if len(collect(t, NewPirateBayMovies(srv.Client(), srv.URL), "ubuntu")) != 0 {
		t.Fatal("legacy movie-only search changed scope")
	}
}

func TestPirateBayBrowseAcceptsNumericFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/precompiled/data_top100_all.json" {
			t.Error("general browse used a category-limited endpoint")
		}
		w.Write([]byte(`[{"id":42,"name":"Open software","info_hash":"0123456789abcdef0123456789abcdef01234567","category":303,"seeders":19,"leechers":2,"size":12345,"added":1700000000}]`))
	}))
	defer srv.Close()
	got := collect(t, NewPirateBay(srv.Client(), srv.URL), "")
	if len(got) != 1 || got[0].SizeBytes != 12345 || got[0].Seeders != 19 || got[0].Category != "Applications" {
		t.Fatalf("numeric browse fields failed: %+v", got)
	}
}
