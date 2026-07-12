package metacache

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/melqtx/tork/internal/config"
)

func testMetaInfo(t *testing.T, name string) metainfo.MetaInfo {
	t.Helper()
	info := metainfo.Info{Name: name, PieceLength: 16 << 10, Length: 1}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	return metainfo.MetaInfo{InfoBytes: infoBytes, Announce: "https://tracker.example/announce"}
}

func testCache(t *testing.T) (*Cache, *config.Config) {
	t.Helper()
	cfg := config.Default(t.TempDir())
	return New(cfg), cfg
}

func TestStoreLoadAndPermissions(t *testing.T) {
	cache, cfg := testCache(t)
	mi := testMetaInfo(t, "cached.bin")
	if err := cache.Store(mi); err != nil {
		t.Fatal(err)
	}
	got, ok := cache.Load(mi.HashInfoBytes())
	if !ok || !bytes.Equal(got.InfoBytes, mi.InfoBytes) {
		t.Fatalf("Load = %+v, %v", got, ok)
	}
	if runtime.GOOS != "windows" {
		dirInfo, _ := os.Stat(cfg.MetadataCacheDir())
		fileInfo, _ := os.Stat(cache.path(mi.HashInfoBytes()))
		if dirInfo.Mode().Perm() != 0o700 || fileInfo.Mode().Perm() != 0o600 {
			t.Fatalf("modes = dir %o file %o", dirInfo.Mode().Perm(), fileInfo.Mode().Perm())
		}
	}
}

func TestInvalidEntryIsRemoved(t *testing.T) {
	cache, _ := testCache(t)
	mi := testMetaInfo(t, "bad.bin")
	path := cache.path(mi.HashInfoBytes())
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not metainfo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Load(mi.HashInfoBytes()); ok {
		t.Fatal("loaded corrupt cache entry")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("corrupt entry was not removed: %v", err)
	}
}

func TestHashMismatchIsRemoved(t *testing.T) {
	cache, _ := testCache(t)
	a := testMetaInfo(t, "a.bin")
	b := testMetaInfo(t, "b.bin")
	if err := cache.Store(a); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cache.path(a.HashInfoBytes()))
	if err != nil {
		t.Fatal(err)
	}
	wrongPath := cache.path(b.HashInfoBytes())
	if err := os.WriteFile(wrongPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Load(b.HashInfoBytes()); ok {
		t.Fatal("loaded mismatched cache entry")
	}
	if _, err := os.Stat(wrongPath); !os.IsNotExist(err) {
		t.Fatalf("mismatched entry was not removed: %v", err)
	}
}

func TestPruneUsesLeastRecentlyUsedEntry(t *testing.T) {
	cache, _ := testCache(t)
	cache.maxEntries = 2
	items := []metainfo.MetaInfo{
		testMetaInfo(t, "old.bin"), testMetaInfo(t, "middle.bin"), testMetaInfo(t, "new.bin"),
	}
	for i, mi := range items {
		if err := cache.Store(mi); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(time.Duration(i-10) * time.Hour)
		if err := os.Chtimes(cache.path(mi.HashInfoBytes()), at, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := cache.prune(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cache.path(items[0].HashInfoBytes())); !os.IsNotExist(err) {
		t.Fatalf("oldest entry survived prune: %v", err)
	}
	for _, mi := range items[1:] {
		if _, err := os.Stat(cache.path(mi.HashInfoBytes())); err != nil {
			t.Fatalf("newer entry missing: %v", err)
		}
	}
}

func TestLoadRefreshesRecency(t *testing.T) {
	cache, _ := testCache(t)
	cache.maxEntries = 2
	a := testMetaInfo(t, "a.bin")
	b := testMetaInfo(t, "b.bin")
	c := testMetaInfo(t, "c.bin")
	for _, mi := range []metainfo.MetaInfo{a, b} {
		if err := cache.Store(mi); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	newer := time.Now().Add(-time.Hour)
	if err := os.Chtimes(cache.path(a.HashInfoBytes()), old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(cache.path(b.HashInfoBytes()), newer, newer); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Load(a.HashInfoBytes()); !ok {
		t.Fatal("cache hit failed")
	}
	if err := cache.Store(c); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cache.path(b.HashInfoBytes())); !os.IsNotExist(err) {
		t.Fatalf("untouched entry survived LRU prune: %v", err)
	}
	for _, mi := range []metainfo.MetaInfo{a, c} {
		if _, err := os.Stat(cache.path(mi.HashInfoBytes())); err != nil {
			t.Fatalf("recent entry missing: %v", err)
		}
	}
}

func TestPruneEnforcesByteLimit(t *testing.T) {
	cache, _ := testCache(t)
	cache.maxBytes = 1
	mi := testMetaInfo(t, "too-big-for-cache-cap.bin")
	if err := cache.Store(mi); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cache.path(mi.HashInfoBytes())); !os.IsNotExist(err) {
		t.Fatalf("byte cap did not prune entry: %v", err)
	}
}

func TestStoreRejectsOversizedMetainfo(t *testing.T) {
	cache, _ := testCache(t)
	mi := testMetaInfo(t, "oversized.bin")
	mi.Comment = string(make([]byte, (16<<20)+1))
	if err := cache.Store(mi); err == nil {
		t.Fatal("stored oversized metainfo")
	}
	if _, err := os.Stat(cache.path(mi.HashInfoBytes())); !os.IsNotExist(err) {
		t.Fatalf("oversized metainfo left a cache entry: %v", err)
	}
}

func TestInspectIsReadOnly(t *testing.T) {
	cache, cfg := testCache(t)
	mi := testMetaInfo(t, "inspect.bin")
	if err := cache.Store(mi); err != nil {
		t.Fatal(err)
	}
	path := cache.path(mi.HashInfoBytes())
	before, _ := os.Stat(path)
	rep := New(cfg).Inspect()
	after, _ := os.Stat(path)
	if rep.Entries != 1 || rep.Invalid != 0 || rep.Err != nil {
		t.Fatalf("Inspect = %+v", rep)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("Inspect touched cache recency")
	}
}

func TestInspectReportsInsecurePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows permissions are ACL-based")
	}
	cache, _ := testCache(t)
	mi := testMetaInfo(t, "permissions.bin")
	if err := cache.Store(mi); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cache.path(mi.HashInfoBytes()), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := cache.Inspect()
	if rep.Insecure != 1 {
		t.Fatalf("Inspect = %+v, want one insecure file", rep)
	}
	info, _ := os.Stat(cache.path(mi.HashInfoBytes()))
	if info.Mode().Perm() != 0o644 {
		t.Fatal("Inspect repaired permissions")
	}
}

func TestStoreRefusesSymlinkedCacheDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation commonly requires elevated privileges")
	}
	cache, cfg := testCache(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, cfg.MetadataCacheDir()); err != nil {
		t.Fatal(err)
	}
	mi := testMetaInfo(t, "escape.bin")
	if err := cache.Store(mi); err == nil {
		t.Fatal("stored through symlinked cache directory")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("outside directory changed: %v, %v", entries, err)
	}
}

func TestDisabledCacheDoesNothing(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.MetadataCache.Enabled = false
	cache := New(cfg)
	mi := testMetaInfo(t, "disabled.bin")
	if err := cache.Store(mi); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Load(mi.HashInfoBytes()); ok {
		t.Fatal("disabled cache returned a hit")
	}
	if _, err := os.Stat(cfg.MetadataCacheDir()); !os.IsNotExist(err) {
		t.Fatalf("disabled cache created files: %v", err)
	}
}
