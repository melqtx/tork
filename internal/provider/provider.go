// Package provider defines the torrent search provider contract and implementations.
package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"
)

// ErrBlocked marks a provider rejected by an anti-bot layer (e.g. Cloudflare).
// The aggregator does not retry it.
var ErrBlocked = errors.New("blocked by site protection")

// maxResponseBytes caps any single provider response. Search results are
// always small (a few hundred KB); this guards against OOM from a broken or
// hostile endpoint streaming an unbounded body. Var (not const) so tests can
// lower it.
var maxResponseBytes int64 = 32 << 20 // 32 MiB

type Result struct {
	Title     string
	Size      string // human string as scraped
	SizeBytes int64
	Seeders   int
	Leechers  int
	Magnet    string // empty when only a detail page is known
	DetailURL string // set by providers that resolve magnets lazily
	Provider  string
	Trusted   bool     // provider flagged this as a trusted/verified release
	Category  string   // provider's category label when known (e.g. "Movies", "XXX")
	AlsoOn    []string // other providers that listed this same infohash (see Merge)
}

// SanitizeResult bounds and strips terminal control sequences from text that
// came from a remote index. URLs and magnets stay byte-for-byte intact; only
// fields rendered in the terminal are normalized.
func SanitizeResult(r Result) Result {
	r.Title = SanitizeDisplayText(r.Title, 512)
	r.Size = SanitizeDisplayText(r.Size, 64)
	r.Provider = SanitizeDisplayText(r.Provider, 80)
	r.Category = SanitizeDisplayText(r.Category, 120)
	r.AlsoOn = sanitizeDisplayList(r.AlsoOn, 64, 80)
	return r
}

// SanitizeDisplayText makes untrusted catalog text inert in a terminal. It
// removes C0/C1 and bidirectional formatting controls, collapses whitespace,
// and applies a rune bound so one remote field cannot dominate rendering.
func SanitizeDisplayText(raw string, limit int) string {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || isBidiControl(r) {
			return ' '
		}
		return r
	}, raw)
	clean = strings.Join(strings.Fields(clean), " ")
	runes := []rune(clean)
	if limit > 0 && len(runes) > limit {
		clean = string(runes[:limit])
	}
	return clean
}

func sanitizeDisplayList(values []string, maxItems, maxRunes int) []string {
	if len(values) > maxItems {
		values = values[:maxItems]
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = SanitizeDisplayText(value, maxRunes); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func isBidiControl(r rune) bool {
	return r == '\u200e' || r == '\u200f' ||
		(r >= '\u202a' && r <= '\u202e') ||
		(r >= '\u2066' && r <= '\u2069')
}

// Key identifies a result for deduplication across retries.
func (r Result) Key() string {
	if r.Magnet != "" {
		return r.Provider + "|" + r.Magnet
	}
	return r.Provider + "|" + r.DetailURL
}

// Provider streams search results onto out. Implementations must respect ctx,
// must NOT close out (the aggregator owns it), and return the first fatal
// error (nil on success, even with zero results).
type Provider interface {
	Name() string
	Search(ctx context.Context, query string, out chan<- Result) error
}

func DisplayName(p Provider) string {
	name := SanitizeDisplayText(p.Name(), 80)
	if name == "" {
		return "provider"
	}
	return name
}

// MagnetResolver is implemented by providers whose results carry only a
// DetailURL; the magnet is fetched on demand when the user selects the row.
type MagnetResolver interface {
	ResolveMagnet(ctx context.Context, r Result) (string, error)
}

// DefaultClient is shared by all providers: a bounded timeout and a redirect
// cap so a redirect loop can't hang or spin forever.
var DefaultClient = &http.Client{
	Timeout: 25 * time.Second,
	CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) >= 6 {
			return errors.New("stopped after 6 redirects")
		}
		return nil
	},
}

// limitedBody wraps a response body so reads stop at maxResponseBytes while
// still closing the underlying connection.
type limitedBody struct {
	io.Reader
	io.Closer
}

func capBody(resp *http.Response) {
	resp.Body = limitedBody{io.LimitReader(resp.Body, maxResponseBytes), resp.Body}
}

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// fetch GETs url with browser-like headers and checks the status code.
// Callers must close the response body on success.
func fetch(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusServiceUnavailable {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: %w", url, ErrBlocked)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: unexpected status %d", url, resp.StatusCode)
	}
	capBody(resp)
	return resp, nil
}

// emit sends r without deadlocking against a cancelled consumer.
func emit(ctx context.Context, out chan<- Result, r Result) error {
	select {
	case out <- r:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// matchesQuery reports whether title contains every whitespace-separated
// token of query, case-insensitively. Used by providers (EZTV) that return
// unrelated listings for unknown queries.
func matchesQuery(title, query string) bool {
	lt := strings.ToLower(title)
	for _, tok := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(lt, tok) {
			return false
		}
	}
	return true
}
