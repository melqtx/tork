package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

// RARBG uses TheRARBG's public search API. This is TheRARBG, not the original
// RARBG service. No account or separate indexer manager is required.
type RARBG struct {
	client *http.Client
	bases  []string
}

func NewRARBG(client *http.Client, bases ...string) *RARBG {
	if client == nil {
		client = DefaultClient
	}
	if len(bases) == 0 {
		bases = []string{"https://therarbg.to", "https://therarbg.com"}
	}
	return &RARBG{client: client, bases: bases}
}
func (*RARBG) Name() string { return "therarbg" }

type rarbgItem struct {
	Title    string `json:"n"`
	Category string `json:"c"`
	Hash     string `json:"h"`
	Size     int64  `json:"s"`
	Seeders  int    `json:"se"`
	Leechers int    `json:"le"`
}

func (p *RARBG) Search(ctx context.Context, query string, out chan<- Result) error {
	// Colons delimit API options. Keep search text from becoming API syntax, and
	// normalize punctuation the same way the site's free-text search does.
	query = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, query)), " ")
	if query == "" {
		return nil
	}
	var lastErr error
	for _, base := range p.bases {
		u := strings.TrimRight(base, "/") + "/get-posts/order:-se:keywords:" + url.PathEscape(query) + ":paginate_by:100:format:json/"
		resp, err := fetch(ctx, p.client, u)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		var body struct {
			Results *[]rarbgItem `json:"results"`
		}
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if err != nil || body.Results == nil {
			lastErr = ErrResponseFormat
			continue
		}
		emitted := 0
		for _, item := range *body.Results {
			if strings.TrimSpace(item.Title) == "" {
				continue
			}
			magnet := BuildMagnet(item.Hash, item.Title, DefaultTrackers)
			if (Result{Magnet: magnet}).InfoHash() == "" || strings.EqualFold(item.Hash, strings.Repeat("0", 40)) {
				continue
			}
			r := Result{Title: item.Title, Category: item.Category, Magnet: magnet, SizeBytes: max(0, item.Size), Seeders: max(0, item.Seeders), Leechers: max(0, item.Leechers), Provider: p.Name()}
			if r.SizeBytes > 0 {
				r.Size = humanSize(r.SizeBytes)
			}
			if err := emit(ctx, out, r); err != nil {
				return err
			}
			emitted++
		}
		if len(*body.Results) > 0 && emitted == 0 {
			lastErr = ErrResponseFormat
			continue
		}
		return nil
	}
	if lastErr == nil {
		return ErrResponseFormat
	}
	return lastErr
}
