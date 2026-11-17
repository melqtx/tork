package tui

import (
	"path/filepath"

	"github.com/charmbracelet/x/ansi"
)

func (a *App) downloadDisplayPath(path string, width int) string {
	if width <= 0 {
		return ""
	}
	if a.cfg != nil && a.cfg.DownloadDir != "" {
		root := filepath.Clean(a.cfg.DownloadDir)
		if rel, err := filepath.Rel(root, path); err == nil && (rel == "." || filepath.IsLocal(rel)) {
			path = filepath.Join(filepath.Base(root), rel)
		}
	}
	path = filepath.ToSlash(path)
	if length := ansi.StringWidth(path); length > width {
		return ansi.TruncateLeft(path, length-width+1, "…")
	}
	return path
}
