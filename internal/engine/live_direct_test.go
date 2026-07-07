//go:build live

package engine

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/melqtx/tork/internal/config"
)

// temporary live smoke test - deleted after verification. Starts a real
// download from Gentoo's CDN, waits for the first bytes, then pauses and
// shuts down; verifies UA/redirect/Range handling against real infrastructure.
func TestLiveDirectDownloadStarts(t *testing.T) {
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

	url := "https://distfiles.gentoo.org/releases/amd64/autobuilds/current-install-amd64-minimal/install-amd64-minimal-20260705T170105Z.iso"
	h, err := eng.AddDirect(url, "gentoo-test.iso", "")
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
