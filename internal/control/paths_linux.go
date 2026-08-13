package control

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// runtimeDir returns a 0700 directory owned by the current euid.
func runtimeDir(name string) (string, error) {
	// systemd `RuntimeDirectory=` (colon-separated)
	if v := os.Getenv("RUNTIME_DIRECTORY"); v != "" {
		return ensureDir(strings.Split(v, ":")[0])
	}
	if os.Geteuid() == 0 {
		return ensureDir(filepath.Join("/run", name))
	}
	if v := os.Getenv("XDG_RUNTIME_DIR"); v != "" {
		return ensureDir(filepath.Join(v, name))
	}
	// Shared /tmp: the uid suffix is what makes the name unique per user,
	// and ensureDir is what makes squatting it fail.
	return ensureDir(filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d", name, os.Geteuid())))
}
