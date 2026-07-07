package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// ytsMirrors are YTS domains tried in order. yts.mx is often DNS-blocked by
// ISPs, so the alternates matter for reliability.
var ytsMirrors = []string{
	"https://yts.mx",
	"https://yts.rs",
	"https://yts.lt",
	"https://yts.am",
}

type YTS struct {
	client *http.Client
	bases  []string

	mu     sync.Mutex
	active string // first mirror that worked this session
}

func NewYTS(client *http.Client, base string) *YTS {
	if client == nil {
		client = DefaultClient
	}
	// configured mirror first, then the built-in fallbacks (deduped)
	bases := []string{}
	seen := map[string]bool{}
	for _, b := range append([]string{strings.TrimRight(base, "/")}, ytsMirrors...) {
		if b != "" && !seen[b] {
			seen[b] = true
			bases = append(bases, b)
		}
	}
	return &YTS{client: client, bases: bases}
}

func (y *YTS) Name() string { return "yts" }

func (y *YTS) orderedBases() []string {
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.active == "" {
		return y.bases
	}
	out := []string{y.active}
	for _, b := range y.bases {
		if b != y.active {
			out = append(out, b)
		}
	}
	return out
}

type ytsResponse struct {
	Data struct {
		MovieCount int `json:"movie_count"`
		Movies     []struct {
			TitleLong string `json:"title_long"`
			Torrents  []struct {
				Hash      string `json:"hash"`
				Quality   string `json:"quality"`
				Size      string `json:"size"`
				SizeBytes int64  `json:"size_bytes"`
				Seeds     int    `json:"seeds"`
				Peers     int    `json:"peers"`
			} `json:"torrents"`
		} `json:"movies"`
	} `json:"data"`
}

func (y *YTS) Search(ctx context.Context, query string, out chan<- Result) error {
	var lastErr error
	for _, base := range y.orderedBases() {
		u := fmt.Sprintf("%s/api/v2/list_movies.json?query_term=%s&limit=50", base, url.QueryEscape(query))
		resp, err := fetch(ctx, y.client, u)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue // try the next mirror
		}
		y.mu.Lock()
		y.active = base
		y.mu.Unlock()
		err = y.emitResults(ctx, resp, out)
		resp.Body.Close()
		return err
	}
	return lastErr
}

func (y *YTS) emitResults(ctx context.Context, resp *http.Response, out chan<- Result) error {
	var body ytsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("yts: decode: %w", err)
	}
	for _, m := range body.Data.Movies {
		for _, t := range m.Torrents {
			title := m.TitleLong + " [" + t.Quality + "]"
			r := Result{
				Title:     title,
				Size:      t.Size,
				SizeBytes: t.SizeBytes,
				Seeders:   t.Seeds,
				Leechers:  t.Peers,
				Magnet:    BuildMagnet(t.Hash, title, DefaultTrackers),
				Provider:  y.Name(),
			}
			if err := emit(ctx, out, r); err != nil {
				return err
			}
		}
	}
	return nil
}
