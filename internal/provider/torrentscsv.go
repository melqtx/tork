package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// TorrentsCSV queries Torrents.csv, an open-source, community-maintained
// torrent index (codeberg.org/heretic/torrents-csv-server). It only keeps
// torrents that still have seeders and periodically re-scrapes swarm counts,
// so its JSON API returns infohashes with live seeder numbers.
const torrentsCSVWeb = "https://torrents-csv.com"

type TorrentsCSV struct {
	client *http.Client
	base   string
}

func NewTorrentsCSV(client *http.Client, base string) *TorrentsCSV {
	if client == nil {
		client = DefaultClient
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = torrentsCSVWeb
	}
	return &TorrentsCSV{client: client, base: base}
}

func (t *TorrentsCSV) Name() string { return "torrentscsv" }

type torrentsCSVResponse struct {
	Torrents *[]struct {
		InfoHash  string `json:"infohash"`
		Name      string `json:"name"`
		SizeBytes int64  `json:"size_bytes"`
		Seeders   int    `json:"seeders"`
		Leechers  int    `json:"leechers"`
	} `json:"torrents"`
}

func (t *TorrentsCSV) Search(ctx context.Context, query string, out chan<- Result) error {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	u := fmt.Sprintf("%s/service/search?q=%s&size=50", t.base, url.QueryEscape(q))
	resp, err := fetch(ctx, t.client, u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var body torrentsCSVResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("torrentscsv: decode: %w", err)
	}
	// A missing "torrents" key is not a valid empty search.
	if body.Torrents == nil {
		return ErrResponseFormat
	}
	emitted := 0
	for _, it := range *body.Torrents {
		name := strings.TrimSpace(it.Name)
		if name == "" {
			continue
		}
		magnet := BuildMagnet(it.InfoHash, name, DefaultTrackers)
		if (Result{Magnet: magnet}).InfoHash() == "" {
			continue
		}
		r := Result{
			Title:     name,
			Size:      humanSize(it.SizeBytes),
			SizeBytes: it.SizeBytes,
			Seeders:   it.Seeders,
			Leechers:  it.Leechers,
			Magnet:    magnet,
			Provider:  t.Name(),
		}
		if err := emit(ctx, out, r); err != nil {
			return err
		}
		emitted++
	}
	if emitted == 0 && len(*body.Torrents) > 0 {
		return ErrResponseFormat
	}
	return nil
}
