package config

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

type Category struct{ ID, Name string }

func (o OrganizationConfig) Categories() []Category {
	options := []Category{{Name: "Uncategorized"}}
	for id, name := range categoryNames {
		options = append(options, Category{ID: id, Name: name})
	}
	for id := range o.Folders {
		if _, builtin := categoryNames[id]; !builtin {
			options = append(options, Category{ID: id, Name: id})
		}
	}
	slices.SortFunc(options[1:], func(a, b Category) int { return cmp.Compare(a.Name, b.Name) })
	return options
}

func (c *Config) ResolveCategoryDestination(id string) (DownloadDestination, error) {
	base, err := filepath.Abs(c.DownloadDir)
	if err != nil {
		return DownloadDestination{}, fmt.Errorf("resolve download directory: %w", err)
	}
	if id == "" {
		return DownloadDestination{DownloadDir: base}, nil
	}
	_, known := categoryNames[id]
	folder := id
	if override, ok := c.Organization.Folders[id]; ok {
		folder, known = override, true
	}
	if !known {
		return DownloadDestination{}, errors.New("category no longer exists")
	}
	if err := validateCategoryFolder(folder); err != nil {
		return DownloadDestination{}, err
	}
	return DownloadDestination{Category: id, DownloadDir: filepath.Join(base, folder)}, nil
}

func validateCategoryName(name string) error {
	invalid := name == "" || name != strings.TrimSpace(name) || len(name) > 64
	if invalid || strings.EqualFold(name, "Uncategorized") || strings.ContainsAny(name, "/\\:") {
		return errors.New("choose a category name of 1–64 characters without path separators")
	}
	if strings.ContainsFunc(name, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
		return errors.New("category name contains unsupported characters")
	}
	return nil
}

func (c *Config) WriteCategory(name, folder string) (Category, error) {
	name, folder = strings.TrimSpace(name), strings.TrimSpace(folder)
	if err := validateCategoryName(name); err != nil {
		return Category{}, err
	}
	if folder == "" {
		folder = name
	}
	if err := validateCategoryFolder(folder); err != nil {
		return Category{}, err
	}
	doc, current, mode, err := c.editableDocument()
	if err != nil {
		return Category{}, err
	}
	for _, option := range current.Organization.Categories() {
		if strings.EqualFold(option.Name, name) || strings.EqualFold(option.ID, name) {
			return Category{}, errors.New("a category with this name already exists")
		}
	}
	organization, err := editableMapping(doc.Content[0], "organization")
	if err != nil {
		return Category{}, err
	}
	folders, err := editableMapping(organization, "folders")
	if err != nil {
		return Category{}, err
	}
	folders.Content = append(folders.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: folder},
	)
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return Category{}, err
	}
	if err := writeYAMLAtomic(c.ConfigPath(), doc, mode); err != nil {
		return Category{}, err
	}
	return Category{ID: name, Name: name}, nil
}
