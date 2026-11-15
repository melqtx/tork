package state

import (
	"fmt"
	"os"
	"path/filepath"
)

type MoveTarget struct {
	DownloadDir    string `json:"download_dir"`
	DataPath       string `json:"data_path"`
	Category       string `json:"category"`
	CategorySource string `json:"category_source,omitempty"`
}

func (m MoveTarget) Apply(e *Entry) {
	e.DownloadDir, e.DataPath = m.DownloadDir, m.DataPath
	e.Category, e.CategorySource = m.Category, m.CategorySource
	e.NeedsRelink, e.PendingMove = false, nil
}

func recoverMove(e *Entry) {
	m := e.PendingMove
	if m == nil {
		return
	}
	valid := filepath.IsAbs(m.DownloadDir)
	if m.DataPath != "" {
		valid = valid && filepath.Dir(m.DataPath) == m.DownloadDir && filepath.Base(m.DataPath) == filepath.Base(e.DataPath)
	} else {
		valid = valid && e.BytesCompleted == 0 && !e.Done
	}
	old, oldErr := movePayloadPresent(e.DataPath)
	next, nextErr := movePayloadPresent(m.DataPath)
	switch {
	case !valid || oldErr != nil || nextErr != nil:
	case e.DataPath == m.DataPath && e.DownloadDir == m.DownloadDir:
		m.Apply(e)
		return
	case old && !next:
		e.PendingMove = nil
		return
	case !old && next:
		m.Apply(e)
		return
	case !old && !next && e.BytesCompleted == 0 && !e.Done:
		m.Apply(e)
		return
	default:
	}
	e.PendingMove = nil
	e.Paused, e.NeedsRelink = true, true
	fmt.Fprintf(os.Stderr, "tork: interrupted move needs relinking: %s\n", e.Name)
}

func movePayloadPresent(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	present := false
	for _, suffix := range []string{"", ".part", ".part.meta"} {
		info, err := os.Lstat(path + suffix)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return false, fmt.Errorf("move path is a symlink")
		}
		present = true
	}
	return present, nil
}
