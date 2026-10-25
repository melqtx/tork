package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSourcesRejectErrorPagesAndMalformedRows(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		build      func(*http.Client, string) Provider
	}{
		{"knaben login", `<html><title>Sign in</title><body>Service unavailable</body></html>`, func(c *http.Client, u string) Provider { return NewKnaben(c, u) }},
		{"knaben broken rows", `<table><thead><tr><th>Title</th><th>Size</th><th>Seeders</th><th>Leechers</th></tr></thead><tbody><tr data-id="1"><td><a href="magnet:?xt=urn:btih:broken">Example</a></td></tr></tbody></table>`, func(c *http.Client, u string) Provider { return NewKnaben(c, u) }},
		{"tpb null", `null`, func(c *http.Client, u string) Provider { return NewPirateBay(c, u) }},
		{"tpb invalid rows", `[{"id":1,"name":"Example","info_hash":"broken","category":201}]`, func(c *http.Client, u string) Provider { return NewPirateBay(c, u) }},
		{"tpb tv invalid rows", `[{}]`, func(c *http.Client, u string) Provider { return NewPirateBayTV(c, u) }},
		{"rarbg invalid rows", `{"results":[{"n":"Example","h":"broken"}]}`, func(c *http.Client, u string) Provider { return NewRARBG(c, u) }},
		{"yts invalid rows", `{"status":"ok","data":{"movie_count":1,"movies":[{"title_long":"Example","torrents":[{"hash":"broken"}]}]}}`, func(c *http.Client, u string) Provider { p := NewYTS(c, u); p.bases = []string{u}; return p }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(tc.body)) }))
			defer srv.Close()
			out := make(chan Result, 2)
			if err := tc.build(srv.Client(), srv.URL).Search(context.Background(), "example", out); !errors.Is(err, ErrResponseFormat) {
				t.Fatalf("invalid response reported healthy: %v", err)
			}
			if len(out) != 0 {
				t.Fatal("invalid response emitted an unusable result")
			}
		})
	}
}

func TestSourcesKeepRecognizableEmptySearchesWorking(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		build      func(*http.Client, string) Provider
	}{
		{"knaben", `<title>Example - All - Knaben Database</title><table class="table"><thead><tr><th>Title</th><th>Size</th><th>Seeders</th><th>Leechers</th></tr></thead><tbody></tbody></table>`, func(c *http.Client, u string) Provider { return NewKnaben(c, u) }},
		{"tpb empty array", `[]`, func(c *http.Client, u string) Provider { return NewPirateBay(c, u) }},
		{"tpb no-result sentinel", `[{"id":"0","info_hash":"0000000000000000000000000000000000000000","name":"No results returned"}]`, func(c *http.Client, u string) Provider { return NewPirateBay(c, u) }},
		{"rarbg", `{"results":[]}`, func(c *http.Client, u string) Provider { return NewRARBG(c, u) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(tc.body)) }))
			defer srv.Close()
			out := make(chan Result, 2)
			if err := tc.build(srv.Client(), srv.URL).Search(context.Background(), "no match", out); err != nil {
				t.Fatal(err)
			}
			if len(out) != 0 {
				t.Fatal("empty search emitted a result")
			}
		})
	}
}
