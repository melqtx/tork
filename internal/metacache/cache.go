// Package metacache persists validated torrent metainfo by infohash. It is a
// derived, bounded cache: corrupt entries may always be discarded safely.
package metacache

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/intake"
)

const (
	defaultMaxMB      = 256
	defaultMaxEntries = 512
)

type Cache struct {
	mu         sync.Mutex
	dir        string
	enabled    bool
	maxBytes   int64
	maxEntries int
}

type Report struct {
	Enabled  bool
	Exists   bool
	Entries  int
	Bytes    int64
	Invalid  int
	Insecure int
	Err      error
}

func New(cfg *config.Config) *Cache {
	maxMB := cfg.MetadataCache.MaxMB
	if maxMB < 1 || int64(maxMB) > int64(^uint64(0)>>1)>>20 {
		maxMB = defaultMaxMB
	}
	maxEntries := cfg.MetadataCache.MaxEntries
	if maxEntries < 1 {
		maxEntries = defaultMaxEntries
	}
	return &Cache{
		dir: cfg.MetadataCacheDir(), enabled: cfg.MetadataCache.Enabled,
		maxBytes: int64(maxMB) << 20, maxEntries: maxEntries,
	}
}

func (c *Cache) Enabled() bool { return c != nil && c.enabled }

func (c *Cache) Discard(hash metainfo.Hash) {
	if !c.Enabled() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = os.Remove(c.path(hash))
}

// Load returns a validated entry. Invalid derived files are removed and
// treated as misses so peer discovery can continue normally.
func (c *Cache) Load(hash metainfo.Hash) (*metainfo.MetaInfo, bool) {
	if !c.Enabled() {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateCacheDir(c.dir); err != nil {
		return nil, false
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Lstat(c.dir); err != nil || info.Mode().Perm()&0o077 != 0 {
			return nil, false
		}
	}
	path := c.path(hash)
	mi, err := readValidated(path, hash)
	if err != nil {
		if !os.IsNotExist(err) && isInvalid(err) {
			_ = os.Remove(path)
		}
		return nil, false
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return mi, true
}

// Store validates and atomically writes one entry, then enforces cache caps.
func (c *Cache) Store(mi metainfo.MetaInfo) error {
	if !c.Enabled() {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(mi.InfoBytes) == 0 {
		return fmt.Errorf("metainfo has no info dictionary")
	}
	if _, err := mi.UnmarshalInfo(); err != nil {
		return fmt.Errorf("invalid info dictionary: %w", err)
	}
	var data bytes.Buffer
	if err := mi.Write(&data); err != nil {
		return err
	}
	if int64(data.Len()) > intake.MaxTorrentBytes {
		return fmt.Errorf("metainfo exceeds 16 MiB")
	}
	if err := ensureCacheDir(c.dir); err != nil {
		return err
	}
	path := c.path(mi.HashInfoBytes())
	tmp, err := os.CreateTemp(c.dir, ".metainfo-*.torrent")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	ok := false
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(data.Bytes()); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	closed = true
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	ok = true
	return c.prune()
}

func (c *Cache) path(hash metainfo.Hash) string {
	return filepath.Join(c.dir, strings.ToLower(hash.HexString())+".torrent")
}

type invalidError struct{ error }

func isInvalid(err error) bool {
	_, ok := err.(invalidError)
	return ok
}

func readValidated(path string, want metainfo.Hash) (*metainfo.MetaInfo, error) {
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 || !linkInfo.Mode().IsRegular() {
		return nil, invalidError{fmt.Errorf("cache entry is not a regular file")}
	}
	if runtime.GOOS != "windows" && linkInfo.Mode().Perm()&0o077 != 0 {
		return nil, invalidError{fmt.Errorf("cache entry permissions are not private")}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > intake.MaxTorrentBytes {
		return nil, invalidError{fmt.Errorf("invalid cache entry size or type")}
	}
	mi, err := metainfo.Load(io.LimitReader(f, intake.MaxTorrentBytes+1))
	if err != nil {
		return nil, invalidError{err}
	}
	if len(mi.InfoBytes) == 0 || mi.HashInfoBytes() != want {
		return nil, invalidError{fmt.Errorf("cache infohash mismatch")}
	}
	if _, err := mi.UnmarshalInfo(); err != nil {
		return nil, invalidError{err}
	}
	return mi, nil
}

type cacheFile struct {
	path string
	size int64
	mod  time.Time
}

func (c *Cache) prune() error {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return err
	}
	var files []cacheFile
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".torrent") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		f := cacheFile{path: filepath.Join(c.dir, entry.Name()), size: info.Size(), mod: info.ModTime()}
		files = append(files, f)
		total += f.size
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for len(files) > c.maxEntries || total > c.maxBytes {
		victim := files[0]
		files = files[1:]
		if err := os.Remove(victim.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		total -= victim.size
	}
	return nil
}

// Inspect validates the cache without touching timestamps or deleting files.
func (c *Cache) Inspect() Report {
	if c != nil {
		c.mu.Lock()
		defer c.mu.Unlock()
	}
	rep := Report{Enabled: c.Enabled()}
	if !rep.Enabled {
		return rep
	}
	dirInfo, err := os.Lstat(c.dir)
	if os.IsNotExist(err) {
		return rep
	}
	if err != nil {
		rep.Err = err
		return rep
	}
	rep.Exists = true
	if dirInfo.Mode()&os.ModeSymlink != 0 || !dirInfo.IsDir() {
		rep.Err = fmt.Errorf("cache path is not a directory")
		return rep
	}
	if runtime.GOOS != "windows" && dirInfo.Mode().Perm()&0o077 != 0 {
		rep.Insecure++
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		rep.Err = err
		return rep
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".torrent") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			rep.Invalid++
			continue
		}
		rep.Entries++
		rep.Bytes += info.Size()
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			rep.Insecure++
		}
		hashHex := strings.TrimSuffix(strings.ToLower(entry.Name()), ".torrent")
		var hash metainfo.Hash
		if err := hash.FromHexString(hashHex); err != nil {
			rep.Invalid++
			continue
		}
		if _, err := readValidated(filepath.Join(c.dir, entry.Name()), hash); err != nil {
			rep.Invalid++
		}
	}
	return rep
}

func ensureCacheDir(dir string) error {
	err := validateCacheDir(dir)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		err = validateCacheDir(dir)
	}
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		return os.Chmod(dir, 0o700)
	}
	return nil
}

func validateCacheDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("metadata cache path is not a regular directory")
	}
	return nil
}
