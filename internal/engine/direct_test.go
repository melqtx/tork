package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/melqtx/tork/internal/config"
)

// newDirectTestEngine builds an engine downloading into a temp dir.
func newDirectTestEngine(t *testing.T) *Engine {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DOWNLOAD_DIR", filepath.Join(t.TempDir(), "Downloads"))
	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), ".tork"))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	return eng
}

// serveISO serves payload with Range support and counts Range requests.
func serveISO(t *testing.T, payload []byte, rangeHits *atomic.Int32) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rangeHits != nil && r.Header.Get("Range") != "" {
			rangeHits.Add(1)
		}
		http.ServeContent(w, r, "image.iso", time.Time{}, bytes.NewReader(payload))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func sumHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func sum512Hex(b []byte) string {
	s := sha512.Sum512(b)
	return hex.EncodeToString(s[:])
}

func TestDirectDownloadSupportsSHA512AndExpectedSize(t *testing.T) {
	payload := []byte("a checksum-verified catalog artifact")
	ts := serveISO(t, payload, nil)
	eng := newDirectTestEngine(t)
	checksum, err := NewChecksum("sha512", sum512Hex(payload))
	if err != nil {
		t.Fatal(err)
	}

	h, err := eng.AddDirectDownload(DirectDownload{
		URL: ts.URL + "/artifact.jar", Name: "artifact.jar", Checksum: checksum,
		ExpectedSize: int64(len(payload)), LockToOrigin: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	awaitDirect(t, eng, func(s Snapshot) bool { return s.Hash == h && s.State == StateDone }, 10*time.Second)
	got, err := os.ReadFile(filepath.Join(eng.cfg.DownloadDir, "artifact.jar"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("downloaded artifact = %q, %v", got, err)
	}
}

func TestDirectDownloadRefusesCrossOriginRedirect(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		_, _ = w.Write([]byte("untrusted"))
	}))
	defer target.Close()
	entry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/artifact.jar", http.StatusFound)
	}))
	defer entry.Close()
	eng := newDirectTestEngine(t)

	h, err := eng.AddDirectDownload(DirectDownload{
		URL: entry.URL + "/artifact.jar", Name: "artifact.jar", LockToOrigin: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	snap := awaitDirect(t, eng, func(s Snapshot) bool { return s.Hash == h && s.State == StatePaused }, 10*time.Second)
	if !strings.Contains(snap.Note, "trusted origin") {
		t.Fatalf("note = %q, want trusted-origin refusal", snap.Note)
	}
	if targetHits.Load() != 0 {
		t.Fatalf("redirect target received %d requests, want none", targetHits.Load())
	}
}

func TestDirectDownloadVerifiesExistingCatalogFile(t *testing.T) {
	payload := []byte("already downloaded")
	eng := newDirectTestEngine(t)
	checksum, err := NewChecksum("sha512", sum512Hex(payload))
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(eng.cfg.DownloadDir, "artifact.jar")
	if err := os.WriteFile(dest, []byte("different bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = eng.AddDirectDownload(DirectDownload{
		URL:  "https://downloads.example.org/releases/artifact.jar",
		Name: "artifact.jar", Checksum: checksum, ExpectedSize: int64(len(payload)),
		VerifyExisting: true, LockToOrigin: true,
	})
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("existing-file error = %v", err)
	}
	got, readErr := os.ReadFile(dest)
	if readErr != nil || string(got) != "different bytes" {
		t.Fatalf("existing destination was changed: %q, %v", got, readErr)
	}
}

func TestDirectDownloadVerifyExistingRequiresVerificationMetadata(t *testing.T) {
	eng := newDirectTestEngine(t)
	dest := filepath.Join(eng.cfg.DownloadDir, "artifact.jar")
	if err := os.WriteFile(dest, []byte("unverifiable existing bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := eng.AddDirectDownload(DirectDownload{
		URL: "https://downloads.example.org/releases/artifact.jar", Name: "artifact.jar",
		VerifyExisting: true,
	})
	if err == nil || !strings.Contains(err.Error(), "no size or checksum") {
		t.Fatalf("existing-file error = %v, want missing verification metadata refusal", err)
	}
	got, readErr := os.ReadFile(dest)
	if readErr != nil || string(got) != "unverifiable existing bytes" {
		t.Fatalf("existing destination was changed: %q, %v", got, readErr)
	}
}

func TestDirectDownloadRejectsPortableUnsafeNames(t *testing.T) {
	eng := newDirectTestEngine(t)
	for _, name := range []string{"../escape", `folder\\escape`, "CON.jar", "bad:name.jar", "trailing.jar."} {
		if _, err := eng.AddDirect("https://example.com/file", name, ""); err == nil {
			t.Errorf("unsafe name %q was accepted", name)
		}
	}
	if _, err := eng.AddDirect("https://example.com/file#other", "file.bin", ""); err == nil {
		t.Error("URL fragment was accepted even though it is not sent to the server")
	}
}

func TestDirectDownloadRefusesSymlinkedDestinationFiles(t *testing.T) {
	payload := []byte("trusted payload")
	ts := serveISO(t, payload, nil)
	eng := newDirectTestEngine(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("leave me alone"), 0o644); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(eng.cfg.DownloadDir, "artifact.jar")
	if err := os.Symlink(outside, dest); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := eng.AddDirect(ts.URL+"/artifact.jar", "artifact.jar", sumHex(payload)); err == nil {
		t.Fatal("symlinked final destination was accepted")
	}
	if err := os.Remove(dest); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, dest+".part"); err != nil {
		t.Fatal(err)
	}
	h, err := eng.AddDirect(ts.URL+"/artifact.jar", "artifact.jar", sumHex(payload))
	if err != nil {
		t.Fatal(err)
	}
	snap := awaitDirect(t, eng, func(s Snapshot) bool { return s.Hash == h && s.State == StatePaused }, 10*time.Second)
	if !strings.Contains(snap.Note, "not a regular file") {
		t.Fatalf("symlink refusal note = %q", snap.Note)
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "leave me alone" {
		t.Fatalf("symlink target was changed: %q, %v", got, err)
	}
}

func awaitDirect(t *testing.T, eng *Engine, want func(Snapshot) bool, timeout time.Duration) Snapshot {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			t.Fatalf("condition not reached: %+v", eng.Snapshots())
		case <-time.After(50 * time.Millisecond):
		}
		for _, s := range eng.Snapshots() {
			if want(s) {
				return s
			}
		}
	}
}

func TestDirectDownloadVerifiesAndCompletes(t *testing.T) {
	payload := make([]byte, 256<<10)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	ts := serveISO(t, payload, nil)
	eng := newDirectTestEngine(t)

	h, err := eng.AddDirect(ts.URL+"/image.iso", "image.iso", sumHex(payload))
	if err != nil {
		t.Fatal(err)
	}
	snap := awaitDirect(t, eng, func(s Snapshot) bool { return s.Hash == h && s.State == StateDone }, 10*time.Second)
	if snap.BytesCompleted != int64(len(payload)) || snap.Length != int64(len(payload)) {
		t.Fatalf("snapshot bytes = %d/%d, want %d", snap.BytesCompleted, snap.Length, len(payload))
	}

	dest := filepath.Join(eng.cfg.DownloadDir, "image.iso")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("downloaded bytes differ from payload")
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatalf(".part file still present: %v", err)
	}
	if eng.Magnet(h) != ts.URL+"/image.iso" {
		t.Fatalf("Magnet(h) = %q, want the source URL", eng.Magnet(h))
	}
}

func TestDirectDownloadWithOptionsUsesOwnDownloadDir(t *testing.T) {
	payload := make([]byte, 32<<10)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	ts := serveISO(t, payload, nil)
	eng := newDirectTestEngine(t)
	otherDir := t.TempDir()

	h, err := eng.AddDirectWithOptions(ts.URL+"/image.iso", "image.iso", sumHex(payload), AddOptions{DownloadDir: otherDir})
	if err != nil {
		t.Fatal(err)
	}
	snap := awaitDirect(t, eng, func(s Snapshot) bool { return s.Hash == h && s.State == StateDone }, 10*time.Second)
	if snap.DownloadDir != otherDir {
		t.Fatalf("Snapshot.DownloadDir = %q, want %q", snap.DownloadDir, otherDir)
	}
	if snap.DataPath != filepath.Join(otherDir, "image.iso") {
		t.Fatalf("Snapshot.DataPath = %q, want file under option dir", snap.DataPath)
	}
	if _, err := os.Stat(filepath.Join(otherDir, "image.iso")); err != nil {
		t.Fatalf("file was not written to option dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(eng.cfg.DownloadDir, "image.iso")); !os.IsNotExist(err) {
		t.Fatalf("file should not be written to default dir: %v", err)
	}
}

func TestDirectChecksumMismatchDiscardsData(t *testing.T) {
	payload := make([]byte, 64<<10)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	ts := serveISO(t, payload, nil)
	eng := newDirectTestEngine(t)
	eng.directMinChunkSize = 16 << 10

	wrong := strings.Repeat("ab", 32)
	if _, err := eng.AddDirect(ts.URL+"/image.iso", "image.iso", wrong); err != nil {
		t.Fatal(err)
	}
	snap := awaitDirect(t, eng, func(s Snapshot) bool { return s.State == StatePaused && s.Note != "" }, 10*time.Second)
	if !strings.Contains(snap.Note, "checksum") {
		t.Fatalf("Note = %q, want a checksum message", snap.Note)
	}
	dest := filepath.Join(eng.cfg.DownloadDir, "image.iso")
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("final file must not exist after a checksum mismatch")
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatal("partial data must be discarded after a checksum mismatch")
	}
	if _, err := os.Stat(dest + ".part.meta"); !os.IsNotExist(err) {
		t.Fatal("resume manifest must be discarded after a checksum mismatch")
	}
	if snap.BytesCompleted != 0 {
		t.Fatalf("checksum mismatch progress = %d, want 0", snap.BytesCompleted)
	}
}

func TestDirectResumesFromPartFileWithRange(t *testing.T) {
	payload := make([]byte, 256<<10)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	var rangeHits atomic.Int32
	ts := serveISO(t, payload, &rangeHits)
	eng := newDirectTestEngine(t)

	// pretend an earlier run got half the file
	dest := filepath.Join(eng.cfg.DownloadDir, "image.iso")
	if err := os.WriteFile(dest+".part", payload[:len(payload)/2], 0o644); err != nil {
		t.Fatal(err)
	}

	h, err := eng.AddDirect(ts.URL+"/image.iso", "image.iso", sumHex(payload))
	if err != nil {
		t.Fatal(err)
	}
	awaitDirect(t, eng, func(s Snapshot) bool { return s.Hash == h && s.State == StateDone }, 10*time.Second)

	if rangeHits.Load() == 0 {
		t.Fatal("expected the resume to use a Range request")
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("resumed bytes differ from payload (checksum should have caught this)")
	}
}

func TestParallelDirectDownloadUsesBoundedRangeWorkers(t *testing.T) {
	payload := make([]byte, 2<<20)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	var active, peak, ranges, badHeaders atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Deliberately omit Accept-Ranges and reject HEAD: the 0-0 GET probe is
		// authoritative and must still enable segmented downloading.
		w.Header().Set("ETag", `"fixed"`)
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var start, end int
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
			w.Write(payload)
			return
		}
		ranges.Add(1)
		if r.Header.Get("Accept-Encoding") != "identity" || r.Header.Get("User-Agent") != directUserAgent ||
			(r.Header.Get("Range") != "bytes=0-0" && r.Header.Get("If-Range") != `"fixed"`) {
			badHeaders.Add(1)
		}
		now := active.Add(1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		defer active.Add(-1)
		time.Sleep(10 * time.Millisecond)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(payload[start : end+1])
	}))
	defer ts.Close()

	eng := newDirectTestEngine(t)
	eng.directMinChunkSize = 64 << 10
	eng.directMaxConnections = 4
	h, err := eng.AddDirect(ts.URL+"/image.iso", "image.iso", sumHex(payload))
	if err != nil {
		t.Fatal(err)
	}
	awaitDirect(t, eng, func(s Snapshot) bool { return s.Hash == h && s.State == StateDone }, 10*time.Second)
	if ranges.Load() < 3 || peak.Load() < 2 || peak.Load() > 4 || badHeaders.Load() != 0 {
		t.Fatalf("range requests=%d, peak concurrency=%d, bad headers=%d", ranges.Load(), peak.Load(), badHeaders.Load())
	}
	got, err := os.ReadFile(filepath.Join(eng.cfg.DownloadDir, "image.iso"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("parallel payload mismatch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(eng.cfg.DownloadDir, "image.iso.part.meta")); !os.IsNotExist(err) {
		t.Fatalf("manifest remained after completion: %v", err)
	}
}

func TestParallelDirectFallsBackWhenRangesUnsupported(t *testing.T) {
	payload := bytes.Repeat([]byte("fallback"), 1<<15)
	var rangeAttempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			rangeAttempts.Add(1)
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		if r.Method != http.MethodHead {
			w.Write(payload)
		}
	}))
	defer ts.Close()
	eng := newDirectTestEngine(t)
	eng.directMinChunkSize = 32 << 10
	h, err := eng.AddDirect(ts.URL+"/image.iso", "image.iso", sumHex(payload))
	if err != nil {
		t.Fatal(err)
	}
	awaitDirect(t, eng, func(s Snapshot) bool { return s.Hash == h && s.State == StateDone }, 10*time.Second)
	if rangeAttempts.Load() == 0 {
		t.Fatal("range capability was not probed")
	}
	got, err := os.ReadFile(filepath.Join(eng.cfg.DownloadDir, "image.iso"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("fallback payload mismatch: %v", err)
	}
}

func TestParallelPauseWaitsAndResumeCompletes(t *testing.T) {
	payload := make([]byte, 512<<10)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	var active atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"fixed"`)
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		if r.Method == http.MethodHead {
			return
		}
		var start, end int
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
			w.Write(payload)
			return
		}
		active.Add(1)
		defer active.Add(-1)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		for offset := start; offset <= end; {
			next := min(end+1, offset+(4<<10))
			if _, err := w.Write(payload[offset:next]); err != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			offset = next
			time.Sleep(time.Millisecond)
		}
	}))
	defer ts.Close()
	eng := newDirectTestEngine(t)
	eng.directMinChunkSize = 64 << 10
	h, err := eng.AddDirect(ts.URL+"/image.iso", "image.iso", sumHex(payload))
	if err != nil {
		t.Fatal(err)
	}
	awaitDirect(t, eng, func(s Snapshot) bool { return s.Hash == h && s.BytesCompleted > 0 }, 5*time.Second)
	if err := eng.Pause(h); err != nil {
		t.Fatal(err)
	}
	if active.Load() != 0 {
		t.Fatalf("Pause returned with %d active requests", active.Load())
	}
	if err := eng.Resume(h); err != nil {
		t.Fatal(err)
	}
	awaitDirect(t, eng, func(s Snapshot) bool { return s.Hash == h && s.State == StateDone }, 10*time.Second)
	got, err := os.ReadFile(filepath.Join(eng.cfg.DownloadDir, "image.iso"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("resumed payload mismatch: %v", err)
	}
}

func TestParallelProtocolViolationRestartsSequentially(t *testing.T) {
	payload := bytes.Repeat([]byte("restart"), 1<<15)
	var probes, sequential atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"fixed"`)
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		if r.Method == http.MethodHead {
			return
		}
		if r.Header.Get("Range") == "bytes=0-0" && probes.Add(1) == 1 {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", len(payload)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(payload[:1])
			return
		}
		if r.Header.Get("Range") != "" {
			// A server that lied during discovery. The engine must discard all
			// segmented bytes and restart with a clean non-range request.
			w.WriteHeader(http.StatusOK)
			w.Write(payload)
			return
		}
		sequential.Add(1)
		w.Write(payload)
	}))
	defer ts.Close()
	eng := newDirectTestEngine(t)
	eng.directMinChunkSize = 32 << 10
	h, err := eng.AddDirect(ts.URL+"/image.iso", "image.iso", sumHex(payload))
	if err != nil {
		t.Fatal(err)
	}
	awaitDirect(t, eng, func(s Snapshot) bool { return s.Hash == h && s.State == StateDone }, 10*time.Second)
	if sequential.Load() != 1 {
		t.Fatalf("clean sequential retries = %d, want 1", sequential.Load())
	}
	got, err := os.ReadFile(filepath.Join(eng.cfg.DownloadDir, "image.iso"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("restart payload mismatch: %v", err)
	}
}

func TestDirectManifestRejectsStaleAndOverlappingRanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.iso.part.meta")
	m := directManifest{Version: directManifestVersion, URL: "https://example/x", Total: 100,
		Validator: `"v1"`, Completed: []byteRange{{Start: 0, End: 60}, {Start: 50, End: 90}}}
	if err := saveDirectManifest(path, m); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDirectManifest(path, m.URL, m.Total, m.Validator); err == nil {
		t.Fatal("overlapping manifest was accepted")
	}
	m.Completed = []byteRange{{Start: 0, End: 49}}
	if err := saveDirectManifest(path, m); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDirectManifest(path, m.URL, m.Total+1, m.Validator); err == nil {
		t.Fatal("stale manifest total was accepted")
	}
}

func TestDirectRangeHelpersRejectInvalidCoverage(t *testing.T) {
	if _, _, _, ok := parseContentRange("bytes 5-2/10"); ok {
		t.Fatal("accepted reversed content range")
	}
	got := missingDirectRanges(10, []byteRange{{Start: 0, End: 2}, {Start: 7, End: 9}}, 2)
	want := []byteRange{{Start: 3, End: 4}, {Start: 5, End: 6}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("missing ranges = %+v, want %+v", got, want)
	}
	scheduler := newDirectRangeScheduler(100, []byteRange{{Start: 40, End: 59}})
	first, ok := scheduler.claim(10)
	if !ok || first != (byteRange{Start: 20, End: 39}) {
		t.Fatalf("first adaptive claim = %+v, %v", first, ok)
	}
	second, ok := scheduler.claim(10)
	if !ok || second != (byteRange{Start: 80, End: 99}) {
		t.Fatalf("second adaptive claim = %+v, %v", second, ok)
	}
}

func TestAddDirectRejectsUnsafeName(t *testing.T) {
	eng := newDirectTestEngine(t)
	if _, err := eng.AddDirect("https://example.com/x.iso", "../../evil.iso", ""); err == nil {
		t.Fatal("expected a path-traversal name to be rejected")
	}
}

func TestVerifyDirectAcceptsValidCompletedFile(t *testing.T) {
	payload := []byte("completed direct download")
	ts := serveISO(t, payload, nil)
	eng := newDirectTestEngine(t)

	h, err := eng.AddDirect(ts.URL+"/image.iso", "image.iso", sumHex(payload))
	if err != nil {
		t.Fatal(err)
	}
	awaitDirect(t, eng, func(s Snapshot) bool { return s.Hash == h && s.State == StateDone }, 10*time.Second)

	result, err := eng.Verify(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	if result.NeedsRepair || result.ChecksumMismatch {
		t.Fatalf("Verify result = %+v, want valid", result)
	}
	if snap, ok := eng.Snapshot(h); !ok || snap.State != StateDone {
		t.Fatalf("snapshot = %+v, ok=%v; want done", snap, ok)
	}
}

func TestVerifyDirectRecognizesPersistedFileSynchronously(t *testing.T) {
	payload := []byte("persisted direct download")
	eng := newDirectTestEngine(t)
	dest := filepath.Join(eng.cfg.DownloadDir, "image.iso")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	h, err := eng.AddDirect("https://example.invalid/image.iso", "image.iso", sumHex(payload))
	if err != nil {
		t.Fatal(err)
	}
	if snap, ok := eng.Snapshot(h); !ok || snap.State != StateDone {
		t.Fatalf("snapshot immediately after activation = %+v, ok=%v; want done", snap, ok)
	}
	if _, err := eng.Verify(context.Background(), h); err != nil {
		t.Fatalf("verify persisted file: %v", err)
	}
}

func TestVerifyDirectQuarantinesMismatchWithoutOverwritingExistingQuarantine(t *testing.T) {
	payload := []byte("known-good direct download")
	ts := serveISO(t, payload, nil)
	eng := newDirectTestEngine(t)

	h, err := eng.AddDirect(ts.URL+"/image.iso", "image.iso", sumHex(payload))
	if err != nil {
		t.Fatal(err)
	}
	awaitDirect(t, eng, func(s Snapshot) bool { return s.Hash == h && s.State == StateDone }, 10*time.Second)
	dest := filepath.Join(eng.cfg.DownloadDir, "image.iso")
	if err := os.WriteFile(dest, []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest+".corrupt", []byte("older corrupt copy"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := eng.Verify(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NeedsRepair || !result.ChecksumMismatch || result.QuarantinePath != dest+".corrupt.1" {
		t.Fatalf("Verify result = %+v", result)
	}
	if got, err := os.ReadFile(result.QuarantinePath); err != nil || string(got) != "corrupt" {
		t.Fatalf("quarantine = %q, err=%v", got, err)
	}
	if got, err := os.ReadFile(dest + ".corrupt"); err != nil || string(got) != "older corrupt copy" {
		t.Fatalf("existing quarantine changed: %q, err=%v", got, err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("original corrupt path still exists: %v", err)
	}
	if snap, ok := eng.Snapshot(h); !ok || snap.State != StatePaused || snap.BytesCompleted != 0 || !strings.Contains(snap.Note, "checksum mismatch") {
		t.Fatalf("snapshot = %+v, ok=%v; want paused checksum failure", snap, ok)
	}
}

func TestVerifyDirectRequiresChecksum(t *testing.T) {
	payload := []byte("unverified direct download")
	ts := serveISO(t, payload, nil)
	eng := newDirectTestEngine(t)

	h, err := eng.AddDirect(ts.URL+"/image.iso", "image.iso", "")
	if err != nil {
		t.Fatal(err)
	}
	awaitDirect(t, eng, func(s Snapshot) bool { return s.Hash == h && s.State == StateDone }, 10*time.Second)
	if _, err := eng.Verify(context.Background(), h); !errors.Is(err, ErrNoChecksum) {
		t.Fatalf("Verify error = %v, want ErrNoChecksum", err)
	}
}

func TestVerifyDirectReportsMissingFileWithoutChangingState(t *testing.T) {
	payload := []byte("completed direct download")
	ts := serveISO(t, payload, nil)
	eng := newDirectTestEngine(t)

	h, err := eng.AddDirect(ts.URL+"/image.iso", "image.iso", sumHex(payload))
	if err != nil {
		t.Fatal(err)
	}
	before := awaitDirect(t, eng, func(s Snapshot) bool { return s.Hash == h && s.State == StateDone }, 10*time.Second)
	if err := os.Remove(filepath.Join(eng.cfg.DownloadDir, "image.iso")); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Verify(context.Background(), h); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Verify error = %v, want missing file", err)
	}
	if after, ok := eng.Snapshot(h); !ok || after.State != StateDone || after.BytesCompleted != before.BytesCompleted || after.Length != before.Length {
		t.Fatalf("snapshot after filesystem error = %+v, ok=%v; want %+v", after, ok, before)
	}
}

func TestVerifyDirectHonorsCanceledContext(t *testing.T) {
	payload := make([]byte, 1<<20)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	ts := serveISO(t, payload, nil)
	eng := newDirectTestEngine(t)

	h, err := eng.AddDirect(ts.URL+"/image.iso", "image.iso", sumHex(payload))
	if err != nil {
		t.Fatal(err)
	}
	awaitDirect(t, eng, func(s Snapshot) bool { return s.Hash == h && s.State == StateDone }, 10*time.Second)
	before, _ := eng.Snapshot(h)
	dest := filepath.Join(eng.cfg.DownloadDir, "image.iso")
	if err := os.Truncate(dest, int64(len(payload)/2)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := eng.Verify(ctx, h); !errors.Is(err, context.Canceled) {
		t.Fatalf("Verify error = %v, want context.Canceled", err)
	}
	if snap, ok := eng.Snapshot(h); !ok || snap.State != StateDone || snap.BytesCompleted != before.BytesCompleted || snap.Length != before.Length {
		t.Fatalf("snapshot = %+v, ok=%v; canceled verification must restore %+v", snap, ok, before)
	}
}
