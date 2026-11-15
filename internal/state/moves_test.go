package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInterruptedMoveRecovery(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		old, next             bool
		oldSuffix, nextSuffix string
		bytes                 int64
		moved, relink         bool
	}{
		{name: "before rename", old: true, bytes: 7},
		{name: "after rename", next: true, bytes: 7, moved: true},
		{name: "partial download", next: true, nextSuffix: ".part", bytes: 7, moved: true},
		{name: "split sidecars", old: true, next: true, oldSuffix: ".part.meta", nextSuffix: ".part", bytes: 7, relink: true},
		{name: "collision", old: true, next: true, bytes: 7, relink: true},
		{name: "missing payload", bytes: 7, relink: true},
		{name: "metadata only", moved: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			old, next := filepath.Join(root, "old", "payload"), filepath.Join(root, "movies", "payload")
			for _, path := range []string{old, next} {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, file := range []struct {
				path    string
				present bool
			}{{old + tc.oldSuffix, tc.old}, {next + tc.nextSuffix, tc.next}} {
				if file.present {
					if err := os.WriteFile(file.path, []byte("payload"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			e := Entry{Magnet: "saved", Name: "payload", Category: "books", BytesCompleted: tc.bytes,
				DownloadDir: filepath.Dir(old), DataPath: old, PendingMove: &MoveTarget{
					DownloadDir: filepath.Dir(next), DataPath: next, Category: "movies", CategorySource: "manual",
				}}
			s := State{Entries: []Entry{e}}
			path := filepath.Join(root, "state.json")
			if err := s.Save(path); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			got := loaded.Entries[0]
			if got.PendingMove != nil || got.NeedsRelink != tc.relink || (got.DataPath == next) != tc.moved {
				t.Fatalf("wrong recovery: %+v", got)
			}
			if tc.moved && (got.Category != "movies" || got.CategorySource != "manual") {
				t.Fatalf("category lost during recovery: %+v", got)
			}
		})
	}
}
