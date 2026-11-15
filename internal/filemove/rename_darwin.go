package filemove

import "golang.org/x/sys/unix"

func Rename(old, next string) error {
	return unix.RenamexNp(old, next, unix.RENAME_EXCL)
}
