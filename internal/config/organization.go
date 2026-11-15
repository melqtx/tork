package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

type OrganizationConfig struct {
	Enabled bool              `yaml:"enabled"`
	Folders map[string]string `yaml:"folders,omitempty"`
}

type DownloadDestination struct {
	Category    string
	DownloadDir string
}

var categoryFolders = map[string]string{
	"movies": "Movies", "tv": "TV", "anime": "Anime", "music": "Music",
	"games": "Games", "software": "Software", "books": "Books", "linux": "Linux",
}

func (o OrganizationConfig) Validate() error {
	for category, folder := range o.Folders {
		if _, ok := categoryFolders[category]; !ok {
			return fmt.Errorf("organization.folders: unknown category %q", category)
		}
		if err := validateCategoryFolder(folder); err != nil {
			return fmt.Errorf("organization.folders.%s: %w", category, err)
		}
	}
	return nil
}

func validateCategoryFolder(folder string) error {
	// Also reject Windows paths when running on Unix.
	invalid := strings.TrimSpace(folder) == "" || strings.ContainsAny(folder, "\\:")
	if invalid || !filepath.IsLocal(folder) || filepath.Clean(folder) == "." {
		return errors.New("folder must be a relative path inside download_dir")
	}
	for _, part := range strings.Split(filepath.ToSlash(folder), "/") {
		if part == ".." {
			return errors.New("folder must not contain parent-directory components")
		}
	}
	if strings.ContainsFunc(folder, unicode.IsControl) {
		return errors.New("folder must not contain control characters")
	}
	return nil
}

func (c *Config) ResolveDownloadDestination(providerCategory string) (DownloadDestination, error) {
	base, err := filepath.Abs(c.DownloadDir)
	if err != nil {
		return DownloadDestination{}, fmt.Errorf("resolve download directory: %w", err)
	}
	destination := DownloadDestination{DownloadDir: base}
	if !c.Organization.Enabled {
		return destination, nil
	}
	category := downloadCategory(providerCategory)
	folder, known := categoryFolders[category]
	if !known {
		return destination, nil
	}
	if override, ok := c.Organization.Folders[category]; ok {
		folder = override
	}
	if err := validateCategoryFolder(folder); err != nil {
		return DownloadDestination{}, fmt.Errorf("organization.folders.%s: %w", category, err)
	}
	return DownloadDestination{Category: category, DownloadDir: filepath.Join(base, folder)}, nil
}

func (c *Config) PrepareDownloadDestination(destination DownloadDestination) error {
	if destination.Category == "" {
		return nil
	}
	base, err := filepath.Abs(c.DownloadDir)
	if err != nil {
		return fmt.Errorf("resolve download directory: %w", err)
	}
	rel, err := filepath.Rel(base, destination.DownloadDir)
	if err != nil {
		return fmt.Errorf("resolve category folder: %w", err)
	}
	if err := validateCategoryFolder(rel); err != nil {
		return fmt.Errorf("resolve category folder: %w", err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return fmt.Errorf("open download directory: %w", err)
	}
	mkdirErr := root.MkdirAll(rel, 0o755)
	closeErr := root.Close()
	if err := errors.Join(mkdirErr, closeErr); err != nil {
		return fmt.Errorf("prepare category folder: %w", err)
	}
	return nil
}

func downloadCategory(raw string) string {
	raw = strings.ToLower(strings.Join(strings.Fields(raw), " "))
	raw = strings.ReplaceAll(raw, " - ", "/")
	segments := strings.FieldsFunc(raw, func(r rune) bool {
		return strings.ContainsRune(">/,|", r)
	})
	for _, segment := range segments {
		switch strings.TrimSpace(segment) {
		case "xxx", "nsfw", "adult", "porn":
			return ""
		}
	}
	// TV/Anime goes in Anime; Movies/HD goes in Movies.
	for i := len(segments) - 1; i >= 0; i-- {
		switch strings.TrimSpace(segments[i]) {
		case "movie", "movies", "film", "films":
			return "movies"
		case "tv", "television", "tv shows", "tv show", "tv series":
			return "tv"
		case "anime":
			return "anime"
		case "music":
			return "music"
		case "game", "games":
			return "games"
		case "software", "app", "apps", "application", "applications":
			return "software"
		case "book", "books", "ebook", "ebooks", "e-book", "e-books", "audiobook", "audiobooks":
			return "books"
		case "linux", "linux iso", "linux isos":
			return "linux"
		}
	}
	return ""
}
