package provider

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type Nyaa struct {
	client *http.Client
	base   string
}

func NewNyaa(client *http.Client, base string) *Nyaa {
	if client == nil {
		client = DefaultClient
	}
	if base == "" {
		base = "https://nyaa.si"
	}
	return &Nyaa{client: client, base: base}
}

func (n *Nyaa) Name() string { return "nyaa" }

// nyaaItem matches the nyaa: namespace (xmlns:nyaa="https://nyaa.si/xmlns/nyaa").
// Go's decoder matches on the namespace URI, so the struct tags carry it.
type nyaaItem struct {
	Title    string `xml:"title"`
	Seeders  string `xml:"https://nyaa.si/xmlns/nyaa seeders"`
	Leechers string `xml:"https://nyaa.si/xmlns/nyaa leechers"`
	Size     string `xml:"https://nyaa.si/xmlns/nyaa size"`
	InfoHash string `xml:"https://nyaa.si/xmlns/nyaa infoHash"`
	Trusted  string `xml:"https://nyaa.si/xmlns/nyaa trusted"`
}

type nyaaFeed struct {
	Items []nyaaItem `xml:"channel>item"`
}

func (n *Nyaa) Search(ctx context.Context, query string, out chan<- Result) error {
	u := fmt.Sprintf("%s/?page=rss&q=%s", n.base, url.QueryEscape(query))
	resp, err := fetch(ctx, n.client, u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var feed nyaaFeed
	if err := xml.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return fmt.Errorf("nyaa: decode rss: %w", err)
	}
	for _, it := range feed.Items {
		if it.InfoHash == "" {
			continue
		}
		seeders, _ := strconv.Atoi(it.Seeders)
		leechers, _ := strconv.Atoi(it.Leechers)
		r := Result{
			Title:     it.Title,
			Size:      it.Size,
			SizeBytes: parseHumanSize(it.Size),
			Seeders:   seeders,
			Leechers:  leechers,
			Magnet:    BuildMagnet(it.InfoHash, it.Title, DefaultTrackers),
			Provider:  n.Name(),
			Trusted:   strings.EqualFold(strings.TrimSpace(it.Trusted), "yes"),
		}
		if err := emit(ctx, out, r); err != nil {
			return err
		}
	}
	return nil
}

// parseHumanSize converts strings like "1.2 GiB" / "700 MB" to bytes (0 on failure).
func parseHumanSize(s string) int64 {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) != 2 {
		return 0
	}
	val, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	var mult float64
	switch strings.ToUpper(fields[1]) {
	case "B":
		mult = 1
	case "KB", "KIB":
		mult = 1 << 10
	case "MB", "MIB":
		mult = 1 << 20
	case "GB", "GIB":
		mult = 1 << 30
	case "TB", "TIB":
		mult = 1 << 40
	default:
		return 0
	}
	return int64(val * mult)
}
