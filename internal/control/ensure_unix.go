//go:build unix

package control

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

// sizeof(sun_path)
const sunPathLen = len(syscall.RawSockaddrUnix{}.Path)

func ensureDir(path string) (string, error) {
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", err
	}

	// O_NOFOLLOW|O_DIRECTORY: refuses to traverse a planted symlink, and
	// gives us an fd so the fstat describes the object we actually opened
	// rather than whatever the name points at a microsecond later.
	fd, err := syscall.Open(path,
		syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err) // ELOOP => symlink
	}
	defer syscall.Close(fd)

	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return "", fmt.Errorf("fstat %s: %w", path, err)
	}
	if uint64(st.Uid) != uint64(os.Geteuid()) {
		return "", fmt.Errorf("%s: owned by uid %d, want %d", path, st.Uid, os.Geteuid())
	}
	// Refuse, don't chmod: a runtime dir with wrong perms means something is
	// off, and silently "fixing" it hides that.
	if perm := fs.FileMode(st.Mode).Perm(); perm&0o077 != 0 {
		return "", fmt.Errorf("%s: mode %#o, want 0700", path, perm)
	}
	return path, nil
}

func socketAddr(name, dir string) (*net.UnixAddr, error) {
	p := filepath.Join(dir, name+".sock")
	if len(p) >= sunPathLen {
		return nil, fmt.Errorf("socket path %q is %d bytes, limit %d", p, len(p), sunPathLen-1)
	}
	return net.ResolveUnixAddr("unix", p)
}
