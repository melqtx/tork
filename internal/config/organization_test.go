package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOrganizationDefaultsAndOverrides(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	settings := "organization:\n  enabled: true\n  folders:\n    movies: Films\n"
	if err := os.WriteFile(path, []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadReadOnlyFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Organization.Enabled || cfg.Organization.Folders["movies"] != "Films" {
		t.Fatalf("organization = %+v", cfg.Organization)
	}
	if Default(dir).Organization.Enabled {
		t.Fatal("organization must be opt-in")
	}
	if err := os.WriteFile(path, []byte("organization: {enabled: false}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadReadOnlyFrom(dir)
	if err != nil || cfg.Organization.Enabled {
		t.Fatalf("disabled organization = %+v, err = %v", cfg, err)
	}
}

func TestResolveDownloadDestination(t *testing.T) {
	t.Parallel()
	base := filepath.Join(t.TempDir(), "not-created")
	cases := []struct {
		name, input, category, folder string
		disabled                      bool
	}{
		{name: "movie", input: "Movies/HD", category: "movies", folder: "Films"},
		{name: "singular", input: "movie", category: "movies", folder: "Films"},
		{name: "tv", input: " TV Shows > HD ", category: "tv", folder: "TV"},
		{name: "anime", input: "Anime - English-translated", category: "anime", folder: "Anime"},
		{name: "specific category", input: "TV/Anime", category: "anime", folder: "Anime"},
		{name: "music", input: "Music", category: "music", folder: "Music"},
		{name: "games", input: "Games", category: "games", folder: "Games"},
		{name: "software", input: "Applications", category: "software", folder: "Software"},
		{name: "books", input: "Other/E-books", category: "books", folder: "Books"},
		{name: "linux", input: "Linux ISOs", category: "linux", folder: "Linux"},
		{name: "missing category"},
		{name: "unknown", input: "Other"},
		{name: "quality is not category", input: "1080p x265"},
		{name: "no substring match", input: "XXX Movies"},
		{name: "adult parent", input: "XXX/Movies"},
		{name: "untrusted path", input: "../../arbitrary-folder"},
		{name: "disabled", input: "Movies", disabled: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := &Config{DownloadDir: base, Organization: OrganizationConfig{
				Enabled: !tc.disabled, Folders: map[string]string{"movies": "Films"},
			}}
			got, err := cfg.ResolveDownloadDestination(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			wantDir := base
			if tc.folder != "" {
				wantDir = filepath.Join(base, tc.folder)
			}
			if got.Category != tc.category || got.DownloadDir != wantDir {
				t.Fatalf("destination = %+v, want category %q directory %q", got, tc.category, wantDir)
			}
			if _, err := os.Stat(base); !os.IsNotExist(err) {
				t.Fatalf("resolver touched the filesystem: %v", err)
			}
		})
	}
}

func TestOrganizationRejectsUnsafeFolders(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, folder string }{
		{name: "empty", folder: ""},
		{name: "root", folder: "."},
		{name: "parent", folder: "../Movies"},
		{name: "nested parent", folder: "Media/../Movies"},
		{name: "absolute", folder: "/Movies"},
		{name: "windows drive", folder: "C:\\Movies"},
		{name: "windows traversal", folder: "..\\Movies"},
		{name: "control", folder: "Movies\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			org := OrganizationConfig{Enabled: true, Folders: map[string]string{"movies": tc.folder}}
			if err := org.Validate(); err == nil {
				t.Fatalf("accepted folder %q", tc.folder)
			}
		})
	}
	if err := (OrganizationConfig{Folders: map[string]string{"typo": "Movies"}}).Validate(); err == nil {
		t.Fatal("accepted an unknown category key")
	}
}
