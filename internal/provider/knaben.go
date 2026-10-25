package provider

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Knaben scrapes Knaben's web search. Knaben is a metasearch that
// aggregates many indexers (The Pirate Bay, 1337x, Nyaa, YTS, EZTV, …) so a
// single query covers every content type - it is tork's universal source.
//
// NOTE: Knaben also exposes a JSON API at api.knaben.* but that endpoint
// ignores the query and only returns a "latest torrents" feed, so we scrape the
// HTML search page (which filters correctly) instead. The .eu domain is dead;
// .org is the live host.
const knabenWeb = "https://knaben.org"

type Knaben struct {
	client *http.Client
	base   string
}

func NewKnaben(client *http.Client, base string) *Knaben {
	if client == nil {
		client = DefaultClient
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	// tolerate stale configs: the api.* host only serves a latest feed, and the
	// knaben.eu mirror is dead - fall back to the live web host in both cases.
	if base == "" || strings.Contains(base, "api.knaben") || strings.Contains(base, "knaben.eu") {
		base = knabenWeb
	}
	return &Knaben{client: client, base: base}
}

func (k *Knaben) Name() string { return "knaben" }

func (k *Knaben) Search(ctx context.Context, query string, out chan<- Result) error {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	// /search/<query>/<category:0=all>/<page>/<order>
	u := k.base + "/search/" + url.PathEscape(q) + "/0/1/seeders"

	resp, err := fetch(ctx, k.client, u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return fmt.Errorf("knaben: parse html: %w", err)
	}

	if isCloudflareChallenge(doc) {
		return ErrBlocked
	}
	// Empty searches retain this table and its headers. A login/error page
	// without the search document is not a successful zero-result response.
	table := doc.Find("table").FilterFunction(func(_ int, table *goquery.Selection) bool {
		headers := map[string]bool{}
		table.Find("thead th").Each(func(_ int, th *goquery.Selection) {
			headers[strings.ToLower(strings.TrimSpace(th.Text()))] = true
		})
		return headers["title"] && headers["size"] && headers["seeders"] && headers["leechers"] && table.Find("tbody").Length() > 0
	}).First()
	if table.Length() == 0 {
		return ErrResponseFormat
	}

	var emitErr error
	emitted := 0
	table.Find("tbody tr[data-id]").EachWithBreak(func(_ int, row *goquery.Selection) bool {
		anchor := row.Find(`a[href^="magnet:"]`).First()
		magnet, ok := anchor.Attr("href")
		if !ok || (Result{Magnet: magnet}).InfoHash() == "" {
			return true
		}
		tds := row.Find("td")
		title := strings.TrimSpace(anchor.AttrOr("title", ""))
		if title == "" {
			title = strings.TrimSpace(tds.Eq(1).Text())
		}
		title = html.UnescapeString(title) // decode &euml; &quot; etc.
		if title == "" {
			return true
		}
		size := strings.TrimSpace(tds.Eq(2).Text())
		// the first cell links the source category (/browse/<id>/1); its first
		// anchor is the top-level label ("Movies", "XXX", "Video", …).
		category := strings.TrimSpace(tds.First().Find(`a[href^="/browse/"]`).First().Text())
		r := Result{
			Title:     title,
			Size:      size,
			SizeBytes: parseHumanSize(size),
			Seeders:   atoiDefault(tds.Eq(4).Text()),
			Leechers:  atoiDefault(tds.Eq(5).Text()),
			Magnet:    magnet,
			Provider:  k.Name(),
			Category:  category,
		}
		if err := emit(ctx, out, r); err != nil {
			emitErr = err
			return false
		}
		emitted++
		return true
	})
	if emitErr == nil && emitted == 0 && table.Find("tbody tr").Length() > 0 {
		return ErrResponseFormat
	}
	return emitErr
}
