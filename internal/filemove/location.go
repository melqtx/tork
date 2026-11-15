package filemove

import (
	"os"
	"path/filepath"
)

func SameLocation(old, next string) bool {
	if old == "" || next == "" || filepath.Base(old) != filepath.Base(next) {
		return false
	}
	left, leftErr := os.Stat(filepath.Dir(old))
	right, rightErr := os.Stat(filepath.Dir(next))
	return leftErr == nil && rightErr == nil && os.SameFile(left, right)
}
