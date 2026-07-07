package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/PuerkitoBio/goquery"
)

// X1337 scrapes 1337x. Search results carry only a DetailURL; the magnet is
// resolved lazily via ResolveMagnet when the user selects a row. Cloudflare
// blocks surface as ErrBlocked and the next mirror is tried.
type X1337 struct {
	client  *http.Client
	mirrors []string

	mu     sync.Mutex
	active string // first mirror that worked this session
}

func NewX1337(client *http.Client, mirrors []string) *X1337 {
	if client == nil {
		client = DefaultClient
	}
	if len(mirrors) == 0 {
		mirrors = []string{"https://1337x.to", "https://1337x.st", "https://x1337x.ws"}
	}
	return &X1337{client: client, mirrors: mirrors}
}

func (x *X1337) Name() string { return "1337x" }

func (x *X1337) orderedMirrors() []string {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.active == "" {
		return x.mirrors
	}
	out := []string{x.active}
	for _, m := range x.mirrors {
		if m != x.active {
			out = append(out, m)
		}
	}
	return out
}

func (x *X1337) setActive(m string) {
	x.mu.Lock()
	x.active = m
	x.mu.Unlock()
}

func (x *X1337) Search(ctx context.Context, query string, out chan<- Result) error {
	var lastErr error
	for _, mirror := range x.orderedMirrors() {
		u := fmt.Sprintf("%s/search/%s/1/", mirror, url.PathEscape(query))
		resp, err := fetch(ctx, x.client, u)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue // try next mirror, including on ErrBlocked
		}
		doc, err := goquery.NewDocumentFromReader(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("1337x: parse html: %w", err)
			continue
		}
		if isCloudflareChallenge(doc) {
			lastErr = fmt.Errorf("%s: %w", mirror, ErrBlocked)
			continue
		}
		x.setActive(mirror)
		return x.parseSearchPage(ctx, doc, mirror, out)
	}
	if lastErr == nil {
		lastErr = errors.New("1337x: no mirrors configured")
	}
	return lastErr
}

func (x *X1337) parseSearchPage(ctx context.Context, doc *goquery.Document, mirror string, out chan<- Result) error {
	var emitErr error
	doc.Find("table.table-list tbody tr").EachWithBreak(func(_ int, row *goquery.Selection) bool {
		link := row.Find("td.name a").FilterFunction(func(_ int, s *goquery.Selection) bool {
			href, _ := s.Attr("href")
			return strings.HasPrefix(href, "/torrent/")
		}).First()
		title := strings.TrimSpace(link.Text())
		href, _ := link.Attr("href")
		if title == "" || href == "" {
			return true
		}
		seeds, _ := strconv.Atoi(strings.TrimSpace(row.Find("td.seeds").Text()))
		leeches, _ := strconv.Atoi(strings.TrimSpace(row.Find("td.leeches").Text()))
		// size cell nests a span with the seeder count; keep only the leading text
		sizeCell := row.Find("td.size").Clone()
		sizeCell.Find("span").Remove()
		size := strings.TrimSpace(sizeCell.Text())
		r := Result{
			Title:     title,
			Size:      size,
			SizeBytes: parseHumanSize(size),
			Seeders:   seeds,
			Leechers:  leeches,
			DetailURL: mirror + href,
			Provider:  x.Name(),
		}
		if err := emit(ctx, out, r); err != nil {
			emitErr = err
			return false
		}
		return true
	})
	return emitErr
}

// ResolveMagnet fetches the detail page and extracts the magnet link.
func (x *X1337) ResolveMagnet(ctx context.Context, r Result) (string, error) {
	resp, err := fetch(ctx, x.client, r.DetailURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", fmt.Errorf("1337x: parse detail page: %w", err)
	}
	if isCloudflareChallenge(doc) {
		return "", fmt.Errorf("%s: %w", r.DetailURL, ErrBlocked)
	}
	magnet, ok := doc.Find(`a[href^="magnet:?"]`).First().Attr("href")
	if !ok {
		return "", errors.New("1337x: no magnet link on detail page")
	}
	return magnet, nil
}

func isCloudflareChallenge(doc *goquery.Document) bool {
	title := strings.ToLower(doc.Find("title").Text())
	return strings.Contains(title, "just a moment") || strings.Contains(title, "attention required")
}
