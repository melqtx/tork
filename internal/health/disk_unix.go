//go:build unix

package health

import "syscall"

// freeBytes reports the space available to an unprivileged user on the
// filesystem holding dir.
func freeBytes(dir string) (int64, error) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(dir, &fs); err != nil {
		return 0, err
	}
	return int64(fs.Bavail) * int64(fs.Bsize), nil
}
