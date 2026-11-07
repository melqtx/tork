package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// InternetArchive searches archive.org, the non-profit digital library. Every
// public item is also published as a torrent that the Archive seeds itself,
// and the advanced-search API returns that torrent's infohash (btih), so no
// extra request is needed per result. The content is public domain or shared
// with the rights holders' permission: films, audio, software, books.
const internetArchiveWeb = "https://archive.org"

// archiveTrackers are the Archive's own trackers. The magnet also carries the
// Archive's download URL as a BEP 19 webseed (ws=), which anacrolix/torrent
// honours, so data still flows over HTTPS when the swarm is thin.
var archiveTrackers = []string{
	"http://bt1.archive.org:6969/announce",
	"http://bt2.archive.org:6969/announce",
}

type InternetArchive struct {
	client *http.Client
	base   string
}

func NewInternetArchive(client *http.Client, base string) *InternetArchive {
	if client == nil {
		client = DefaultClient
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = internetArchiveWeb
	}
	return &InternetArchive{client: client, base: base}
}

func (a *InternetArchive) Name() string { return "archive" }

type archiveResponse struct {
	Response *struct {
		Docs []struct {
			Identifier string          `json:"identifier"`
			Title      json.RawMessage `json:"title"`
			MediaType  string          `json:"mediatype"`
			ItemSize   int64           `json:"item_size"`
			BTIH       string          `json:"btih"`
		} `json:"docs"`
	} `json:"response"`
}

// archiveCategories maps Archive media types onto labels the browse filter
// recognises (see tui.browseCategory). The Archive calls all moving images
// "movies".
var archiveCategories = map[string]string{
	"movies":   "Movies",
	"audio":    "Audio",
	"etree":    "Audio",
	"texts":    "Books",
	"software": "Software",
	"image":    "Images",
	"data":     "Other",
}

func (a *InternetArchive) Search(ctx context.Context, query string, out chan<- Result) error {
	q := luceneTerms(query)
	if q == "" {
		return nil
	}
	// A bare query also matches descriptions, full text and reviews, which
	// buries the obvious item; matching titles and ranking by downloads puts
	// the well-known copy first. Collections, accounts and web captures are
	// not downloadable items.
	search := "title:(" + q + ") AND -mediatype:(collection OR account OR web OR search)"
	params := url.Values{}
	params.Set("q", search)
	for _, f := range []string{"identifier", "title", "mediatype", "item_size", "btih"} {
		params.Add("fl[]", f)
	}
	params.Set("sort[]", "downloads desc")
	params.Set("rows", "50")
	params.Set("output", "json")
	u := a.base + "/advancedsearch.php?" + params.Encode()

	resp, err := fetch(ctx, a.client, u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var body archiveResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("archive: decode: %w", err)
	}
	if body.Response == nil {
		return ErrResponseFormat
	}
	for _, d := range body.Response.Docs {
		// Items without a torrent (dark, restricted or still processing)
		// carry no btih; they cannot be fetched over BitTorrent.
		if d.Identifier == "" || d.BTIH == "" {
			continue
		}
		title := archiveTitle(d.Title)
		if title == "" {
			title = d.Identifier
		}
		magnet := BuildMagnet(d.BTIH, d.Identifier, archiveTrackers) +
			"&ws=" + url.QueryEscape(a.base+"/download/")
		if (Result{Magnet: magnet}).InfoHash() == "" {
			continue
		}
		r := Result{
			Title:     title,
			SizeBytes: d.ItemSize,
			Magnet:    magnet,
			Provider:  a.Name(),
			Trusted:   true, // seeded by the Archive itself
			Category:  archiveCategories[d.MediaType],
		}
		if d.ItemSize > 0 {
			r.Size = humanSize(d.ItemSize)
		}
		if err := emit(ctx, out, r); err != nil {
			return err
		}
	}
	return nil
}

// luceneTerms turns user input into plain search words for the Archive's
// Lucene query, so characters such as ':', '"' or '(' cannot change the
// query's structure. They are dropped rather than backslash-escaped: the
// Archive's backend rejects some escapes (`c\+\+` fails), and its title
// analyzer ignores punctuation anyway. Lowercasing keeps words like AND, OR
// and NOT from acting as operators; title matching is case-insensitive.
func luceneTerms(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`+-&|!(){}[]^"~*?:\/`, r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// archiveTitle accepts the Archive's title field as a string or, for some
// older items, an array of strings.
func archiveTitle(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil && len(list) > 0 {
		return strings.TrimSpace(list[0])
	}
	return ""
}
