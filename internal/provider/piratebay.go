package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// pirateBayMirrors are apibay-compatible hosts tried in order. apibay.org is
// the canonical one; ISPs frequently block it, so the fallbacks matter.
var pirateBayMirrors = []string{
	"https://apibay.org",
	"https://thepiratebay.zone/apibay",
	"https://piratebay.party/apibay",
}

var (
	tpbMovieCats = map[int]bool{201: true, 202: true, 207: true, 209: true}
	tpbTVCats    = map[int]bool{205: true, 208: true}
)

type PirateBay struct {
	client    *http.Client
	name      string
	bases     []string
	cats      map[int]bool
	browseURL string
}

// NewPirateBay searches every category; legacy movie/TV sources retain their
// existing IDs and filters so upgrades preserve previously configured choices.
func NewPirateBay(client *http.Client, bases ...string) *PirateBay {
	return newPirateBay(client, "tpb", bases, nil, "/precompiled/data_top100_all.json")
}

func NewPirateBayMovies(client *http.Client, bases ...string) *PirateBay {
	return newPirateBay(client, "tpb-movies", bases, tpbMovieCats, "/precompiled/data_top100_207.json")
}

func NewPirateBayTV(client *http.Client, bases ...string) *PirateBay {
	return newPirateBay(client, "tpb-tv", bases, tpbTVCats, "/precompiled/data_top100_208.json")
}

func newPirateBay(client *http.Client, name string, bases []string, cats map[int]bool, browseURL string) *PirateBay {
	if client == nil {
		client = DefaultClient
	}
	// keep only non-empty configured bases; fall back to the built-in mirrors
	var clean []string
	for _, b := range bases {
		if strings.TrimSpace(b) != "" {
			clean = append(clean, strings.TrimRight(b, "/"))
		}
	}
	if len(clean) == 0 {
		clean = pirateBayMirrors
	}
	return &PirateBay{client: client, name: name, bases: clean, cats: cats, browseURL: browseURL}
}

func (p *PirateBay) Name() string { return p.name }

type apibayItem struct {
	ID       apiNumber `json:"id"`
	Name     string    `json:"name"`
	InfoHash string    `json:"info_hash"`
	Seeders  apiNumber `json:"seeders"`
	Leechers apiNumber `json:"leechers"`
	Size     apiNumber `json:"size"`
	Added    apiNumber `json:"added"`
	Category apiNumber `json:"category"`
	Status   string    `json:"status"` // vip / trusted / member / ""
	Username string    `json:"username"`
}

func (p *PirateBay) Search(ctx context.Context, query string, out chan<- Result) error {
	q := strings.TrimSpace(query)

	var lastErr error
	for _, base := range p.bases {
		u := base + p.browseURL
		if q != "" {
			// apibay's q.php wants %20 for spaces (url.QueryEscape uses '+',
			// which some backends treat literally and return nothing).
			u = base + "/q.php?q=" + queryEscapeSpace(q)
		}
		items, err := p.fetchItems(ctx, u)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue // try next mirror
		}
		emitted := 0
		eligible := 0
		for _, it := range items {
			if it.ID == "0" && it.InfoHash == strings.Repeat("0", 40) {
				continue
			}
			if q != "" && p.cats != nil && !p.cats[atoiDefault(string(it.Category))] {
				continue
			}
			eligible++
			r, ok := p.resultFromItem(it)
			if !ok {
				continue
			}
			if err := emit(ctx, out, r); err != nil {
				return err
			}
			emitted++
		}
		if eligible > 0 && emitted == 0 {
			lastErr = ErrResponseFormat
			continue
		}
		return nil // first working mirror wins
	}
	return lastErr
}

func (p *PirateBay) fetchItems(ctx context.Context, u string) ([]apibayItem, error) {
	resp, err := fetch(ctx, p.client, u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var items []apibayItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("%s: decode api: %w", p.name, err)
	}
	if items == nil {
		return nil, ErrResponseFormat
	}
	if len(items) > 0 {
		valid := false
		for _, item := range items {
			if _, ok := p.resultFromItem(item); ok {
				valid = true
				break
			}
		}
		emptySentinel := len(items) == 1 && items[0].ID == "0" && items[0].InfoHash == strings.Repeat("0", 40)
		if !valid && !emptySentinel {
			return nil, ErrResponseFormat
		}
	}
	return items, nil
}

// queryEscapeSpace percent-encodes a query but emits %20 for spaces.
func queryEscapeSpace(q string) string {
	return strings.ReplaceAll(url.QueryEscape(q), "+", "%20")
}

func (p *PirateBay) resultFromItem(it apibayItem) (Result, bool) {
	hash := strings.TrimSpace(it.InfoHash)
	if hash == "" || hash == "0000000000000000000000000000000000000000" || it.ID == "0" {
		return Result{}, false
	}
	title := strings.TrimSpace(it.Name)
	if title == "" {
		title = hash
	}
	magnet := BuildMagnet(hash, title, DefaultTrackers)
	if (Result{Magnet: magnet}).InfoHash() == "" {
		return Result{}, false
	}
	sizeBytes := int64Default(string(it.Size))
	return Result{
		Title:     title,
		Size:      humanSize(sizeBytes),
		SizeBytes: sizeBytes,
		Seeders:   atoiDefault(string(it.Seeders)),
		Leechers:  atoiDefault(string(it.Leechers)),
		Magnet:    magnet,
		Provider:  p.name,
		Category:  pirateBayCategory(atoiDefault(string(it.Category))),
		Trusted:   isTrustedTPBStatus(it.Status),
	}, true
}

// isTrustedTPBStatus reports whether apibay's uploader status marks a release
// as vetted: VIP and Trusted are the site's own verified-uploader badges.
func isTrustedTPBStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "vip", "trusted":
		return true
	}
	return false
}

func atoiDefault(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func int64Default(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

func pirateBayCategory(id int) string {
	if tpbMovieCats[id] {
		return "Movies"
	}
	if tpbTVCats[id] {
		return "TV"
	}
	switch id / 100 {
	case 1:
		return "Music"
	case 2:
		return "Video"
	case 3:
		return "Applications"
	case 4:
		return "Games"
	case 5:
		return "XXX"
	case 6:
		return "Other"
	}
	return ""
}

// The search API returns numeric strings, while precompiled browse responses
// use JSON numbers. Both represent the same fields.
type apiNumber string

func (n *apiNumber) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*n = ""
		return nil
	}
	var str string
	if len(data) > 0 && data[0] == '"' {
		if err := json.Unmarshal(data, &str); err != nil {
			return err
		}
		*n = apiNumber(str)
		return nil
	}
	var num json.Number
	if err := json.Unmarshal(data, &num); err != nil {
		return err
	}
	*n = apiNumber(num.String())
	return nil
}
