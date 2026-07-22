package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
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
	seederClosed := false
	defer func() {
		if !seederClosed {
			seeder.Close()
		}
	}()
	_, err = seeder.AddTorrent(&mi)
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
	seed := false
	h, err := eng.AddWithOptions(magnet, AddOptions{Seed: &seed})
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

	validResult, err := eng.Verify(context.Background(), h)
	if err != nil {
		eng.Close()
		t.Fatalf("verify valid torrent: %v", err)
	}
	if validResult.CheckedPieces == 0 || validResult.BadPieces != 0 || validResult.NeedsRepair {
		eng.Close()
		t.Fatalf("valid Verify result = %+v", validResult)
	}

	payloadPath := filepath.Join(cfg.DownloadDir, "payload.dat")
	// Repeat the non-seeding recovery transition so peer reconnection and piece
	// reprioritization are exercised after every completed repair.
	for attempt := 0; attempt < 5; attempt++ {
		corrupt := append([]byte(nil), payload...)
		corrupt[(attempt+1)*(len(corrupt)/6)] ^= 0xff
		if err := os.Chmod(payloadPath, 0o644); err != nil {
			eng.Close()
			t.Fatal(err)
		}
		if err := os.WriteFile(payloadPath, corrupt, 0o644); err != nil {
			eng.Close()
			t.Fatal(err)
		}
		badResult, err := eng.Verify(context.Background(), h)
		if err != nil {
			eng.Close()
			t.Fatalf("verify corrupt torrent (attempt %d): %v", attempt, err)
		}
		if badResult.BadPieces == 0 || !badResult.NeedsRepair {
			eng.Close()
			t.Fatalf("corrupt Verify result (attempt %d) = %+v", attempt, badResult)
		}
		awaitComplete(t, eng, h, 60*time.Second)
		repaired, err := os.ReadFile(payloadPath)
		if err != nil || !bytes.Equal(repaired, payload) {
			eng.Close()
			t.Fatalf("repaired payload differs on attempt %d, err=%v", attempt, err)
		}
	}
	eng.Close() // releases the bolt piece-completion lock

	// --- offline resume: cached metainfo plus bolt piece completion must make a
	// fresh engine complete from disk after the only seeder is gone.
	seeder.Close()
	seederClosed = true

	eng2, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()
	h2, err := eng2.Add(magnet, nil)
	if err != nil {
		t.Fatal(err)
	}
	if snap, ok := eng2.Snapshot(h2); !ok || snap.Metadata.Source != MetadataCache {
		t.Fatalf("resume metadata = %+v, ok=%v; want cache", snap.Metadata, ok)
	}
	snap = awaitComplete(t, eng2, h2, 90*time.Second)
	if snap.BytesCompleted != int64(len(payload)) {
		t.Fatalf("resume completed %d bytes, want %d", snap.BytesCompleted, len(payload))
	}
}

func TestAddWithOptionsRecordsDownloadDir(t *testing.T) {
	cfg := strictProxyConfig(t, "127.0.0.1:1")
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

func TestVerifyWaitsForMetadataAndHonorsContext(t *testing.T) {
	cfg := strictProxyConfig(t, "127.0.0.1:1")
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	h, err := eng.Add("magnet:?xt=urn:btih:"+strings.Repeat("b", 40), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	verifyErr := make(chan error, 1)
	go func() {
		_, err := eng.Verify(ctx, h)
		verifyErr <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if snap, ok := eng.Snapshot(h); ok && snap.State == StateVerifying {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("verification did not enter verifying state")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := eng.Verify(context.Background(), h); !errors.Is(err, ErrVerificationInProgress) {
		t.Fatalf("duplicate Verify error = %v, want ErrVerificationInProgress", err)
	}
	if err := eng.Pause(h); !errors.Is(err, ErrVerificationInProgress) {
		t.Fatalf("Pause error = %v, want ErrVerificationInProgress", err)
	}
	if err := eng.SetSeeding(h, true); !errors.Is(err, ErrVerificationInProgress) {
		t.Fatalf("SetSeeding error = %v, want ErrVerificationInProgress", err)
	}
	if err := eng.Remove(h, false); !errors.Is(err, ErrVerificationInProgress) {
		t.Fatalf("Remove error = %v, want ErrVerificationInProgress", err)
	}
	cancel()
	if err := <-verifyErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("Verify error = %v, want context canceled", err)
	}
	if snap, ok := eng.Snapshot(h); !ok || snap.State != StateFetchingMeta {
		t.Fatalf("snapshot = %+v, ok=%v; want metadata fetch restored", snap, ok)
	}
}

func TestLocalTorrentFilePreviewCachesForOfflineReopen(t *testing.T) {
	seedDir := t.TempDir()
	payloadPath := filepath.Join(seedDir, "local.bin")
	if err := os.WriteFile(payloadPath, []byte("local torrent payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 16 << 10}
	if err := info.BuildFromFilePath(payloadPath); err != nil {
		t.Fatal(err)
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes, Announce: "https://tracker.example/announce"}
	torrentPath := filepath.Join(t.TempDir(), "local.torrent")
	f, err := os.Create(torrentPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := mi.Write(f); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	cfg := strictProxyConfig(t, "127.0.0.1:1")
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h, name, magnet, owned, err := eng.AddTorrentFileForPreview(torrentPath)
	if err != nil {
		eng.Close()
		t.Fatal(err)
	}
	if !owned || name != "local.bin" {
		eng.Close()
		t.Fatalf("preview = hash %s name %q owned %v", h.HexString(), name, owned)
	}
	files, ready := eng.Files(h)
	if !ready || len(files) != 1 || files[0].Path != "local.bin" {
		eng.Close()
		t.Fatalf("files = %+v, ready=%v", files, ready)
	}
	if snap, ok := eng.Snapshot(h); !ok || snap.Metadata.Source != MetadataFile {
		eng.Close()
		t.Fatalf("local metadata = %+v, ok=%v", snap.Metadata, ok)
	}
	eng.Close()
	if err := os.Remove(torrentPath); err != nil {
		t.Fatal(err)
	}

	eng2, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()
	h2, owned, err := eng2.AddForPreview(magnet)
	if err != nil {
		t.Fatal(err)
	}
	if !owned || h2 != h {
		t.Fatalf("cached preview = hash %s owned %v", h2.HexString(), owned)
	}
	if files, ready := eng2.Files(h2); !ready || len(files) != 1 {
		t.Fatalf("cached files = %+v, ready=%v", files, ready)
	}
	if snap, ok := eng2.Snapshot(h2); !ok || snap.Metadata.Source != MetadataCache {
		t.Fatalf("cached metadata = %+v, ok=%v", snap.Metadata, ok)
	}
}

func TestInvalidSemanticCacheFallsBackToMagnet(t *testing.T) {
	cfg := strictProxyConfig(t, "127.0.0.1:1")
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	// Decodes as an info dictionary, but has no piece hash for its one piece;
	// the torrent client rejects it even though the cache's lightweight parser
	// can read it.
	infoBytes, err := bencode.Marshal(metainfo.Info{Name: "bad.bin", PieceLength: 16 << 10, Length: 1})
	if err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes}
	if err := eng.metainfo.Store(mi); err != nil {
		t.Fatal(err)
	}
	hash := mi.HashInfoBytes()
	h, _, err := eng.AddForPreview("magnet:?xt=urn:btih:" + hash.HexString())
	if err != nil {
		t.Fatalf("fallback magnet add failed: %v", err)
	}
	status, ok := eng.MetadataDiscovery(h)
	if !ok || status.Source != MetadataPeers {
		t.Fatalf("fallback discovery = %+v, ok=%v", status, ok)
	}
	cachePath := filepath.Join(cfg.MetadataCacheDir(), hash.HexString()+".torrent")
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatalf("rejected cache entry was not discarded: %v", err)
	}
}

func TestDisabledMetadataCacheDoesNotPersistLocalTorrent(t *testing.T) {
	payloadPath := filepath.Join(t.TempDir(), "disabled.bin")
	if err := os.WriteFile(payloadPath, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 16 << 10}
	if err := info.BuildFromFilePath(payloadPath); err != nil {
		t.Fatal(err)
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes}
	torrentPath := filepath.Join(t.TempDir(), "disabled.torrent")
	f, err := os.Create(torrentPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := mi.Write(f); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	cfg := strictProxyConfig(t, "127.0.0.1:1")
	cfg.MetadataCache.Enabled = false
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := eng.AddTorrentFileForPreview(torrentPath); err != nil {
		eng.Close()
		t.Fatal(err)
	}
	eng.Close()
	if _, err := os.Stat(cfg.MetadataCacheDir()); !os.IsNotExist(err) {
		t.Fatalf("disabled cache created directory: %v", err)
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
	if port == 0 {
		t.Fatal("seeder has no TCP listen addr")
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

	torrentPath := filepath.Join(t.TempDir(), "pack.torrent")
	f, err := os.Create(torrentPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := mi.Write(f); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	h, _, _, owned, err := eng.AddTorrentFileForPreview(torrentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !owned {
		t.Fatal("first local preview should own the torrent")
	}
	eng.mu.Lock()
	leecher := eng.items[h].t
	eng.mu.Unlock()
	leecher.AddPeers([]torrent.PeerInfo{{
		Addr:   &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: port},
		Source: torrent.PeerSourceDirect,
	}})
	files, ready := eng.Files(h)
	if !ready {
		t.Fatal("local metainfo was not ready immediately")
	}
	if len(files) != 3 {
		t.Fatalf("Files returned %d entries, want 3", len(files))
	}
	if snap, ok := eng.Snapshot(h); !ok || snap.Metadata.Source != MetadataFile {
		t.Fatalf("local preview metadata = %+v, ok=%v", snap.Metadata, ok)
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

// TestNoSeedDropsConnectionsOnCompletion verifies that a download added with
// Seed=false actually stops uploading once complete: the engine caps its peer
// connections instead of leaving the torrent seeding on the client-wide default.
// This is what makes `tork --evil` real rather than cosmetic.
func TestNoSeedDropsConnectionsOnCompletion(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}

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

	magnet := fmt.Sprintf("magnet:?xt=urn:btih:%s&x.pe=127.0.0.1:%d",
		mi.HashInfoBytes().HexString(), seederPort)
	noSeed := false
	h, err := eng.AddWithOptions(magnet, AddOptions{Seed: &noSeed})
	if err != nil {
		t.Fatal(err)
	}

	snap := awaitComplete(t, eng, h, 60*time.Second)
	if snap.State != StateDone {
		t.Fatalf("state = %v, want StateDone (a non-seeding item must not seed)", snap.State)
	}

	// Completion caps the torrent's connections; poll until they actually drop.
	deadline := time.After(10 * time.Second)
	for {
		s, ok := eng.Snapshot(h)
		if ok && s.PeersActive == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("connections not dropped after completion: PeersActive=%d", s.PeersActive)
		case <-time.After(200 * time.Millisecond):
		}
	}

	// Pausing drops the capped torrent handle. Resuming creates a fresh handle,
	// so the cap must be applied again when completion is observed.
	eng.Pause(h)
	if err := eng.Resume(h); err != nil {
		t.Fatal(err)
	}
	eng.mu.Lock()
	resumed := eng.items[h]
	if resumed == nil || resumed.t == nil {
		eng.mu.Unlock()
		t.Fatal("resume did not restore a torrent handle")
	}
	if resumed.noSeedApplied {
		eng.mu.Unlock()
		t.Fatal("resume kept noSeedApplied=true for a fresh torrent handle")
	}
	eng.mu.Unlock()
	snap = awaitComplete(t, eng, h, 60*time.Second)
	if snap.State != StateDone {
		t.Fatalf("state after resume = %v, want StateDone", snap.State)
	}
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
