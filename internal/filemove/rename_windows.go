package filemove

import "golang.org/x/sys/windows"

func Rename(old, next string) error {
	from, err := windows.UTF16PtrFromString(old)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(next)
	if err != nil {
		return err
	}
	return windows.MoveFile(from, to)
}
