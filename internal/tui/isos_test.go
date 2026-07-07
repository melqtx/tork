package tui

import (
	"slices"
	"testing"

	"github.com/melqtx/tork/internal/isos"
)

func TestShelfRowsUseOnlyDesktopAndServers(t *testing.T) {
	rows := buildShelfRows(isos.Catalog())
	var headers []string
	for _, row := range rows {
		if row.distro < 0 {
			headers = append(headers, row.header)
		}
	}
	if !slices.Equal(headers, []string{"desktop", "servers"}) {
		t.Fatalf("headers = %v, want [desktop servers]", headers)
	}
}
