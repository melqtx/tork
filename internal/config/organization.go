package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
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
	seen := map[string]bool{}
	for category, folder := range o.Folders {
		if err := validateCategoryName(category); err != nil {
			return err
		}
		key := strings.ToLower(category)
		_, builtin := categoryFolders[key]
		if seen[key] || (builtin && key != category) {
			return fmt.Errorf("duplicate category %q", category)
		}
		seen[key] = true
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
	return c.ResolveCategoryDestination(downloadCategory(providerCategory))
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

func (c *Config) WriteOrganizationEnabled(enabled bool) error {
	doc, current, mode, err := c.editableDocument()
	if err != nil {
		return err
	}
	if enabled {
		if err := current.Organization.Validate(); err != nil {
			return err
		}
	}
	organization, err := editableMapping(doc.Content[0], "organization")
	if err != nil {
		return err
	}
	value := strconv.FormatBool(enabled)
	if i := mappingKey(organization, "enabled"); i >= 0 {
		node := organization.Content[i+1]
		if node.Kind != yaml.ScalarNode || node.Anchor != "" {
			return errors.New("organization enabled flag must be a scalar without a YAML anchor")
		}
		node.Tag, node.Value, node.Style = "!!bool", value, 0
	} else {
		if mappingKey(organization, "<<") >= 0 {
			return errors.New("expand YAML merge keys before changing organization")
		}
		organization.Content = append(organization.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "enabled"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: value},
		)
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	return writeYAMLAtomic(c.ConfigPath(), doc, mode)
}
