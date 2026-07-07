//go:build live

package isos

import (
	"context"
	"testing"
	"time"
)

// temporary live smoke test - deleted after verification
func TestLiveResolveDirectEntries(t *testing.T) {
	for _, d := range Catalog() {
		if d.ID != "gentoo" && d.ID != "opensuse" {
			continue
		}
		d := d
		t.Run(d.ID, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			img, err := Resolve(ctx, d)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if img.DirectURL == "" || img.SHA256 == "" {
				t.Fatalf("want DirectURL+SHA256, got %+v", img)
			}
			t.Logf("%s -> %q sha256=%.16s… url=%s", d.ID, img.Title, img.SHA256, img.DirectURL)
		})
	}
}
