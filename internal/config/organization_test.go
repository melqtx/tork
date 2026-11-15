package config

import (
	"os"
	"path/filepath"
	"strings"
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
	if !Default(dir).Organization.Enabled {
		t.Fatal("new installations should organize automatically")
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
		{name: "tv", input: " TV Shows > HD ", category: "tv", folder: "tv"},
		{name: "anime", input: "Anime - English-translated", category: "anime", folder: "anime"},
		{name: "specific category", input: "TV/Anime", category: "anime", folder: "anime"},
		{name: "music", input: "Music", category: "music", folder: "music"},
		{name: "games", input: "Games", category: "games", folder: "games"},
		{name: "software", input: "Applications", category: "software", folder: "software"},
		{name: "books", input: "Other/E-books", category: "books", folder: "books"},
		{name: "linux", input: "Linux ISOs", category: "linux", folder: "linux"},
		{name: "missing category"},
		{name: "unknown", input: "Other"},
		{name: "quality is not category", input: "1080p x265"},
		{name: "no substring match", input: "XXX Movies"},
		{name: "adult parent", input: "XXX/Movies"},
		{name: "untrusted path", input: "../../arbitrary-folder"},
		{name: "disabled", input: "Movies", category: "movies", disabled: true},
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
	if err := (OrganizationConfig{Folders: map[string]string{"../typo": "Movies"}}).Validate(); err == nil {
		t.Fatal("accepted an invalid category name")
	}
}

func TestPrepareDownloadDestinationCreatesOnlySelectedFolder(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	cfg := &Config{DownloadDir: base, Organization: OrganizationConfig{
		Enabled: true, Folders: map[string]string{"movies": "Media/Films"},
	}}
	destination, err := cfg.ResolveDownloadDestination("Movies")
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.PrepareDownloadDestination(destination); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(destination.DownloadDir); err != nil || !info.IsDir() {
		t.Fatalf("selected folder not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "TV")); !os.IsNotExist(err) {
		t.Fatalf("unselected folder created: %v", err)
	}
}

func TestPrepareDownloadDestinationRefusesSymlinkEscape(t *testing.T) {
	t.Parallel()
	base, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(base, "Media")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	cfg := &Config{DownloadDir: base, Organization: OrganizationConfig{
		Enabled: true, Folders: map[string]string{"movies": "Media/Films"},
	}}
	destination, err := cfg.ResolveDownloadDestination("Movies")
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.PrepareDownloadDestination(destination); err == nil {
		t.Fatal("prepared a folder outside download_dir through a symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "Films")); !os.IsNotExist(err) {
		t.Fatalf("created folder outside root: %v", err)
	}
}

func TestWriteOrganizationEnabledPreservesConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := `# personal settings
max_connections: 7
organization:
  enabled: false # keep this
  folders:
    movies: Films
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadReadOnlyFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{true, false} {
		if err := cfg.WriteOrganizationEnabled(enabled); err != nil {
			t.Fatal(err)
		}
		reloaded, err := LoadReadOnlyFrom(dir)
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.Organization.Enabled != enabled || reloaded.Organization.Folders["movies"] != "Films" {
			t.Fatalf("reloaded organization = %+v", reloaded.Organization)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"# personal settings", "# keep this", "max_connections: 7"} {
			if !strings.Contains(string(data), want) {
				t.Errorf("config lost %q", want)
			}
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("config permissions changed: %v", err)
		}
	}
	if cfg.Organization.Enabled {
		t.Fatal("writer mutated in-memory config before the UI handled its result")
	}
}

func TestWriteOrganizationEnabledRefusesMalformedConfig(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, input string }{
		{name: "invalid yaml", input: "organization: ["},
		{name: "multiple documents", input: "max_connections: 7\n---\norganization: {enabled: false}\n"},
		{name: "non-mapping root", input: "[one, two]"},
		{name: "anchored flag", input: "organization: {enabled: &flag false}\n"},
		{name: "anchored organization", input: "organization: &org {enabled: false}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(path, []byte(tc.input), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := Default(dir).WriteOrganizationEnabled(true); err == nil {
				t.Fatal("accepted unsafe config edit")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != tc.input {
				t.Fatalf("failed edit changed the config: %v", err)
			}
		})
	}
}

func TestExistingConfigKeepsOrganizationOffUnlessEnabled(t *testing.T) {
	for _, setting := range []string{"", "organization: {enabled: false}\n"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(setting), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadReadOnlyFrom(dir)
		if err != nil || cfg.Organization.Enabled {
			t.Fatalf("existing settings changed: %+v, %v", cfg, err)
		}
	}
}
