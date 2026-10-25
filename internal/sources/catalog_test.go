package sources

import (
	"github.com/melqtx/tork/internal/config"
	"strings"
	"testing"
)

func TestBroadSourcesAreAvailableWithoutConfigurationAndRemainOptIn(t *testing.T) {
	cfg := config.Default(t.TempDir())
	available := Available(cfg)
	if len(available) < 3 || available[0].ID != "tpb" || available[1].ID != "therarbg" || available[2].ID != "limetorrents" {
		t.Fatalf("broad source catalog is incomplete: %+v", available)
	}
	for _, id := range []string{"tpb", "therarbg", "limetorrents"} {
		p, ok := cfg.Providers[id]
		if !ok || p.Enabled {
			t.Fatalf("%s is missing or silently enabled", id)
		}
		built, err := Build(id, p, nil)
		if err != nil || built.Name() != id {
			t.Fatalf("%s cannot be built without configuration: %v", id, err)
		}
		if !strings.Contains(Description(Source{ID: id, Config: p}), "software") {
			t.Fatalf("%s does not explain its broad coverage", id)
		}
	}
	for _, s := range available {
		if s.ID == "tpb_movies" || s.ID == "tpb_tv" {
			t.Fatal("add catalog still requires choosing between category-limited Pirate Bay sources")
		}
	}
}
