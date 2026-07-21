package engine

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// This file adds a plain-HTTPS download path for ISO-shelf images whose
// distros publish no torrent (Gentoo, openSUSE, …). Direct downloads share
// the torrents' Snapshot/pause/resume/remove surface, verify a published
// sha256 incrementally as bytes arrive, and resume partial files with HTTP
// Range requests.

// directItem tracks one HTTP download. All fields are guarded by Engine.mu.
type directItem struct {
	url         string
	name        string // file name under DownloadDir (pre-validated by safeDataPath)
	sha256      string // expected hex digest; "" downloads unverified
	downloadDir string
	dataPath    string

	length       int64 // total bytes; 0 until the server reports it
	done         int64
	state        TorrentState
	note         string             // short human status, e.g. a checksum failure
	cancel       context.CancelFunc // nil while paused
	verifyCancel context.CancelFunc
	samples      ring
}

// directClient has no overall timeout - an ISO download runs for minutes to
// hours - but bounds redirects and the wait for response headers.
var directClient = &http.Client{
	Transport: &http.Transport{ResponseHeaderTimeout: 30 * time.Second},
	CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) >= 8 {
			return errors.New("stopped after 8 redirects")
		}
		return nil
	},
}

const directUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// directHash keys a direct download in the same hash space torrents use, so
// the TUI can address both kinds uniformly.
func directHash(url string) metainfo.Hash {
	return metainfo.Hash(sha1.Sum([]byte(url)))
}

// AddDirect starts downloading url to name under the download dir, verifying
// the sha256 hex digest on completion when one is given. Re-adding a paused
// download resumes it; re-adding an active or finished one is a no-op.
func (e *Engine) AddDirect(url, name, sum string) (metainfo.Hash, error) {
	return e.AddDirectWithOptions(url, name, sum, AddOptions{})
}

func (e *Engine) AddDirectWithOptions(url, name, sum string, opts AddOptions) (metainfo.Hash, error) {
	opts = e.normalizeOptions(opts)
	if name == "" {
		name = filepath.Base(strings.TrimRight(url, "/"))
	}
	dataPath, ok := safeDataPath(opts.DownloadDir, name)
	if !ok {
		return metainfo.Hash{}, fmt.Errorf("unsafe file name %q", name)
	}
	existingSize, existingDone := int64(0), false
	if fi, err := os.Stat(dataPath); err == nil && fi.Mode().IsRegular() {
		existingSize, existingDone = fi.Size(), true
	}
	h := directHash(url)

	e.mu.Lock()
	defer e.mu.Unlock()
	if it, ok := e.direct[h]; ok {
		if it.state == StatePaused {
			it.downloadDir = opts.DownloadDir
			it.dataPath = dataPath
			e.startDirectLocked(it)
		}
		return h, nil
	}
	it := &directItem{
		url: url, name: name, sha256: strings.ToLower(sum),
		downloadDir: opts.DownloadDir, dataPath: dataPath,
		state: StateDownloading,
	}
	e.direct[h] = it
	if existingDone {
		it.done, it.length, it.state = existingSize, existingSize, StateDone
		return h, nil
	}
	e.startDirectLocked(it)
	return h, nil
}

// startDirectLocked launches the download goroutine. Caller holds e.mu.
func (e *Engine) startDirectLocked(it *directItem) {
	ctx, cancel := context.WithCancel(context.Background())
	it.cancel = cancel
	it.state = StateDownloading
	it.note = ""
	it.samples = ring{}
	e.dwg.Add(1)
	go func() {
		defer e.dwg.Done()
		defer func() { _ = recover() }() // a download must never crash the app
		e.runDirect(ctx, it)
	}()
}

// runDirect performs the whole transfer: prefix-hash an existing .part file,
// request the remainder with a Range header, stream to disk while hashing,
// then verify and rename into place.
func (e *Engine) runDirect(ctx context.Context, it *directItem) {
	dest := it.dataPath
	if dest == "" {
		dest, _ = safeDataPath(it.downloadDir, it.name) // validated in AddDirect
	}
	part := dest + ".part"

	// already on disk from an earlier run (e.g. resumed from state.json)
	if fi, err := os.Stat(dest); err == nil {
		e.mu.Lock()
		if it.state == StateVerifying {
			e.mu.Unlock()
			return
		}
		it.done, it.length, it.state, it.cancel = fi.Size(), fi.Size(), StateDone, nil
		e.mu.Unlock()
		return
	}

	hasher := sha256.New()
	offset := hashExistingPart(part, hasher)
	e.mu.Lock()
	it.done = offset
	e.mu.Unlock()

	resp, err := e.openDirect(ctx, it.url, offset)
	if err != nil {
		e.failDirect(ctx, it, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK && offset > 0 {
		// server ignored the Range request: start over
		offset = 0
		hasher = sha256.New()
	}
	length := totalLength(resp, offset)

	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if offset == 0 {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		e.failDirect(ctx, it, err)
		return
	}

	e.mu.Lock()
	it.length = length
	e.mu.Unlock()

	if err := e.copyDirect(it, f, resp.Body, hasher, offset); err != nil {
		f.Close()
		e.failDirect(ctx, it, err)
		return
	}
	if err := f.Close(); err != nil {
		e.failDirect(ctx, it, err)
		return
	}

	if it.sha256 != "" {
		if got := hex.EncodeToString(hasher.Sum(nil)); got != it.sha256 {
			os.Remove(part)
			e.mu.Lock()
			it.done, it.state, it.cancel = 0, StatePaused, nil
			it.note = "checksum mismatch - data discarded, press p to retry"
			e.mu.Unlock()
			return
		}
	}
	if err := os.Rename(part, dest); err != nil {
		e.failDirect(ctx, it, err)
		return
	}
	e.mu.Lock()
	it.done, it.state, it.cancel = it.length, StateDone, nil
	if it.length == 0 { // server never reported a length
		if fi, err := os.Stat(dest); err == nil {
			it.done, it.length = fi.Size(), fi.Size()
		}
	}
	e.mu.Unlock()
}

// copyDirect streams body to the file and hasher, publishing progress.
func (e *Engine) copyDirect(it *directItem, f *os.File, body io.Reader, hasher hash.Hash, offset int64) error {
	buf := make([]byte, 128<<10)
	done := offset
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			hasher.Write(buf[:n])
			done += int64(n)
			e.mu.Lock()
			it.done = done
			e.mu.Unlock()
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

func (e *Engine) openDirect(ctx context.Context, url string, offset int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", directUserAgent)
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := e.directHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return resp, nil
}

// failDirect parks the download as paused with a short reason, so the user
// can retry with `p`. A cancelled context means pause/remove already spoke
// for the state - leave it alone.
func (e *Engine) failDirect(ctx context.Context, it *directItem, err error) {
	if ctx.Err() != nil {
		return
	}
	e.mu.Lock()
	it.state, it.cancel = StatePaused, nil
	it.note = shortNetErr(err)
	e.mu.Unlock()
}

// shortNetErr compresses a raw network error into a calm one-liner.
func shortNetErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && i+2 < len(s) {
		s = s[i+2:]
	}
	return "download interrupted (" + s + ") - press p to resume"
}

// hashExistingPart feeds an existing partial file through the hasher so the
// final digest covers the whole file, returning its size (0 = start fresh).
func hashExistingPart(part string, hasher hash.Hash) int64 {
	f, err := os.Open(part)
	if err != nil {
		return 0
	}
	defer f.Close()
	n, err := io.Copy(hasher, f)
	if err != nil {
		hasher.Reset()
		return 0
	}
	return n
}

// totalLength derives the full file size from a 200 or 206 response.
func totalLength(resp *http.Response, offset int64) int64 {
	if resp.StatusCode == http.StatusPartialContent {
		// Content-Range: bytes <from>-<to>/<total>
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			if i := strings.LastIndexByte(cr, '/'); i >= 0 {
				if total, err := strconv.ParseInt(cr[i+1:], 10, 64); err == nil {
					return total
				}
			}
		}
		if resp.ContentLength > 0 {
			return offset + resp.ContentLength
		}
		return 0
	}
	if resp.ContentLength > 0 {
		return resp.ContentLength
	}
	return 0
}

// directSnapshot renders a direct download in the shared Snapshot shape.
// Caller holds e.mu.
func (e *Engine) directSnapshot(h metainfo.Hash, it *directItem, now time.Time) Snapshot {
	s := Snapshot{
		Hash: h, Name: it.name, Magnet: it.url,
		DownloadDir: it.downloadDir, DataPath: it.dataPath,
		BytesCompleted: it.done, Length: it.length,
		State: it.state, Note: it.note,
	}
	if it.state == StateDownloading {
		it.samples.push(sample{at: now, bytes: it.done})
		s.SpeedBps = it.samples.speedBps()
		if s.SpeedBps > 0 && s.Length > s.BytesCompleted {
			s.ETA = time.Duration(float64(s.Length-s.BytesCompleted) / s.SpeedBps * float64(time.Second))
		}
	}
	return s
}

// pauseDirectLocked stops the transfer, keeping the .part file for resume.
// Caller holds e.mu.
func pauseDirectLocked(it *directItem) {
	if it.state != StateDownloading || it.cancel == nil {
		return
	}
	it.cancel()
	it.cancel = nil
	it.state = StatePaused
	it.samples = ring{}
}

// removeDirect drops the download and optionally deletes its data.
func (e *Engine) removeDirect(h metainfo.Hash, it *directItem, deleteData bool) error {
	e.mu.Lock()
	if it.cancel != nil {
		it.cancel()
		it.cancel = nil
	}
	delete(e.direct, h)
	name := it.name
	dataPath := it.dataPath
	downloadDir := it.downloadDir
	e.mu.Unlock()

	if !deleteData {
		return nil
	}
	dest := dataPath
	if dest == "" {
		var ok bool
		dest, ok = safeDataPath(downloadDir, name)
		if !ok {
			return fmt.Errorf("delete data refused: unknown or unsafe path")
		}
	}
	if !safePathWithin(downloadDir, dest) {
		return fmt.Errorf("delete data refused: unknown or unsafe path")
	}
	err1 := os.Remove(dest + ".part")
	err2 := os.Remove(dest)
	if err2 != nil && !os.IsNotExist(err2) {
		return err2
	}
	if err1 != nil && !os.IsNotExist(err1) {
		return err1
	}
	return nil
}

func (e *Engine) verifyDirectDownload(ctx context.Context, h metainfo.Hash) (result VerifyResult, err error) {
	e.mu.Lock()
	if e.closing {
		e.mu.Unlock()
		return result, errors.New("engine is closing")
	}
	it, ok := e.direct[h]
	if !ok {
		e.mu.Unlock()
		return result, errors.New("unknown download")
	}
	if it.state == StateVerifying {
		e.mu.Unlock()
		return result, ErrVerificationInProgress
	}
	if it.state != StateDone {
		e.mu.Unlock()
		return result, ErrVerificationIncomplete
	}
	expected := strings.TrimSpace(strings.ToLower(it.sha256))
	if expected == "" {
		e.mu.Unlock()
		return result, ErrNoChecksum
	}
	dest := it.dataPath
	if dest == "" {
		dest, ok = safeDataPath(it.downloadDir, it.name)
		if !ok {
			e.mu.Unlock()
			return result, errors.New("download path is unavailable")
		}
	}
	e.mu.Unlock()

	fi, err := os.Stat(dest)
	if err != nil {
		return result, err
	}
	if !fi.Mode().IsRegular() {
		return result, fmt.Errorf("verify %s: not a regular file", dest)
	}

	e.mu.Lock()
	current, ok := e.direct[h]
	if !ok || current != it {
		e.mu.Unlock()
		return result, errors.New("download changed during verification")
	}
	if it.state == StateVerifying {
		e.mu.Unlock()
		return result, ErrVerificationInProgress
	}
	if it.state != StateDone {
		e.mu.Unlock()
		return result, ErrVerificationIncomplete
	}
	if it.cancel != nil {
		it.cancel()
		it.cancel = nil
	}
	previousDone, previousLength, previousNote := it.done, it.length, it.note
	verifyCtx, cancel := context.WithCancel(ctx)
	it.state = StateVerifying
	it.note = "checking SHA256"
	it.verifyCancel = cancel
	e.vwg.Add(1)
	e.mu.Unlock()

	defer func() {
		cancel()
		e.mu.Lock()
		if current, exists := e.direct[h]; exists && current == it {
			if it.state == StateVerifying {
				it.done, it.length, it.state = previousDone, previousLength, StateDone
				it.note = previousNote
			}
			it.verifyCancel = nil
		}
		e.mu.Unlock()
		e.vwg.Done()
	}()

	return e.verifyDirectFile(verifyCtx, h, it, dest, expected, fi.Size())
}

func (e *Engine) verifyDirectFile(ctx context.Context, h metainfo.Hash, it *directItem, dest, expected string, size int64) (VerifyResult, error) {
	var result VerifyResult
	f, err := os.Open(dest)
	if err != nil {
		return result, err
	}
	defer f.Close()

	hasher := sha256.New()
	buf := make([]byte, 128<<10)
	for {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
		n, readErr := f.Read(buf)
		if n > 0 {
			if _, err := hasher.Write(buf[:n]); err != nil {
				return result, err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return result, readErr
		}
	}

	got := hex.EncodeToString(hasher.Sum(nil))
	if got == expected {
		e.mu.Lock()
		if current, ok := e.direct[h]; ok && current == it {
			it.done, it.length, it.state = size, size, StateDone
			it.note = ""
		}
		e.mu.Unlock()
		return result, nil
	}

	quarantine, err := nextQuarantinePath(dest)
	if err != nil {
		return result, err
	}
	if err := os.Rename(dest, quarantine); err != nil {
		return result, fmt.Errorf("quarantine corrupt download: %w", err)
	}

	result.ChecksumMismatch = true
	result.NeedsRepair = true
	result.QuarantinePath = quarantine
	e.mu.Lock()
	if current, ok := e.direct[h]; ok && current == it {
		it.done, it.length, it.state, it.cancel = 0, size, StatePaused, nil
		it.note = "checksum mismatch - moved to " + filepath.Base(quarantine) + "; press p to retry"
	}
	e.mu.Unlock()
	return result, nil
}

func nextQuarantinePath(dest string) (string, error) {
	base := dest + ".corrupt"
	for i := 0; ; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s.%d", base, i)
		}
		_, err := os.Lstat(candidate)
		switch {
		case os.IsNotExist(err):
			return candidate, nil
		case err != nil:
			return "", err
		}
	}
}
