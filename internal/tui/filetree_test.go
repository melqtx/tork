package tui

import (
	"strings"
	"testing"

	"github.com/melqtx/tork/internal/engine"
)

func TestBuildFileTreeAndCollapse(t *testing.T) {
	files := []engine.FileInfo{
		{Index: 0, Path: "show/season/ep1.mkv", Length: 100},
		{Index: 1, Path: "show/season/ep2.mkv", Length: 200},
		{Index: 2, Path: "extras/readme.txt", Length: 10},
		{Index: 3, Path: "foo", Length: 1},
		{Index: 4, Path: "foo/bar.txt", Length: 2},
	}
	root := buildFileTree(files)
	if len(root.children) != 4 {
		t.Fatalf("root children = %d, want 4", len(root.children))
	}
	if root.children[0].fileIdx >= 0 {
		t.Fatalf("directories should sort before files: %#v", root.children)
	}

	var rows []*fileNode
	root.flatten(&rows)
	if len(rows) <= len(root.children) {
		t.Fatalf("expanded rows = %d, want nested children included", len(rows))
	}
	root.children[0].collapsed = true
	rows = nil
	root.flatten(&rows)
	for _, row := range rows {
		if strings.HasPrefix(row.name, "readme") {
			t.Fatalf("collapsed directory leaked child row: %#v", rows)
		}
	}
}

func TestNodeCheckAndDirToggle(t *testing.T) {
	files := []engine.FileInfo{
		{Index: 0, Path: "dir/a.mkv", Length: 100},
		{Index: 1, Path: "dir/b.mkv", Length: 200},
	}
	root := buildFileTree(files)
	dir := root.children[0]
	p := previewModel{excluded: map[int]bool{}}

	if got := nodeCheck(dir, p.excluded); got != checkAll {
		t.Fatalf("initial check = %v, want all", got)
	}
	p.excluded[0] = true
	if got := nodeCheck(dir, p.excluded); got != checkMixed {
		t.Fatalf("one excluded check = %v, want mixed", got)
	}
	p.toggleNode(dir)
	if got := nodeCheck(dir, p.excluded); got != checkNone {
		t.Fatalf("mixed dir toggle check = %v, want none", got)
	}
	p.toggleNode(dir)
	if got := nodeCheck(dir, p.excluded); got != checkAll {
		t.Fatalf("none dir toggle check = %v, want all", got)
	}
}

func TestRiskFor(t *testing.T) {
	cases := map[string]bool{
		"movie.mkv":        false,
		"movie.mkv.exe":    true,
		"installer.msi":    true,
		"subs.srt":         false,
		"scripts/start.sh": true,
	}
	for name, wantRisk := range cases {
		gotRisk := riskFor(name) != ""
		if gotRisk != wantRisk {
			t.Errorf("riskFor(%q) risk=%v, want %v", name, gotRisk, wantRisk)
		}
	}
	if got := riskFor("movie.mkv.exe"); !strings.Contains(got, "double-extension") {
		t.Errorf("movie.mkv.exe reason = %q", got)
	}
}
