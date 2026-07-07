package provider

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// RSS is a generic RSS/Torznab provider. It is meant for feed/API-like sources
// so users can add indexers without writing another HTML scraper.
type RSS struct {
	client    *http.Client
	name      string
	searchURL string
}

func NewRSS(client *http.Client, name, searchURL string) *RSS {
	if client == nil {
		client = DefaultClient
	}
	return &RSS{client: client, name: name, searchURL: searchURL}
}

func (r *RSS) Name() string { return r.name }

type rssFeed struct {
	Items []rssItem `xml:"channel>item"`
}

type rssItem struct {
	Title      string         `xml:"title"`
	Link       string         `xml:"link"`
	GUID       string         `xml:"guid"`
	Enclosures []rssEnclosure `xml:"enclosure"`
	Attrs      []rssAttr      `xml:"attr"`
}

type rssEnclosure struct {
	URL    string `xml:"url,attr"`
	Length string `xml:"length,attr"`
}

type rssAttr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

func (r *RSS) Search(ctx context.Context, query string, out chan<- Result) error {
	if strings.TrimSpace(r.searchURL) == "" {
		return errors.New("rss: missing search_url")
	}
	u := renderSearchURL(r.searchURL, query)
	resp, err := fetch(ctx, r.client, u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var feed rssFeed
	if err := xml.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return fmt.Errorf("%s: decode rss: %w", r.name, err)
	}
	for _, it := range feed.Items {
		res, ok := r.resultFromItem(it)
		if !ok {
			continue
		}
		if err := emit(ctx, out, res); err != nil {
			return err
		}
	}
	return nil
}

func renderSearchURL(tmpl, query string) string {
	escaped := url.QueryEscape(query)
	if strings.Contains(tmpl, "{query}") {
		return strings.ReplaceAll(tmpl, "{query}", escaped)
	}
	if strings.Contains(tmpl, "%s") {
		return fmt.Sprintf(tmpl, escaped)
	}
	return tmpl
}

func (r *RSS) resultFromItem(it rssItem) (Result, bool) {
	title := strings.TrimSpace(it.Title)
	if title == "" {
		return Result{}, false
	}
	attrs := rssAttrs(it.Attrs)
	magnet := firstMagnet(it)
	if magnet == "" {
		if hash := attrs.first("infohash", "info_hash", "hash"); hash != "" {
			magnet = BuildMagnet(hash, title, DefaultTrackers)
		}
	}
	if magnet == "" {
		return Result{}, false
	}

	sizeBytes := attrs.int64("size", "length")
	if sizeBytes == 0 {
		sizeBytes = firstEnclosureLength(it.Enclosures)
	}
	size := attrs.first("size_text", "filesize")
	if size == "" && sizeBytes > 0 {
		size = humanSize(sizeBytes)
	}

	return Result{
		Title:     title,
		Size:      size,
		SizeBytes: sizeBytes,
		Seeders:   attrs.int("seeders", "seeds"),
		Leechers:  attrs.int("leechers", "peers"),
		Magnet:    magnet,
		Provider:  r.name,
		Trusted:   attrs.bool("trusted", "verified"),
	}, true
}

func firstMagnet(it rssItem) string {
	for _, s := range append([]string{it.Link, it.GUID}, enclosureURLs(it.Enclosures)...) {
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, "magnet:?") {
			return s
		}
	}
	return ""
}

func enclosureURLs(enclosures []rssEnclosure) []string {
	out := make([]string, 0, len(enclosures))
	for _, e := range enclosures {
		out = append(out, e.URL)
	}
	return out
}

func firstEnclosureLength(enclosures []rssEnclosure) int64 {
	for _, e := range enclosures {
		if n, err := strconv.ParseInt(strings.TrimSpace(e.Length), 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

type rssAttrs []rssAttr

func (a rssAttrs) first(names ...string) string {
	for _, want := range names {
		for _, attr := range a {
			if strings.EqualFold(attr.Name, want) {
				return strings.TrimSpace(attr.Value)
			}
		}
	}
	return ""
}

func (a rssAttrs) int(names ...string) int {
	n, _ := strconv.Atoi(a.first(names...))
	return n
}

func (a rssAttrs) int64(names ...string) int64 {
	n, _ := strconv.ParseInt(a.first(names...), 10, 64)
	return n
}

func (a rssAttrs) bool(names ...string) bool {
	v := strings.ToLower(a.first(names...))
	return v == "1" || v == "true" || v == "yes"
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
