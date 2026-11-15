package config

import (
	"cmp"
	"slices"
)

type Category struct{ ID, Name string }

func (o OrganizationConfig) Categories() []Category {
	options := []Category{{Name: "Uncategorized"}}
	for id, name := range categoryFolders {
		options = append(options, Category{ID: id, Name: name})
	}
	slices.SortFunc(options[1:], func(a, b Category) int { return cmp.Compare(a.Name, b.Name) })
	return options
}

func (c *Config) ResolveCategoryDestination(id string) (DownloadDestination, error) {
	manual := *c
	manual.Organization.Enabled = true
	return manual.ResolveDownloadDestination(id)
}
