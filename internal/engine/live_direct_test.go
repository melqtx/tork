//go:build live

package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/isos"
)

// temporary live smoke test - deleted after verification. Starts a real
// download from Gentoo's CDN, waits for the first bytes, then pauses and
// shuts down; verifies UA/redirect/Range handling against real infrastructure.
func TestLiveDirectDownloadStarts(t *testing.T) {
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
	defer eng.Close()

	var gentoo isos.Distro
	for _, distro := range isos.Catalog() {
		if distro.ID == "gentoo" {
			gentoo = distro
			break
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	image, err := isos.ResolveWithClient(ctx, gentoo, cfg.ProxyHTTPClient())
	if err != nil {
		t.Fatal(err)
	}
	h, err := eng.AddDirect(image.DirectURL, "gentoo-test.iso", image.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(30 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("no bytes arrived: %+v", eng.Snapshots())
		case <-time.After(200 * time.Millisecond):
		}
		var done, length int64
		for _, s := range eng.Snapshots() {
			if s.Hash == h {
				done, length = s.BytesCompleted, s.Length
			}
		}
		if done > 256<<10 && length > 0 {
			t.Logf("streaming: %d bytes of %d - pausing", done, length)
			eng.Pause(h)
			return
		}
	}
}
