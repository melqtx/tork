package provider

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAdaptersPreserveUnknownAndReportedZeroCounts(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		make       func(*http.Client, string) Provider
	}{
		{"tpb", `[{"id":1,"name":"Example","info_hash":"0123456789abcdef0123456789abcdef01234567","seeders":0},{"id":2,"name":"Example 2","info_hash":"89abcdef0123456789abcdef0123456789abcdef"}]`, func(c *http.Client, u string) Provider { return NewPirateBay(c, u) }},
		{"yts", `{"status":"ok","data":{"movie_count":1,"movies":[{"title_long":"Example (2020)","torrents":[{"hash":"0123456789abcdef0123456789abcdef01234567","quality":"1080p","seeds":0},{"hash":"89abcdef0123456789abcdef0123456789abcdef","quality":"720p"}]}]}}`, func(c *http.Client, u string) Provider { return NewYTS(c, u) }},
		{"rarbg", `{"results":[{"n":"Example","h":"0123456789abcdef0123456789abcdef01234567","se":0},{"n":"Example 2","h":"89abcdef0123456789abcdef0123456789abcdef"}]}`, func(c *http.Client, u string) Provider { return NewRARBG(c, u) }},
		{"rss", `<rss><channel><item><title>Example</title><link>magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567</link><attr name="seeders" value="0"/></item><item><title>Example 2</title><link>magnet:?xt=urn:btih:89abcdef0123456789abcdef0123456789abcdef</link></item></channel></rss>`, func(c *http.Client, u string) Provider { return NewRSS(c, "rss", u+"?q={query}") }},
		{"nyaa", `<rss xmlns:nyaa="https://nyaa.si/xmlns/nyaa"><channel><item><title>Example</title><nyaa:infoHash>0123456789abcdef0123456789abcdef01234567</nyaa:infoHash><nyaa:seeders>0</nyaa:seeders></item><item><title>Example 2</title><nyaa:infoHash>89abcdef0123456789abcdef0123456789abcdef</nyaa:infoHash></item></channel></rss>`, func(c *http.Client, u string) Provider { return NewNyaa(c, u) }},
		{"limetorrents", strings.ReplaceAll(strings.ReplaceAll(limeSearchFixture, ">1,234<", ">0<"), ">42<", "><"), func(c *http.Client, u string) Provider { return NewLimeTorrents(c, u) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(tc.body)) }))
			defer srv.Close()
			rows := collect(t, tc.make(srv.Client(), srv.URL), "Example")
			if len(rows) != 2 || !rows[0].KnownSeeders() || rows[0].Seeders != 0 || rows[1].KnownSeeders() {
				t.Fatalf("adapter conflated missing and zero: %+v", rows)
			}
			for _, row := range rows {
				if len(row.Observations) != 1 || row.Observations[0].FetchedAt.IsZero() {
					t.Fatal("source report lacks fetch time")
				}
			}
		})
	}
}

func TestSwarmCountsDistinguishMissingInvalidAndZero(t *testing.T) {
	for _, tc := range []struct {
		text    string
		want    int
		unknown bool
	}{{"", 0, true}, {"N/A", 0, true}, {"-1", 0, true}, {"0", 0, false}, {"1,234", 1234, false}} {
		r := (Result{}).WithSwarm(tc.text, tc.text)
		if r.Seeders != tc.want || r.SeedersUnknown != tc.unknown || r.LeechersUnknown != tc.unknown {
			t.Fatalf("%q: %+v", tc.text, r)
		}
	}
}

func TestMergeKeepsPairedCountsAndLatestSourceObservations(t *testing.T) {
	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	a := (Result{Provider: "alpha"}).WithSwarm("12", "300").WithObservation(at)
	b := (Result{Provider: "beta"}).WithSwarm("100", "2").WithObservation(at)
	merged := Merge(a, b)
	if merged.Seeders != 100 || merged.Leechers != 2 || len(merged.Observations) != 2 {
		t.Fatalf("invented swarm pair: %+v", merged)
	}
	latest := (Result{Provider: "beta"}).WithSwarm("0", "").WithObservation(at.Add(time.Minute))
	merged = Merge(merged, latest)
	if merged.Seeders != 12 || merged.Leechers != 300 || len(merged.Observations) != 2 {
		t.Fatalf("stale high report survived newer source report: %+v", merged)
	}
	if merged.Observations[1].Seeders != 0 || !merged.Observations[1].LeechersUnknown {
		t.Fatal("new zero/missing pair lost")
	}
	unknown := (Result{Provider: "unknown"}).WithSwarm("", "")
	if result := Merge(unknown, latest); !result.KnownSeeders() || result.Seeders != 0 {
		t.Fatal("known zero became unknown")
	}
}
