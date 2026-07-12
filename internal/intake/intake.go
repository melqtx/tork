// Package intake classifies user-supplied torrent inputs without performing
// network requests. It is shared by the CLI and the TUI home field so both
// surfaces accept the same magnets, hashes, URLs, and local files.
package intake

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/melqtx/tork/internal/provider"
)

const MaxTorrentBytes int64 = 16 << 20

type Kind int

const (
	Magnet Kind = iota
	InfoHash
	TorrentURL
	TorrentFile
)

type Target struct {
	Kind  Kind
	Value string // original magnet/URL, or absolute local path
	Name  string // best-effort display name
}

var (
	reHexInfoHash    = regexp.MustCompile(`(?i)^[0-9a-f]{40}$`)
	reBase32InfoHash = regexp.MustCompile(`(?i)^[a-z2-7]{32}$`)
)

// DetectCLI accepts every supported explicit positional input. Any existing
// regular file is offered to the metainfo decoder even without a .torrent
// suffix; ordinary nonexistent text is left unclassified.
func DetectCLI(raw string) (Target, bool, error) {
	return detect(raw, true)
}

// DetectHome is conservative around local paths so a search term that happens
// to name a file is not hijacked. Only .torrent-looking paths that actually
// exist are classified; anything else stays a search query.
func DetectHome(raw string) (Target, bool, error) {
	return detect(raw, false)
}

// ExplicitTorrentURL validates --torrent-url values without requiring their
// path to end in .torrent.
func ExplicitTorrentURL(raw string) (Target, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Target{}, errors.New("--torrent-url requires an absolute http or https URL")
	}
	return Target{Kind: TorrentURL, Value: raw, Name: urlName(u)}, nil
}

func detect(raw string, allowAnyExistingFile bool) (Target, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Target{}, false, nil
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "magnet:?") {
		return Target{Kind: Magnet, Value: raw, Name: magnetName(raw)}, true, nil
	}
	if reHexInfoHash.MatchString(raw) || reBase32InfoHash.MatchString(raw) {
		return Target{
			Kind: InfoHash, Value: provider.BuildMagnet(raw, "", provider.DefaultTrackers),
		}, true, nil
	}
	if u, ok := automaticTorrentURL(raw); ok {
		return Target{Kind: TorrentURL, Value: raw, Name: urlName(u)}, true, nil
	}

	path, looksLikeTorrent := localPath(raw)
	if !allowAnyExistingFile && !looksLikeTorrent {
		return Target{}, false, nil
	}
	info, err := os.Stat(path) // follows symlinks intentionally
	if err != nil {
		if !allowAnyExistingFile {
			// A home-screen query is only hijacked by a real file; a missing
			// or unreadable path falls through to a normal search.
			return Target{}, false, nil
		}
		if os.IsNotExist(err) {
			if looksLikeTorrent {
				return Target{}, true, fmt.Errorf("torrent file not found: %s", path)
			}
			return Target{}, false, nil
		}
		return Target{}, true, err
	}
	if !info.Mode().IsRegular() {
		return Target{}, true, fmt.Errorf("torrent input is not a regular file: %s", path)
	}
	if info.Size() > MaxTorrentBytes {
		return Target{}, true, fmt.Errorf("torrent file is too large: %s (maximum 16 MiB)", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Target{}, true, err
	}
	return Target{Kind: TorrentFile, Value: abs, Name: filepath.Base(abs)}, true, nil
}

func automaticTorrentURL(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, false
	}
	return u, strings.EqualFold(filepath.Ext(u.Path), ".torrent")
}

func localPath(raw string) (path string, looksLikeTorrent bool) {
	path = raw
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = home
			if raw != "~" {
				path = filepath.Join(home, strings.TrimPrefix(raw, "~/"))
			}
		}
	}
	return filepath.Clean(path), strings.EqualFold(filepath.Ext(path), ".torrent")
}

func magnetName(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Query().Get("dn")
}

func urlName(u *url.URL) string {
	name := filepath.Base(u.Path)
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	if name == "." || name == "/" {
		return ""
	}
	return name
}
