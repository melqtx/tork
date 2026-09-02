package engine

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/anacrolix/torrent/metainfo"
)

// This file adds a plain-HTTP(S) download path for files whose publishers
// provide no torrent. Direct downloads share
// the torrents' Snapshot/pause/resume/remove surface, verify published hashes
// incrementally, and resume partial files with HTTP Range requests.

// directItem tracks one HTTP download. All fields are guarded by Engine.mu.
type directItem struct {
	url          string
	name         string // file name under DownloadDir (pre-validated by safeDataPath)
	checksum     Checksum
	expectedSize int64
	lockedOrigin string
	downloadDir  string
	dataPath     string

	length       int64 // total bytes; 0 until the server reports it
	done         int64
	state        TorrentState
	note         string             // short human status, e.g. a checksum failure
	cancel       context.CancelFunc // nil while paused
	runDone      chan struct{}      // closed after the current transfer releases its files
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
	checksum, err := SHA256Checksum(sum)
	if err != nil {
		return metainfo.Hash{}, err
	}
	return e.AddDirectDownloadWithOptions(DirectDownload{
		URL: url, Name: name, Checksum: checksum,
	}, opts)
}

func (e *Engine) AddDirectDownload(spec DirectDownload) (metainfo.Hash, error) {
	return e.AddDirectDownloadWithOptions(spec, AddOptions{})
}

func (e *Engine) AddDirectDownloadWithOptions(spec DirectDownload, opts AddOptions) (metainfo.Hash, error) {
	opts = e.normalizeOptions(opts)
	parsed, err := url.Parse(spec.URL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return metainfo.Hash{}, errors.New("direct download needs a valid HTTP(S) URL without credentials or fragments")
	}
	checksum, err := NewChecksum(string(spec.Checksum.Algorithm), spec.Checksum.Hex)
	if err != nil {
		return metainfo.Hash{}, err
	}
	spec.Checksum = checksum
	if spec.ExpectedSize < 0 {
		return metainfo.Hash{}, errors.New("direct download size cannot be negative")
	}
	if spec.Name == "" {
		spec.Name = path.Base(strings.TrimRight(parsed.Path, "/"))
	}
	if !safeDirectFilename(spec.Name) {
		return metainfo.Hash{}, fmt.Errorf("unsafe file name %q", spec.Name)
	}
	dataPath, ok := safeDataPath(opts.DownloadDir, spec.Name)
	if !ok {
		return metainfo.Hash{}, fmt.Errorf("unsafe file name %q", spec.Name)
	}
	existingSize, existingDone := int64(0), false
	if fi, err := os.Lstat(dataPath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
			return metainfo.Hash{}, errors.New("destination already exists and is not a regular file")
		}
		existingSize, existingDone = fi.Size(), true
		if spec.VerifyExisting || spec.ExpectedSize > 0 || !spec.Checksum.Empty() {
			if spec.VerifyExisting && spec.ExpectedSize == 0 && spec.Checksum.Empty() {
				return metainfo.Hash{}, errors.New("destination already exists but no size or checksum is available to verify it; refusing to trust it")
			}
			if spec.ExpectedSize > 0 && existingSize != spec.ExpectedSize {
				return metainfo.Hash{}, fmt.Errorf("destination already exists with size %d, expected %d; refusing to overwrite", existingSize, spec.ExpectedSize)
			}
			if !spec.Checksum.Empty() {
				matches, err := fileMatchesChecksum(dataPath, spec.Checksum)
				if err != nil {
					return metainfo.Hash{}, fmt.Errorf("verify existing destination: %w", err)
				}
				if !matches {
					return metainfo.Hash{}, errors.New("destination already exists with a different checksum; refusing to overwrite")
				}
			}
		}
	}
	h := directHash(spec.URL)
	lockedOrigin := ""
	if spec.LockToOrigin {
		lockedOrigin = directURLOrigin(parsed)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if it, ok := e.direct[h]; ok {
		if it.name != spec.Name || it.checksum != spec.Checksum || it.expectedSize != spec.ExpectedSize || it.lockedOrigin != lockedOrigin {
			return metainfo.Hash{}, errors.New("direct download is already tracked with different safety metadata")
		}
		if it.state == StatePaused {
			it.downloadDir = opts.DownloadDir
			it.dataPath = dataPath
			e.startDirectLocked(it)
		}
		return h, nil
	}
	it := &directItem{
		url: spec.URL, name: spec.Name, checksum: spec.Checksum,
		expectedSize: spec.ExpectedSize,
		downloadDir:  opts.DownloadDir, dataPath: dataPath,
		length: spec.ExpectedSize, state: StateDownloading,
	}
	it.lockedOrigin = lockedOrigin
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
	done := make(chan struct{})
	it.cancel = cancel
	it.runDone = done
	it.state = StateDownloading
	it.note = ""
	it.samples = ring{}
	e.dwg.Add(1)
	go func() {
		defer e.dwg.Done()
		defer close(done)
		defer func() {
			if recovered := recover(); recovered != nil {
				e.failDirect(ctx, it, fmt.Errorf("internal transfer failure: %v", recovered))
			}
		}()
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
	if fi, err := os.Lstat(part); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
			e.failDirect(ctx, it, errors.New("partial download path is not a regular file; refusing to read or write it"))
			return
		}
		if it.expectedSize > 0 && fi.Size() > it.expectedSize {
			e.failDirect(ctx, it, fmt.Errorf("partial download is %d bytes, larger than the expected %d; refusing to overwrite it", fi.Size(), it.expectedSize))
			return
		}
	} else if !os.IsNotExist(err) {
		e.failDirect(ctx, it, fmt.Errorf("inspect partial download: %w", err))
		return
	}

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

	if e.cfg.Direct.EnableChunking && e.directMaxConnections >= 2 {
		handled, err := e.runDirectParallel(ctx, it, dest)
		if handled {
			if err != nil {
				e.failDirect(ctx, it, err)
			}
			return
		}
	}
	_ = os.Remove(part + ".meta")

	hasher, _ := it.checksum.newHash()
	offset := hashExistingPart(part, hasher)
	e.mu.Lock()
	it.done = offset
	e.mu.Unlock()

	resp, err := e.openDirect(ctx, it, offset)
	if err != nil {
		e.failDirect(ctx, it, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK && offset > 0 {
		// server ignored the Range request: start over
		offset = 0
		hasher, _ = it.checksum.newHash()
	}
	length := totalLength(resp, offset)
	if it.expectedSize > 0 && length > 0 && length != it.expectedSize {
		e.failDirect(ctx, it, fmt.Errorf("server reported %d bytes, expected %d", length, it.expectedSize))
		return
	}

	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if offset == 0 {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	if fi, err := os.Lstat(part); err == nil && (fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular()) {
		e.failDirect(ctx, it, errors.New("partial download path is not a regular file; refusing to write"))
		return
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

	e.mu.Lock()
	downloaded := it.done
	e.mu.Unlock()
	if it.expectedSize > 0 && downloaded != it.expectedSize {
		_ = os.Remove(part)
		e.failDirect(ctx, it, fmt.Errorf("downloaded %d bytes, expected %d", downloaded, it.expectedSize))
		return
	}
	if !it.checksum.Empty() {
		if got := hex.EncodeToString(hasher.Sum(nil)); got != it.checksum.Hex {
			os.Remove(part)
			e.mu.Lock()
			it.done, it.state, it.cancel = 0, StatePaused, nil
			it.note = it.checksum.Label() + " checksum mismatch - data discarded, press p to retry"
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
			if it.expectedSize > 0 && done+int64(n) > it.expectedSize {
				return fmt.Errorf("server exceeded the declared size of %d bytes", it.expectedSize)
			}
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

func (e *Engine) openDirect(ctx context.Context, it *directItem, offset int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, it.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", directUserAgent)
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := e.doDirectRequest(req, it)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return resp, nil
}

func directURLOrigin(u *url.URL) string {
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}

func safeDirectFilename(name string) bool {
	if len(name) == 0 || len(name) > 240 || strings.TrimSpace(name) != name ||
		name == "." || name == ".." || strings.ContainsAny(name, "/\\<>:\"|?*") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || directBidiControl(r) {
			return false
		}
	}
	stem := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	switch stem {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return false
	}
	return !strings.HasSuffix(name, ".") && !strings.HasSuffix(name, " ")
}

func directBidiControl(r rune) bool {
	return r == '\u200e' || r == '\u200f' ||
		(r >= '\u202a' && r <= '\u202e') ||
		(r >= '\u2066' && r <= '\u2069')
}

// doDirectRequest preserves the configured transport while applying the
// per-download redirect boundary. The cloned client is request-local and
// therefore safe when range workers call it concurrently.
func (e *Engine) doDirectRequest(req *http.Request, it *directItem) (*http.Response, error) {
	if it.lockedOrigin == "" {
		return e.directHTTP.Do(req)
	}
	client := *e.directHTTP
	baseCheck := client.CheckRedirect
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if next.URL.User != nil || directURLOrigin(next.URL) != it.lockedOrigin {
			return errors.New("refusing a download redirect outside the trusted origin")
		}
		if baseCheck != nil {
			return baseCheck(next, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return client.Do(req)
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
	done := it.runDone
	name := it.name
	dataPath := it.dataPath
	downloadDir := it.downloadDir
	e.mu.Unlock()
	if done != nil {
		<-done
	}

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
	err0 := os.Remove(dest + ".part.meta")
	err1 := os.Remove(dest + ".part")
	err2 := os.Remove(dest)
	if err2 != nil && !os.IsNotExist(err2) {
		return err2
	}
	if err1 != nil && !os.IsNotExist(err1) {
		return err1
	}
	if err0 != nil && !os.IsNotExist(err0) {
		return err0
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
	checksum := it.checksum
	if checksum.Empty() {
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

	fi, err := os.Lstat(dest)
	if err != nil {
		return result, err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
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
	it.note = "checking " + checksum.Label()
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

	return e.verifyDirectFile(verifyCtx, h, it, dest, checksum, fi.Size())
}

func (e *Engine) verifyDirectFile(ctx context.Context, h metainfo.Hash, it *directItem, dest string, checksum Checksum, size int64) (VerifyResult, error) {
	var result VerifyResult
	f, err := os.Open(dest)
	if err != nil {
		return result, err
	}
	defer f.Close()

	hasher, err := checksum.newHash()
	if err != nil {
		return result, err
	}
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
	if got == checksum.Hex {
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

func fileMatchesChecksum(filename string, checksum Checksum) (bool, error) {
	f, err := os.Open(filename)
	if err != nil {
		return false, err
	}
	defer f.Close()
	hasher, err := checksum.newHash()
	if err != nil {
		return false, err
	}
	if _, err := io.Copy(hasher, f); err != nil {
		return false, err
	}
	return hex.EncodeToString(hasher.Sum(nil)) == checksum.Hex, nil
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
