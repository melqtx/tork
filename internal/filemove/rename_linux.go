package filemove

import "golang.org/x/sys/unix"

func Rename(old, next string) error {
	return unix.Renameat2(unix.AT_FDCWD, old, unix.AT_FDCWD, next, unix.RENAME_NOREPLACE)
}
