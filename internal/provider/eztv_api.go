package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var eztvTech = regexp.MustCompile(`(?i)(?:^|[ ._-]+)(?:s\d{1,2}(?:e\d{1,3})?|\d{1,2}x\d{1,3}|(?:2160|1080|720|576|480)p|web[ ._-]?(?:dl|rip)|hdtv|blu[ ._-]?ray|x26[45]|hevc)\b.*$`)
var eztvYear = regexp.MustCompile(`\s((?:19|20)\d{2})$`)
var eztvIMDb = regexp.MustCompile(`(?i)^tt(\d{7,10})$`)

type eztvAPIResponse struct {
	Count    *int `json:"torrents_count"`
	Torrents []struct {
		Hash     string    `json:"hash"`
		Title    string    `json:"title"`
		Filename string    `json:"filename"`
		Magnet   string    `json:"magnet_url"`
		Size     apiNumber `json:"size_bytes"`
		Seeds    apiNumber `json:"seeds"`
		Peers    apiNumber `json:"peers"`
	} `json:"torrents"`
}

// EZTV's API accepts IMDb IDs, not text search. Resolve the requested show
// first, then filter its own releases; never present an unrelated latest feed
// as the answer to a title search. Browse/quality-only searches use latest rows.
func (e *EZTV) searchAPI(ctx context.Context, query string, out chan<- Result) error {
	query = strings.TrimSpace(query)
	filter := query
	name := strings.TrimSpace(eztvTech.ReplaceAllString(query, ""))
	name = strings.Join(strings.Fields(strings.NewReplacer(".", " ", "_", " ", "-", " ").Replace(name)), " ")
	year := ""
	if match := eztvYear.FindStringSubmatch(name); match != nil {
		year = match[1]
		name = strings.TrimSpace(eztvYear.ReplaceAllString(name, ""))
		filter = strings.TrimSpace(strings.Replace(filter, year, "", 1))
	}
	var ids []string
	if match := eztvIMDb.FindStringSubmatch(query); match != nil {
		ids = []string{match[1]}
		filter = ""
	} else if name != "" {
		var err error
		ids, err = e.lookupShows(ctx, name, year)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			// Verify the source itself before reporting a healthy empty result.
			_, _, err = e.apiPage(ctx, "", 1, 1)
			return err
		}
	}
	if len(ids) == 0 {
		ids = []string{""}
	}
	seen := map[string]bool{}
	for _, id := range ids {
		for page := 1; page <= 2; page++ {
			rows, total, err := e.apiPage(ctx, id, page, 100)
			if err != nil {
				return err
			}
			for _, r := range rows {
				if !eztvMatchesQuery(r.Title, filter) || seen[r.InfoHash()] {
					continue
				}
				seen[r.InfoHash()] = true
				if err := emit(ctx, out, r); err != nil {
					return err
				}
			}
			if id == "" || page*100 >= total {
				break
			}
		}
	}
	return nil
}

func eztvMatchesQuery(title, query string) bool {
	normalize := strings.NewReplacer(".", " ", "_", " ", "-", " ")
	return matchesQuery(normalize.Replace(title), normalize.Replace(query))
}

func (e *EZTV) lookupShows(ctx context.Context, name, year string) ([]string, error) {
	resp, err := fetch(ctx, e.client, e.lookupBase+"/search/shows?q="+url.QueryEscape(name))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var shows []struct {
		Show struct {
			Name      string `json:"name"`
			Premiered string `json:"premiered"`
			Externals struct {
				IMDb string `json:"imdb"`
			} `json:"externals"`
		} `json:"show"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&shows); err != nil {
		return nil, fmt.Errorf("eztv: show lookup: %w", ErrResponseFormat)
	}
	if shows == nil {
		return nil, ErrResponseFormat
	}
	var ids []string
	seen := map[string]bool{}
	for _, candidate := range shows {
		s := candidate.Show
		if !matchesQuery(s.Name, name) || year != "" && !strings.HasPrefix(s.Premiered, year+"-") {
			continue
		}
		match := eztvIMDb.FindStringSubmatch(s.Externals.IMDb)
		if match == nil || seen[match[1]] {
			continue
		}
		seen[match[1]] = true
		ids = append(ids, match[1])
		if len(ids) == 3 {
			break
		}
	}
	return ids, nil
}

func (e *EZTV) apiPage(ctx context.Context, id string, page, limit int) ([]Result, int, error) {
	q := url.Values{"limit": {fmt.Sprint(limit)}, "page": {fmt.Sprint(page)}}
	if id != "" {
		q.Set("imdb_id", id)
	}
	resp, err := fetch(ctx, e.client, e.apiBase+"/api/get-torrents?"+q.Encode())
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var body eztvAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, 0, ErrResponseFormat
	}
	if body.Count == nil || *body.Count < 0 {
		return nil, 0, ErrResponseFormat
	}
	if *body.Count > 0 && len(body.Torrents) == 0 {
		return nil, 0, ErrResponseFormat
	}
	var rows []Result
	for _, t := range body.Torrents {
		title := strings.TrimSpace(t.Title)
		if title == "" {
			title = strings.TrimSpace(t.Filename)
		}
		magnet := t.Magnet
		if (Result{Magnet: magnet}).InfoHash() == "" {
			magnet = BuildMagnet(t.Hash, title, DefaultTrackers)
		}
		if title == "" || (Result{Magnet: magnet}).InfoHash() == "" {
			continue
		}
		size := int64Default(string(t.Size))
		rows = append(rows, Result{Title: title, Magnet: magnet, Size: humanSize(size), SizeBytes: size, Seeders: atoiDefault(string(t.Seeds)), Leechers: atoiDefault(string(t.Peers)), Provider: e.Name(), Category: "TV"})
	}
	if len(body.Torrents) > 0 && len(rows) == 0 {
		return nil, 0, ErrResponseFormat
	}
	return rows, *body.Count, nil
}
