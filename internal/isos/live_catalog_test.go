//go:build live

package isos

import (
	"context"
	"testing"
	"time"
)

// Live smoke test for the expanded catalog. It is build-tagged so the default
// unit suite never depends on external distro infrastructure.
// Run with: go test -tags live -run TestLiveNewEntries ./internal/isos/
func TestLiveNewEntries(t *testing.T) {
	newIDs := map[string]bool{
		"popos": true, "fedora-xfce": true, "fedora-cosmic": true,
		"kubuntu": true, "xubuntu": true, "lubuntu": true, "ubuntu-mate": true,
		"opensuse-leap": true, "mxlinux": true, "void": true, "alpine": true,
		"bazzite": true, "fedora-sway": true, "fedora-i3": true,
	}
	wantSHA := map[string]bool{
		"popos": true, "opensuse-leap": true, "mxlinux": true,
		"void": true, "alpine": true, "bazzite": true,
	}
	for _, d := range Catalog() {
		if !newIDs[d.ID] {
			continue
		}
		d := d
		t.Run(d.ID, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			img, err := Resolve(ctx, d)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if img.URL == "" && img.Magnet == "" && img.DirectURL == "" {
				t.Fatalf("no resolvable target: %+v", img)
			}
			if wantSHA[d.ID] && img.SHA256 == "" {
				t.Fatalf("missing sha256 for direct image: %+v", img)
			}
			t.Logf("%s -> %q url=%s magnet=%t direct=%s sha=%.12s",
				d.ID, img.Title, img.URL, img.Magnet != "", img.DirectURL, img.SHA256)
		})
	}
}
