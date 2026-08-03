package control

import (
	"fmt"
	"os"
	"path/filepath"
)

// RuntimeDir returns a 0700 directory owned by the current euid.
func RuntimeDir(name string) (string, error) {
	if os.Geteuid() == 0 {
		return ensureDir(filepath.Join("/var/run", name))
	}
	return ensureDir(filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d", name, os.Geteuid())))
}
