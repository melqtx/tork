package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const supportedRSSItem = `<item><title>Ubuntu 1080p</title><torznab:attr name="infohash" value="0123456789abcdef0123456789abcdef01234567"/></item>`

func feedXML(items string) string {
	return `<rss xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>` + items + `</channel></rss>`
}

func TestRSSChecksValidateProtocolAndUsableItems(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       error
		count      int
	}{
		{"html", `<html><body>sign in</body></html>`, ErrFeedFormat, 0},
		{"missing channel", `<rss/>`, ErrFeedFormat, 0},
		{"empty feed", feedXML(""), nil, 0},
		{"auth", `<error code="100" description="secret"/>`, ErrFeedAuth, 0},
		{"api error", `<error code="201" description="secret"/>`, ErrFeedAPI, 0},
		{"torrent only", feedXML(`<item><title>Ubuntu</title><enclosure url="https://example.org/file.torrent"/></item>`), ErrFeedUnsupported, 0},
		{"invalid hash", feedXML(`<item><title>Ubuntu</title><torznab:attr name="infohash" value="not-a-hash"/></item>`), ErrFeedUnsupported, 0},
		{"partial", feedXML(supportedRSSItem + `<item><title>Unsupported</title></item>`), ErrFeedUnsupported, 1},
		{"extra document", feedXML("") + `<html/>`, ErrFeedFormat, 0},
		{"valid comment", feedXML(supportedRSSItem) + `<!-- footer -->`, nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(tc.body)) }))
			defer srv.Close()
			p := NewRSS(srv.Client(), "feed", srv.URL)
			out := make(chan Result, 8)
			err := p.Search(context.Background(), "ubuntu", out)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v, want=%v", err, tc.want)
			}
			if len(out) != tc.count {
				t.Fatalf("got %d usable items, want %d", len(out), tc.count)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("raw feed error leaked")
			}
			if len(out) > 0 {
				r := <-out
				if len(r.Trackers()) != 0 {
					t.Fatal("hash-only custom feed injected public trackers")
				}
			}
		})
	}
}

func TestTorznabSearchParametersAndMagnetAttribute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("q") != "ubuntu & linux" || q.Get("t") != "search" || q.Get("extended") != "1" || q.Get("apikey") != "a%+&key" || q.Get("cat") != "2000" {
			t.Error("Torznab parameters were lost or incorrectly escaped")
		}
		w.Write([]byte(feedXML(`<item><title>Ubuntu</title><torznab:attr name="magneturl" value="magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&amp;tr=https%3A%2F%2Fexample.org%2Fannounce"/></item>`)))
	}))
	defer srv.Close()
	p := NewTorznab(srv.Client(), "feed", srv.URL+"?cat=2000&t=caps&q=old", "a%+&key")
	out := make(chan Result, 1)
	if err := p.Search(context.Background(), "ubuntu & linux", out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || len((<-out).Trackers()) != 1 {
		t.Fatal("magneturl attribute or original trackers were lost")
	}
}

func TestRSSTemplatePreservesPercentEscapes(t *testing.T) {
	got := renderSearchURL("https://example.org/?key=a%2Bb&q=%s", "ubuntu & linux")
	if got != "https://example.org/?key=a%2Bb&q=ubuntu+%26+linux" {
		t.Fatal("template corrupted an escaped key")
	}
}
