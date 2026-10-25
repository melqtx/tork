package provider

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// LimeTorrents searches the results table, excluding sponsored download links.
// Only a selected result's detail page is fetched to obtain its magnet.
type LimeTorrents struct {
	client *http.Client
	bases  []string
}

func NewLimeTorrents(client *http.Client, bases ...string) *LimeTorrents {
	if client == nil {
		client = DefaultClient
	}
	if len(bases) == 0 {
		bases = []string{"https://www.limetorrents.fun"}
	}
	return &LimeTorrents{client: client, bases: bases}
}
func (*LimeTorrents) Name() string { return "limetorrents" }

func (p *LimeTorrents) Search(ctx context.Context, query string, out chan<- Result) error {
	if strings.TrimSpace(query) == "" {
		return nil
	}
	var lastErr error
	for _, base := range p.bases {
		base = strings.TrimRight(base, "/")
		resp, err := fetch(ctx, p.client, base+"/search/all/"+url.PathEscape(strings.TrimSpace(query))+"/seeds/1/")
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		doc, err := goquery.NewDocumentFromReader(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = ErrResponseFormat
			continue
		}
		if isCloudflareChallenge(doc) {
			lastErr = ErrBlocked
			continue
		}
		if doc.Find("table.table2").Length() == 0 {
			lastErr = ErrResponseFormat
			continue
		}
		var emitErr error
		doc.Find("table.table2 tr").EachWithBreak(func(_ int, row *goquery.Selection) bool {
			link := row.Find(`div.tt-name > a[href*="-torrent-"]`).First()
			href, _ := link.Attr("href")
			title := strings.TrimSpace(link.Text())
			if title == "" || href == "" {
				return true
			}
			origin, _ := url.Parse(base)
			relative, err := url.Parse(href)
			if err != nil {
				return true
			}
			detail := origin.ResolveReference(relative)
			if detail.Scheme != origin.Scheme || detail.Host != origin.Host || !strings.HasSuffix(detail.Path, ".html") {
				return true
			}
			cells := row.Find("td")
			size := strings.TrimSpace(cells.Eq(2).Text())
			category := ""
			added := strings.TrimSpace(cells.Eq(1).Text())
			if i := strings.LastIndex(added, " in "); i >= 0 {
				category = strings.TrimSpace(strings.TrimSuffix(added[i+4:], "."))
			}
			r := Result{Title: title, DetailURL: detail.String(), Provider: p.Name(), Category: category, Size: size, SizeBytes: parseHumanSize(size), Seeders: commaCount(row.Find(".tdseed").Text()), Leechers: commaCount(row.Find(".tdleech").Text())}
			if err := emit(ctx, out, r); err != nil {
				emitErr = err
				return false
			}
			return true
		})
		return emitErr
	}
	if lastErr == nil {
		return ErrResponseFormat
	}
	return lastErr
}

func (p *LimeTorrents) ResolveMagnet(ctx context.Context, r Result) (string, error) {
	target, err := url.Parse(r.DetailURL)
	if err != nil {
		return "", ErrResponseFormat
	}
	allowed := false
	for _, base := range p.bases {
		u, err := url.Parse(base)
		if err == nil && target.Host == u.Host && target.Scheme == u.Scheme {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", errors.New("detail page does not belong to this source")
	}
	resp, err := fetch(ctx, p.client, r.DetailURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", ErrResponseFormat
	}
	if isCloudflareChallenge(doc) {
		return "", ErrBlocked
	}
	var magnet string
	doc.Find(`a[href^="magnet:?"]`).EachWithBreak(func(_ int, a *goquery.Selection) bool {
		candidate, _ := a.Attr("href")
		if (Result{Magnet: candidate}).InfoHash() != "" {
			magnet = candidate
			return false
		}
		return true
	})
	if magnet == "" {
		return "", errors.New("no usable magnet on detail page")
	}
	return magnet, nil
}

func commaCount(text string) int {
	n, _ := strconv.Atoi(strings.ReplaceAll(strings.TrimSpace(text), ",", ""))
	return max(0, n)
}
