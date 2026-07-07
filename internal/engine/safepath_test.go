package engine

import (
	"path/filepath"
	"testing"
)

func TestSafeDataPath(t *testing.T) {
	dir := filepath.FromSlash("/home/u/Downloads/tork")
	cases := []struct {
		name  string
		want  string // "" means rejected
		allow bool
	}{
		{"ubuntu-24.04.iso", filepath.Join(dir, "ubuntu-24.04.iso"), true},
		{"pack/big.dat", filepath.Join(dir, "pack/big.dat"), true},
		{"../../../etc/passwd", "", false},
		{"..", "", false},
		{".", "", false},                                        // must never target the whole download dir
		{"a/../../b", "", false},                                // cleans to an escape
		{"/etc/passwd", filepath.Join(dir, "etc/passwd"), true}, // stays inside after Join
	}
	for _, c := range cases {
		got, ok := safeDataPath(dir, c.name)
		if ok != c.allow {
			t.Errorf("safeDataPath(%q) allowed=%v, want %v", c.name, ok, c.allow)
			continue
		}
		if ok && got != c.want {
			t.Errorf("safeDataPath(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSafePathWithin(t *testing.T) {
	dir := filepath.FromSlash("/home/u/Downloads/tork")
	cases := []struct {
		path string
		want bool
	}{
		{filepath.Join(dir, "ubuntu.iso"), true},
		{filepath.Join(dir, "pack"), true},
		{dir, false},
		{filepath.Dir(dir), false},
		{filepath.FromSlash("/etc/passwd"), false},
	}
	for _, c := range cases {
		if got := safePathWithin(dir, c.path); got != c.want {
			t.Errorf("safePathWithin(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
