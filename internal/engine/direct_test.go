package engine

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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

func TestAddDirectRejectsUnsafeName(t *testing.T) {
	eng := newDirectTestEngine(t)
	if _, err := eng.AddDirect("https://example.com/x.iso", "../../evil.iso", ""); err == nil {
		t.Fatal("expected a path-traversal name to be rejected")
	}
}
