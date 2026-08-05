package engine

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
)

const directManifestVersion = 1

type byteRange struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"` // inclusive
}

type directManifest struct {
	Version   int         `json:"version"`
	URL       string      `json:"url"`
	Total     int64       `json:"total"`
	Validator string      `json:"validator,omitempty"`
	Completed []byteRange `json:"completed,omitempty"`
}

type directProtocolError struct{ error }

func (e *Engine) runDirectParallel(ctx context.Context, it *directItem, dest string) (bool, error) {
	total, validator, ok := e.discoverDirectRanges(ctx, it.url)
	if !ok || total < 2*e.directMinChunkSize || (it.sha256 == "" && validator == "") {
		return false, nil
	}
	part, meta := dest+".part", dest+".part.meta"
	manifest, err := loadDirectManifest(meta, it.url, total, validator)
	if err != nil {
		_ = os.Remove(meta)
		_ = os.Remove(part)
		manifest = directManifest{Version: directManifestVersion, URL: it.url, Total: total, Validator: validator}
	}
	if manifest.Version == 0 {
		manifest = directManifest{Version: directManifestVersion, URL: it.url, Total: total, Validator: validator}
		if fi, statErr := os.Stat(part); statErr == nil && fi.Size() > 0 && fi.Size() < total {
			manifest.Completed = []byteRange{{Start: 0, End: fi.Size() - 1}}
		}
	}
	if err := saveDirectManifest(meta, manifest); err != nil {
		return true, err
	}
	f, err := os.OpenFile(part, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return true, err
	}
	if err := f.Truncate(total); err != nil {
		f.Close()
		return true, err
	}

	scheduler := newDirectRangeScheduler(total, manifest.Completed)
	completedBytes := rangeBytes(manifest.Completed)
	e.mu.Lock()
	it.done, it.length = completedBytes, total
	e.mu.Unlock()

	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	var manifestMu sync.Mutex
	var firstErr error
	var errMu sync.Mutex
	worker := func() {
		defer workers.Done()
		for {
			r, ok := scheduler.claim(e.directMinChunkSize)
			if !ok {
				return
			}
			if err := e.fetchDirectRange(workCtx, it, f, r, total, validator); err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				errMu.Unlock()
				return
			}
			if err := f.Sync(); err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				errMu.Unlock()
				return
			}
			manifestMu.Lock()
			manifest.Completed = normalizeRanges(append(manifest.Completed, r))
			err := saveDirectManifest(meta, manifest)
			manifestMu.Unlock()
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				errMu.Unlock()
				return
			}
		}
	}
	count := e.directMaxConnections
	if !scheduler.hasWork() {
		count = 0
	}
	workers.Add(count)
	for range count {
		go worker()
	}
	workers.Wait()
	if closeErr := f.Close(); firstErr == nil {
		firstErr = closeErr
	}
	if firstErr != nil {
		var protocol directProtocolError
		if errors.As(firstErr, &protocol) {
			_ = os.Remove(part)
			_ = os.Remove(meta)
			return false, nil
		}
		return true, firstErr
	}
	if err := finalizeParallelDirect(it, part, dest); err != nil {
		_ = os.Remove(meta)
		e.mu.Lock()
		it.done = 0
		e.mu.Unlock()
		return true, err
	}
	_ = os.Remove(meta)
	e.mu.Lock()
	it.done, it.length, it.state, it.cancel = total, total, StateDone, nil
	e.mu.Unlock()
	return true, nil
}

func (e *Engine) discoverDirectRanges(ctx context.Context, rawURL string) (int64, string, bool) {
	validator := ""
	head, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err == nil {
		head.Header.Set("User-Agent", directUserAgent)
		head.Header.Set("Accept-Encoding", "identity")
		if resp, doErr := e.directHTTP.Do(head); doErr == nil {
			validator = responseValidator(resp)
			resp.Body.Close()
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, "", false
	}
	req.Header.Set("User-Agent", directUserAgent)
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Range", "bytes=0-0")
	resp, err := e.directHTTP.Do(req)
	if err != nil {
		return 0, "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return 0, "", false
	}
	start, end, total, ok := parseContentRange(resp.Header.Get("Content-Range"))
	if !ok || start != 0 || end != 0 || total <= 0 {
		return 0, "", false
	}
	if got := responseValidator(resp); got != "" {
		if validator != "" && got != validator {
			return 0, "", false
		}
		validator = got
	}
	return total, validator, true
}

func (e *Engine) fetchDirectRange(ctx context.Context, it *directItem, f *os.File, r byteRange, total int64, validator string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, it.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", directUserAgent)
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", r.Start, r.End))
	if validator != "" {
		req.Header.Set("If-Range", validator)
	}
	resp, err := e.directHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return directProtocolError{fmt.Errorf("range request returned %d", resp.StatusCode)}
	}
	start, end, gotTotal, ok := parseContentRange(resp.Header.Get("Content-Range"))
	if !ok || start != r.Start || end != r.End || gotTotal != total {
		return directProtocolError{errors.New("range response did not match request")}
	}
	if got := responseValidator(resp); validator != "" && got != "" && got != validator {
		return directProtocolError{errors.New("remote file changed during download")}
	}
	remaining, offset := r.End-r.Start+1, r.Start
	buf := make([]byte, 128<<10)
	for remaining > 0 {
		n, readErr := resp.Body.Read(buf[:min(int64(len(buf)), remaining)])
		if n > 0 {
			if _, err := f.WriteAt(buf[:n], offset); err != nil {
				return err
			}
			offset += int64(n)
			remaining -= int64(n)
			e.mu.Lock()
			it.done += int64(n)
			e.mu.Unlock()
		}
		if readErr != nil {
			if readErr == io.EOF && remaining == 0 {
				break
			}
			return readErr
		}
	}
	var extra [1]byte
	if n, err := resp.Body.Read(extra[:]); n != 0 || (err != nil && err != io.EOF) {
		return directProtocolError{errors.New("range response exceeded requested length")}
	}
	return nil
}

func finalizeParallelDirect(it *directItem, part, dest string) error {
	f, err := os.Open(part)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, f)
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if it.sha256 != "" && hex.EncodeToString(h.Sum(nil)) != it.sha256 {
		_ = os.Remove(part)
		return errors.New("checksum mismatch - data discarded, press p to retry")
	}
	return os.Rename(part, dest)
}

func parseContentRange(raw string) (start, end, total int64, ok bool) {
	if !strings.HasPrefix(raw, "bytes ") {
		return
	}
	parts := strings.Split(strings.TrimPrefix(raw, "bytes "), "/")
	if len(parts) != 2 {
		return
	}
	bounds := strings.Split(parts[0], "-")
	if len(bounds) != 2 {
		return
	}
	start, err1 := strconv.ParseInt(bounds[0], 10, 64)
	end, err2 := strconv.ParseInt(bounds[1], 10, 64)
	total, err3 := strconv.ParseInt(parts[1], 10, 64)
	ok = err1 == nil && err2 == nil && err3 == nil && start >= 0 && end >= start && end < total
	return
}

func responseValidator(resp *http.Response) string {
	if etag := strings.TrimSpace(resp.Header.Get("ETag")); etag != "" && !strings.HasPrefix(etag, "W/") {
		return etag
	}
	return strings.TrimSpace(resp.Header.Get("Last-Modified"))
}

func loadDirectManifest(path, rawURL string, total int64, validator string) (directManifest, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return directManifest{}, nil
	}
	if err != nil {
		return directManifest{}, err
	}
	var m directManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return directManifest{}, err
	}
	if m.Version != directManifestVersion || m.URL != rawURL || m.Total != total || m.Validator != validator {
		return directManifest{}, errors.New("stale direct manifest")
	}
	normal := normalizeRanges(m.Completed)
	if len(normal) != len(m.Completed) {
		return directManifest{}, errors.New("invalid direct manifest ranges")
	}
	for i, r := range normal {
		if r.Start < 0 || r.End < r.Start || r.End >= total || r != m.Completed[i] {
			return directManifest{}, errors.New("invalid direct manifest range")
		}
	}
	return m, nil
}

func saveDirectManifest(path string, m directManifest) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".direct-meta-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

func normalizeRanges(in []byteRange) []byteRange {
	out := append([]byteRange(nil), in...)
	slices.SortFunc(out, func(a, b byteRange) int { return cmp.Compare(a.Start, b.Start) })
	merged := out[:0]
	for _, r := range out {
		if len(merged) == 0 || r.Start > merged[len(merged)-1].End+1 {
			merged = append(merged, r)
			continue
		}
		if r.End > merged[len(merged)-1].End {
			merged[len(merged)-1].End = r.End
		}
	}
	return merged
}

type directRangeScheduler struct {
	mu      sync.Mutex
	pending []byteRange
}

func newDirectRangeScheduler(total int64, complete []byteRange) *directRangeScheduler {
	complete = normalizeRanges(complete)
	var pending []byteRange
	start := int64(0)
	for _, done := range complete {
		if start < done.Start {
			pending = append(pending, byteRange{Start: start, End: done.Start - 1})
		}
		start = max(start, done.End+1)
	}
	if start < total {
		pending = append(pending, byteRange{Start: start, End: total - 1})
	}
	return &directRangeScheduler{pending: pending}
}

func (s *directRangeScheduler) hasWork() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending) > 0
}

// claim dynamically splits the largest pending interval. An idle worker takes
// the upper half while the lower half remains available for another worker;
// once an interval reaches the minimum size it is claimed whole.
func (s *directRangeScheduler) claim(minChunk int64) (byteRange, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return byteRange{}, false
	}
	largest := 0
	for i := 1; i < len(s.pending); i++ {
		if s.pending[i].End-s.pending[i].Start > s.pending[largest].End-s.pending[largest].Start {
			largest = i
		}
	}
	r := s.pending[largest]
	length := r.End - r.Start + 1
	if length >= 2*minChunk {
		split := r.Start + length/2
		s.pending[largest].End = split - 1
		return byteRange{Start: split, End: r.End}, true
	}
	s.pending = slices.Delete(s.pending, largest, largest+1)
	return r, true
}

func missingDirectRanges(total int64, complete []byteRange, chunk int64) []byteRange {
	complete = normalizeRanges(complete)
	var missing []byteRange
	start := int64(0)
	for _, done := range complete {
		for start < done.Start {
			end := min(done.Start-1, start+chunk-1)
			missing = append(missing, byteRange{start, end})
			start = end + 1
		}
		start = max(start, done.End+1)
	}
	for start < total {
		end := min(total-1, start+chunk-1)
		missing = append(missing, byteRange{start, end})
		start = end + 1
	}
	return missing
}

func rangeBytes(ranges []byteRange) int64 {
	var n int64
	for _, r := range normalizeRanges(ranges) {
		n += r.End - r.Start + 1
	}
	return n
}
