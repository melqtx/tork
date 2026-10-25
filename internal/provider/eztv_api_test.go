package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func localEZTV(t *testing.T, h http.HandlerFunc) *EZTV {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p := NewEZTV(srv.Client(), "")
	p.apiBase, p.lookupBase = srv.URL, srv.URL
	return p
}

func TestEZTVAPISearchResolvesShowAndKeepsRequestedEpisode(t *testing.T) {
	var lookup, api atomic.Int32
	p := localEZTV(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/shows":
			lookup.Add(1)
			if r.URL.Query().Get("q") != "Severance" {
				t.Errorf("wrong lookup query: %s", r.URL.RawQuery)
			}
			w.Write([]byte(`[{"show":{"name":"Severance","premiered":"2022-02-18","externals":{"imdb":"tt1234567"}}},{"show":{"name":"Severance","premiered":"2015-01-01","externals":{"imdb":"tt7654321"}}}]`))
		case "/api/get-torrents":
			api.Add(1)
			if r.URL.Query().Get("imdb_id") != "1234567" {
				t.Errorf("looked up the wrong show: %s", r.URL.RawQuery)
			}
			w.Write([]byte(`{"torrents_count":3,"torrents":[{"title":"Severance.S02E01.1080p.WEB.H264","hash":"1111111111111111111111111111111111111111","size_bytes":"123456","seeds":70,"peers":"4"},{"title":"Severance S02E02 1080p WEB H264","hash":"2222222222222222222222222222222222222222"},{"title":"Some Other Show S02E01 1080p","hash":"3333333333333333333333333333333333333333"}]}`))
		default:
			t.Errorf("unexpected endpoint: %s", r.URL)
			w.WriteHeader(404)
		}
	})
	rows := collect(t, p, "Severance.2022.S02E01.1080p")
	if len(rows) != 1 || rows[0].Seeders != 70 || rows[0].Leechers != 4 || rows[0].SizeBytes != 123456 || rows[0].Category != "TV" || rows[0].InfoHash() == "" {
		t.Fatalf("incorrect API results: %+v", rows)
	}
	if lookup.Load() != 1 || api.Load() != 1 {
		t.Fatal("unexpected request count")
	}
}

func TestEZTVAPICanaryAndIMDbQueriesAvoidTitleLookup(t *testing.T) {
	for _, query := range []string{"1080p", "tt1234567"} {
		t.Run(query, func(t *testing.T) {
			p := localEZTV(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/get-torrents" {
					t.Error("quality/ID search used title lookup")
					w.WriteHeader(500)
					return
				}
				w.Write([]byte(`{"torrents_count":1,"torrents":[{"title":"Example S01E01 1080p","hash":"1111111111111111111111111111111111111111"}]}`))
			})
			if rows := collect(t, p, query); len(rows) != 1 {
				t.Fatalf("no result for %s", query)
			}
		})
	}
}

func TestEZTVEmptyShowLookupStillVerifiesSource(t *testing.T) {
	for _, body := range []string{`{"error":"unavailable"}`, `{"torrents_count":1}`, `{"torrents_count":1,"torrents":[{"title":"bad hash","hash":"invalid"}]}`} {
		p := localEZTV(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/search/") {
				w.Write([]byte(`[]`))
				return
			}
			w.Write([]byte(body))
		})
		err := p.Search(context.Background(), "nonexistent show", make(chan Result, 2))
		if !errors.Is(err, ErrResponseFormat) {
			t.Fatalf("invalid source response reported success: %v", err)
		}
	}
}

func TestEZTVOfficialDefaultsUseAPIAndCustomMirrorsStayLocal(t *testing.T) {
	for _, base := range []string{"", "https://eztv.re", "https://eztvx.to/"} {
		if p := NewEZTV(nil, base); p.apiBase == "" {
			t.Fatalf("old default %s kept blocked HTML path", base)
		}
	}
	if p := NewEZTV(nil, "https://custom.example/eztv"); p.apiBase != "" {
		t.Fatal("custom mirror leaked to public API/lookup")
	}
}

func TestEZTVNumericShowNameIsNotTreatedAsAYear(t *testing.T) {
	p := localEZTV(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search/shows" {
			if r.URL.Query().Get("q") != "1923" {
				t.Error("numeric show title was removed")
			}
			w.Write([]byte(`[{"show":{"name":"1923","premiered":"2022-12-18","externals":{"imdb":"tt1234567"}}}]`))
			return
		}
		if r.URL.Query().Get("imdb_id") != "1234567" {
			t.Error("numeric title search fell back to unrelated latest feed")
		}
		w.Write([]byte(`{"torrents_count":1,"torrents":[{"title":"1923 S01E01 1080p","hash":"1111111111111111111111111111111111111111"}]}`))
	})
	if rows := collect(t, p, "1923"); len(rows) != 1 {
		t.Fatal("numeric show was not found")
	}
}
