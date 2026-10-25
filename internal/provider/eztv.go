package provider

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

type EZTV struct {
	client              *http.Client
	base                string
	apiBase, lookupBase string
}

func NewEZTV(client *http.Client, base string) *EZTV {
	if client == nil {
		client = DefaultClient
	}
	if base == "" {
		base = "https://eztvx.to"
	}
	e := &EZTV{client: client, base: strings.TrimRight(base, "/")}
	// Old default domains now block HTML search. Use the site's documented
	// public API for those defaults; retain explicit custom HTML mirrors.
	for _, host := range []string{"eztvx.to", "eztv.re", "eztv.ag", "eztv.it", "eztv.ch", "eztv.li"} {
		if e.base == "https://"+host {
			e.apiBase = "https://eztvx.to"
			e.lookupBase = "https://api.tvmaze.com"
			break
		}
	}
	return e
}

func (e *EZTV) Name() string { return "eztv" }

func (e *EZTV) Search(ctx context.Context, query string, out chan<- Result) error {
	if e.apiBase != "" {
		return e.searchAPI(ctx, query, out)
	}
	slug := strings.ReplaceAll(strings.TrimSpace(query), " ", "-")
	u := fmt.Sprintf("%s/search/%s", e.base, slug)
	resp, err := fetch(ctx, e.client, u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return fmt.Errorf("eztv: parse html: %w", err)
	}

	if isCloudflareChallenge(doc) {
		return ErrBlocked
	}
	if doc.Find("table.forum_header_border").Length() == 0 {
		return ErrResponseFormat
	}

	var emitErr error
	doc.Find("table.forum_header_border tr.forum_header_border, table.forum_header_border tr[name=hover]").EachWithBreak(func(_ int, row *goquery.Selection) bool {
		title := strings.TrimSpace(row.Find("td a.epinfo").Text())
		magnet, _ := row.Find("td a.magnet").Attr("href")
		if title == "" || !strings.HasPrefix(magnet, "magnet:") {
			return true
		}
		// EZTV serves its "latest torrents" table for unknown queries; only
		// emit rows that actually match the query.
		if !matchesQuery(title, query) {
			return true
		}
		cells := row.Find("td")
		size := strings.TrimSpace(cells.Eq(3).Text())
		seeds, _ := strconv.Atoi(strings.TrimSpace(cells.Eq(5).Text()))
		r := Result{
			Title:     title,
			Size:      size,
			SizeBytes: parseHumanSize(size),
			Seeders:   seeds,
			Magnet:    magnet,
			Provider:  e.Name(),
		}
		if err := emit(ctx, out, r); err != nil {
			emitErr = err
			return false
		}
		return true
	})
	return emitErr
}
