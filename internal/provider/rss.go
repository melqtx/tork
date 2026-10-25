package provider

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
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
	torznab   bool
	apiKey    string
}

func NewRSS(client *http.Client, name, searchURL string) *RSS {
	if client == nil {
		client = DefaultClient
	}
	return &RSS{client: client, name: name, searchURL: searchURL}
}

// NewTorznab accepts an API endpoint, including existing query parameters.
// Query construction preserves escaped API keys and adds the standard search
// parameters without requiring users to hand-write a URL template.
func NewTorznab(client *http.Client, name, endpoint, apiKey string) *RSS {
	r := NewRSS(client, name, endpoint)
	r.torznab, r.apiKey = true, apiKey
	return r
}

func (r *RSS) Name() string { return r.name }

type rssFeed struct {
	XMLName xml.Name
	Code    string `xml:"code,attr"`
	Channel *struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title      string         `xml:"title"`
	Link       string         `xml:"link"`
	GUID       string         `xml:"guid"`
	Category   string         `xml:"category"`
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
	if r.torznab {
		parsed, err := url.Parse(u)
		if err != nil {
			return ErrFeedFormat
		}
		q := parsed.Query()
		q.Set("t", "search")
		q.Set("q", query)
		q.Set("extended", "1")
		if r.apiKey != "" {
			q.Set("apikey", r.apiKey)
		}
		parsed.RawQuery = q.Encode()
		u = parsed.String()
	}
	resp, err := fetch(ctx, r.client, u)
	if err != nil {
		// Never surface URLs containing credentials or raw parser messages.
		if errors.Is(err, ErrBlocked) {
			return ErrBlocked
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			return context.DeadlineExceeded
		}
		if strings.Contains(err.Error(), "unexpected status 401") {
			return ErrFeedAuth
		}
		return errors.New("feed request failed")
	}
	defer resp.Body.Close()

	var feed rssFeed
	decoder := xml.NewDecoder(resp.Body)
	if err := decoder.Decode(&feed); err != nil {
		return ErrFeedFormat
	}
	if feed.XMLName.Local == "error" {
		switch feed.Code {
		case "100", "101", "102":
			return ErrFeedAuth
		}
		return ErrFeedAPI
	}
	if feed.XMLName.Local != "rss" || feed.Channel == nil {
		return ErrFeedFormat
	}
	// Reject extra documents and truncated/oversized bodies as malformed, rather
	// than treating a valid prefix as a successful check.
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ErrFeedFormat
		}
		if text, ok := token.(xml.CharData); ok && strings.TrimSpace(string(text)) == "" {
			continue
		}
		switch token.(type) {
		case xml.Comment, xml.ProcInst:
			continue
		}
		return ErrFeedFormat
	}
	unsupported := false
	for _, it := range feed.Channel.Items {
		res, ok := r.resultFromItem(it)
		if !ok {
			unsupported = true
			continue
		}
		if err := emit(ctx, out, res); err != nil {
			return err
		}
	}
	if unsupported {
		return ErrFeedUnsupported
	}
	return nil
}

func renderSearchURL(tmpl, query string) string {
	escaped := url.QueryEscape(query)
	if strings.Contains(tmpl, "{query}") {
		return strings.ReplaceAll(tmpl, "{query}", escaped)
	}
	if strings.Contains(tmpl, "%s") {
		return strings.ReplaceAll(tmpl, "%s", escaped)
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
		magnet = attrs.first("magneturl", "magnet")
	}
	if magnet == "" {
		if hash := attrs.first("infohash", "info_hash", "hash"); hash != "" {
			// A custom source's hash alone does not authorize announcing it to
			// public trackers. Preserve source-supplied trackers instead.
			magnet = BuildMagnet(hash, title, nil)
		}
	}
	if (Result{Magnet: magnet}).InfoHash() == "" {
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
		Category:  rssCategory(it, attrs),
	}, true
}

// rssCategory resolves a listing's category from Torznab attrs or a plain RSS
// <category>. Newznab/Torznab category IDs put all adult content in the
// 6000-6999 range, which we normalize to "XXX" so the shared content filter
// catches it; otherwise any non-numeric label is passed through as-is.
func rssCategory(it rssItem, attrs rssAttrs) string {
	values := attrs.all("category", "cat")
	if cat := strings.TrimSpace(it.Category); cat != "" {
		values = append(values, cat)
	}
	var label string
	for _, v := range values {
		v = strings.TrimSpace(v)
		if n, err := strconv.Atoi(v); err == nil {
			if n >= 6000 && n < 7000 {
				return "XXX"
			}
			continue // a numeric non-adult category carries no useful label
		}
		if adultCategories[strings.ToLower(v)] {
			return "XXX"
		}
		if v != "" && label == "" {
			label = v
		}
	}
	return label
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

func (a rssAttrs) all(names ...string) []string {
	var out []string
	for _, attr := range a {
		for _, want := range names {
			if strings.EqualFold(attr.Name, want) {
				out = append(out, strings.TrimSpace(attr.Value))
			}
		}
	}
	return out
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
