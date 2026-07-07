package engine

import (
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	analog "github.com/anacrolix/log"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/melqtx/tork/internal/config"
)

// TestEngineDownloadsFromLocalSeeder exercises the full engine path - Add,
// metadata fetch, download, snapshots, completion, and resume-after-restart -
// against a local anacrolix seeder, no external network needed. The magnet
// carries an x.pe peer hint pointing at the seeder.
func TestEngineDownloadsFromLocalSeeder(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}

	// --- seed data + metainfo
	seedDir := t.TempDir()
	payload := make([]byte, 512<<10)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seedDir, "payload.dat"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 32 << 10}
	if err := info.BuildFromFilePath(filepath.Join(seedDir, "payload.dat")); err != nil {
		t.Fatal(err)
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes}

	// --- seeder client
	scc := torrent.NewDefaultClientConfig()
	scc.DataDir = seedDir
	scc.Seed = true
	scc.NoDHT = true
	scc.DisableTrackers = true
	scc.ListenPort = 0 // random free port
	silent := analog.NewLogger("seeder")
	silent.Handlers = []analog.Handler{analog.DiscardHandler}
	scc.Logger = silent
	seeder, err := torrent.NewClient(scc)
	if err != nil {
		t.Fatal(err)
	}
	defer seeder.Close()
	seederT, err := seeder.AddTorrent(&mi)
	if err != nil {
		t.Fatal(err)
	}

	var seederPort int
	for _, addr := range seeder.ListenAddrs() {
		if tcp, ok := addr.(*net.TCPAddr); ok {
			seederPort = tcp.Port
			break
		}
	}
	if seederPort == 0 {
		t.Fatal("seeder has no TCP listen addr")
	}

	// --- engine under test: download to completion
	t.Setenv("XDG_DOWNLOAD_DIR", filepath.Join(t.TempDir(), "Downloads"))
	dir := filepath.Join(t.TempDir(), ".tork")
	cfg, err := config.LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	magnet := fmt.Sprintf("magnet:?xt=urn:btih:%s&x.pe=127.0.0.1:%d",
		mi.HashInfoBytes().HexString(), seederPort)
	h, err := eng.Add(magnet, nil)
	if err != nil {
		eng.Close()
		t.Fatal(err)
	}
	snap := awaitComplete(t, eng, h, 60*time.Second)
	if snap.BytesCompleted != int64(len(payload)) {
		eng.Close()
		t.Fatalf("completed %d bytes, want %d", snap.BytesCompleted, len(payload))
	}
	got, err := os.ReadFile(filepath.Join(cfg.DownloadDir, "payload.dat"))
	if err != nil || len(got) != len(payload) {
		eng.Close()
		t.Fatalf("downloaded file: %d bytes, err=%v; want %d bytes", len(got), err, len(payload))
	}
	eng.Close() // releases the bolt piece-completion lock

	// --- resume: a fresh engine on the same dir must complete from disk.
	// Metadata still comes from the seeder, but bolt piece completion means
	// no payload is re-transferred - assert via the seeder's upload counter.
	statsBefore := seederT.Stats()
	uploadedBefore := statsBefore.BytesWrittenData.Int64()

	eng2, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()
	h2, err := eng2.Add(magnet, nil)
	if err != nil {
		t.Fatal(err)
	}
	snap = awaitComplete(t, eng2, h2, 90*time.Second)
	if snap.BytesCompleted != int64(len(payload)) {
		t.Fatalf("resume completed %d bytes, want %d", snap.BytesCompleted, len(payload))
	}
	statsAfter := seederT.Stats()
	uploadedDuringResume := statsAfter.BytesWrittenData.Int64() - uploadedBefore
	if uploadedDuringResume > 64<<10 {
		t.Errorf("resume re-downloaded %d bytes from seeder; piece completion should have prevented that", uploadedDuringResume)
	}
}

func TestAddWithOptionsRecordsDownloadDir(t *testing.T) {
	t.Setenv("XDG_DOWNLOAD_DIR", filepath.Join(t.TempDir(), "Downloads"))
	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), ".tork"))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	dir := t.TempDir()
	magnet := "magnet:?xt=urn:btih:" + strings.Repeat("a", 40)
	h, err := eng.AddWithOptions(magnet, AddOptions{DownloadDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	snap, ok := eng.Snapshot(h)
	if !ok {
		t.Fatal("snapshot missing")
	}
	if snap.DownloadDir != dir {
		t.Fatalf("DownloadDir = %q, want %q", snap.DownloadDir, dir)
	}
}

// TestPreviewAndExcludeFiles builds a 3-file torrent, previews it (metadata
// only, no download), then downloads while excluding the largest file. The
// excluded file must not land on disk and the progress length must equal the
// selected bytes.
func TestPreviewAndExcludeFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}

	seedDir := t.TempDir()
	content := filepath.Join(seedDir, "pack")
	if err := os.MkdirAll(content, 0o755); err != nil {
		t.Fatal(err)
	}
	sizes := map[string]int{"a.dat": 128 << 10, "b.dat": 96 << 10, "big.dat": 256 << 10}
	for name, sz := range sizes {
		buf := make([]byte, sz)
		rand.Read(buf)
		if err := os.WriteFile(filepath.Join(content, name), buf, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	info := metainfo.Info{PieceLength: 32 << 10}
	if err := info.BuildFromFilePath(content); err != nil {
		t.Fatal(err)
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes}

	scc := torrent.NewDefaultClientConfig()
	scc.DataDir = seedDir
	scc.Seed = true
	scc.NoDHT = true
	scc.DisableTrackers = true
	scc.ListenPort = 0
	silent := analog.NewLogger("seeder")
	silent.Handlers = []analog.Handler{analog.DiscardHandler}
	scc.Logger = silent
	seeder, err := torrent.NewClient(scc)
	if err != nil {
		t.Fatal(err)
	}
	defer seeder.Close()
	if _, err := seeder.AddTorrent(&mi); err != nil {
		t.Fatal(err)
	}
	var port int
	for _, addr := range seeder.ListenAddrs() {
		if tcp, ok := addr.(*net.TCPAddr); ok {
			port = tcp.Port
			break
		}
	}

	t.Setenv("XDG_DOWNLOAD_DIR", filepath.Join(t.TempDir(), "Downloads"))
	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), ".tork"))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	magnet := fmt.Sprintf("magnet:?xt=urn:btih:%s&x.pe=127.0.0.1:%d", mi.HashInfoBytes().HexString(), port)
	h, owned, err := eng.AddForPreview(magnet)
	if err != nil {
		t.Fatal(err)
	}
	if !owned {
		t.Fatal("first preview add should own the metadata-only torrent")
	}

	// wait for metadata; nothing should download while previewing
	var files []FileInfo
	deadline := time.After(90 * time.Second)
	for files == nil {
		select {
		case <-deadline:
			t.Fatal("metadata never arrived for preview")
		case <-time.After(100 * time.Millisecond):
		}
		if fs, ok := eng.Files(h); ok {
			files = fs
		}
	}
	if len(files) != 3 {
		t.Fatalf("Files returned %d entries, want 3", len(files))
	}

	// find the largest file (big.dat) and exclude it
	bigIdx, bigLen := -1, int64(0)
	var selectedBytes int64
	for _, f := range files {
		if f.Length > bigLen {
			bigLen = f.Length
			bigIdx = f.Index
		}
	}
	for _, f := range files {
		if f.Index != bigIdx {
			selectedBytes += f.Length
		}
	}

	eng.StartDownload(h, []int{bigIdx})
	snap := awaitComplete(t, eng, h, 90*time.Second)
	if snap.Length != selectedBytes {
		t.Errorf("Snapshot.Length = %d, want selected bytes %d", snap.Length, selectedBytes)
	}
	if snap.Progress() < 0.999 {
		t.Errorf("progress = %.3f, want ~1.0", snap.Progress())
	}

	// the excluded file must be absent or zero-length on disk
	bigPath := filepath.Join(cfg.DownloadDir, "pack", "big.dat")
	if fi, err := os.Stat(bigPath); err == nil && fi.Size() > 0 {
		t.Errorf("excluded file big.dat is %d bytes on disk, want absent/empty", fi.Size())
	}
	// an included file must be fully present
	aPath := filepath.Join(cfg.DownloadDir, "pack", "a.dat")
	if fi, err := os.Stat(aPath); err != nil || fi.Size() != int64(sizes["a.dat"]) {
		t.Errorf("included file a.dat: size=%v err=%v, want %d", fileSize(fi), err, sizes["a.dat"])
	}
}

func fileSize(fi os.FileInfo) int64 {
	if fi == nil {
		return -1
	}
	return fi.Size()
}

func awaitComplete(t *testing.T, eng *Engine, h metainfo.Hash, timeout time.Duration) Snapshot {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			t.Fatalf("download did not complete: %+v", eng.Snapshots())
		case <-time.After(200 * time.Millisecond):
		}
		for _, s := range eng.Snapshots() {
			if s.Hash == h && (s.State == StateSeeding || s.State == StateDone) {
				return s
			}
		}
	}
}
