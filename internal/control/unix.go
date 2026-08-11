//go:build unix

package control

import (
	"context"
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

func socketPath(name, dir string) (string, error) {
	p := filepath.Join(dir, name+".sock")
	if len(p) >= sunPathLen {
		return "", fmt.Errorf("socket path %q is %d bytes, limit %d", p, len(p), sunPathLen-1)
	}
	return p, nil
}

// acquireLock takes the daemon's exclusive claim on the runtime directory.
//
// The claim is the flock, not the file. A flock lives on the open file
// description, so the kernel drops it when the process dies by any means —
// SIGKILL, panic, OOM — which makes "is the lock held" and "is a daemon alive"
// the same question, answered atomically. A pid file cannot do that: it
// survives its author, so reading it back means guessing whether the pid is
// still the process that wrote it, and pid reuse makes that guess wrong.
func acquireLock(path string) (*os.File, error) {
	// No O_TRUNC: at this point the file may belong to a live daemon, and
	// truncating another process's lock file before losing the race for it
	// would destroy the pid it left for humans to read.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrDaemonRunning
		}
		return nil, fmt.Errorf("flock %s: %w", path, err)
	}
	// The claim is ours, so leave a pid behind for humans. Nothing reads this
	// back — liveness is the lock, and the contents are a comment.
	if err := f.Truncate(0); err == nil {
		fmt.Fprintf(f, "%d\n", os.Getpid())
	}
	// The lock file is never unlinked, not even on a clean exit: flock binds to
	// the inode, so removing the name would let a second process create a fresh
	// file at the same path and lock that one instead. Two winners, no error.
	return f, nil
}

func listen(p paths) (*Listener, error) {
	lock, err := acquireLock(p.lock)
	if err != nil {
		return nil, err
	}
	// Holding the lock is what makes this unlink safe: no live daemon can be
	// bound here, so anything at this path is a corpse from a crash. Unix
	// sockets are not cleaned up by the kernel, so somebody has to do it, and
	// only the lock holder may.
	if err := os.Remove(p.socket); err != nil && !errors.Is(err, fs.ErrNotExist) {
		lock.Close()
		return nil, fmt.Errorf("clear stale socket %s: %w", p.socket, err)
	}
	l, err := net.Listen("unix", p.socket)
	if err != nil {
		lock.Close()
		return nil, err
	}
	// Belt and braces over the 0700 directory: a bind honours the umask, so
	// the socket can land group- or world-writable. Nothing can traverse into
	// the directory to reach it, but the mode should not depend on that.
	if err := os.Chmod(p.socket, 0o600); err != nil {
		l.Close()
		lock.Close()
		return nil, err
	}
	return &Listener{Listener: l, lock: lock}, nil
}

func dial(ctx context.Context, p paths) (net.Conn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", p.socket)
	if err != nil {
		// ENOENT: never started. ECONNREFUSED: crashed, and the socket file
		// outlived it. Both mean the same thing to a caller deciding whether
		// to fall back to an in-process engine.
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
			return nil, ErrNoDaemon
		}
		return nil, err
	}
	return c, nil
}
