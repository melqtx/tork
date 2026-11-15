package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
)

func (e *Engine) SetPreviewDownloadDir(h metainfo.Hash, dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("choose a download folder")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve preview folder: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("open preview folder: %w", err)
	}
	if !info.IsDir() {
		return errors.New("preview destination must be a folder")
	}
	e.mu.Lock()
	it, ok := e.items[h]
	if !ok || !it.preview || it.t == nil || e.closing {
		e.mu.Unlock()
		return errors.New("only a new preview can change its download folder")
	}
	if it.downloadDir == dir {
		e.mu.Unlock()
		return nil
	}
	oldDir, magnet, seed, source := it.downloadDir, it.magnet, it.seeding, it.metadataSource
	var mi *metainfo.MetaInfo
	if it.t.Info() != nil {
		metadata := it.t.Metainfo()
		mi = &metadata
	}
	it.t.Drop()
	delete(e.items, h)
	e.mu.Unlock()

	reopen := func(target string) error {
		opts := AddOptions{DownloadDir: target, Seed: &seed}
		var err error
		if mi != nil {
			_, _, _, _, err = e.addMetaInfoForPreview(mi, opts, source)
		} else {
			_, _, err = e.AddForPreviewWithOptions(magnet, opts)
		}
		if err != nil {
			return err
		}
		e.mu.Lock()
		if current := e.items[h]; current != nil {
			current.magnet = magnet
		}
		e.mu.Unlock()
		return nil
	}
	if err := reopen(dir); err != nil {
		return errors.Join(fmt.Errorf("change preview folder: %w", err), reopen(oldDir))
	}
	return nil
}
