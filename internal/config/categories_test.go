package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCustomCategoryPersistsAndResolves(t *testing.T) {
	t.Parallel()
	cfg := Default(t.TempDir())
	if _, err := cfg.WriteCategory("Courses", "Learning"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadReadOnlyFrom(cfg.Dir())
	if err != nil {
		t.Fatal(err)
	}
	destination, err := reloaded.ResolveCategoryDestination("Courses")
	if err != nil || destination.DownloadDir != filepath.Join(cfg.DownloadDir, "Learning") {
		t.Fatalf("custom destination = %+v, error = %v", destination, err)
	}
	if _, err := reloaded.WriteCategory("courses", "Elsewhere"); err == nil {
		t.Fatal("accepted a duplicate category")
	}
	before, err := os.ReadFile(cfg.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.WriteCategory("Outside", "../escape"); err == nil {
		t.Fatal("accepted a folder outside download_dir")
	}
	after, err := os.ReadFile(cfg.ConfigPath())
	if err != nil || string(before) != string(after) {
		t.Fatal("failed save changed config")
	}
}

func TestManualCategoryWorksWithAutomaticOrganizationOff(t *testing.T) {
	t.Parallel()
	cfg := Default(t.TempDir())
	auto, err := cfg.ResolveDownloadDestination("Movies")
	if err != nil {
		t.Fatal(err)
	}
	manual, err := cfg.ResolveCategoryDestination("movies")
	if err != nil || manual.DownloadDir == auto.DownloadDir || manual.Category != "movies" {
		t.Fatalf("manual=%+v automatic=%+v error=%v", manual, auto, err)
	}
	if _, err := cfg.ResolveCategoryDestination("missing"); err == nil {
		t.Fatal("missing category silently fell back to the root")
	}
}
