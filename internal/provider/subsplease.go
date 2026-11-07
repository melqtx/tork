package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
)

// SubsPlease queries the search API of the SubsPlease release group itself,
// so every magnet comes straight from the publisher of the release rather
// than from a third-party re-upload. Coverage is SubsPlease's own simulcast
// subtitles; Nyaa carries the same releases with swarm counts, and Merge
// folds the two together by infohash.
const subsPleaseWeb = "https://subsplease.org"

type SubsPlease struct {
	client *http.Client
	base   string
}

func NewSubsPlease(client *http.Client, base string) *SubsPlease {
	if client == nil {
		client = DefaultClient
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = subsPleaseWeb
	}
	return &SubsPlease{client: client, base: base}
}

func (s *SubsPlease) Name() string { return "subsplease" }

type subsPleaseEntry struct {
	Show      string `json:"show"`
	Episode   string `json:"episode"`
	Downloads []struct {
		Res    string `json:"res"`
		Magnet string `json:"magnet"`
	} `json:"downloads"`
}

func (s *SubsPlease) Search(ctx context.Context, query string, out chan<- Result) error {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	u := fmt.Sprintf("%s/api/?f=search&tz=UTC&s=%s", s.base, url.QueryEscape(q))
	resp, err := fetch(ctx, s.client, u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("subsplease: read: %w", err)
	}
	// The API answers an empty JSON array for no matches and an object keyed
	// by release title otherwise.
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("[]")) {
		return nil
	}
	var entries map[string]subsPleaseEntry
	if err := json.Unmarshal(trimmed, &entries); err != nil {
		return fmt.Errorf("subsplease: decode: %w", err)
	}
	// Map order is random; keep results stable for the UI and tests.
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	emitted, offered := 0, 0
	for _, key := range keys {
		e := entries[key]
		show := strings.TrimSpace(e.Show)
		if show == "" {
			show = strings.TrimSpace(key)
		}
		for _, d := range e.Downloads {
			offered++
			m, err := metainfo.ParseMagnetUri(d.Magnet)
			if err != nil {
				continue
			}
			title := strings.TrimSpace(m.DisplayName)
			if title == "" {
				title = fmt.Sprintf("[SubsPlease] %s - %s (%sp)", show, e.Episode, d.Res)
			}
			var size int64
			if xl := m.Params.Get("xl"); xl != "" {
				size, _ = strconv.ParseInt(xl, 10, 64)
			}
			r := Result{
				Title:     title,
				SizeBytes: size,
				Magnet:    d.Magnet,
				Provider:  s.Name(),
				Trusted:   true, // published by the release group itself
				Category:  "Anime",
			}
			if size > 0 {
				r.Size = humanSize(size)
			}
			if err := emit(ctx, out, r); err != nil {
				return err
			}
			emitted++
		}
	}
	if emitted == 0 && offered > 0 {
		return ErrResponseFormat
	}
	return nil
}
