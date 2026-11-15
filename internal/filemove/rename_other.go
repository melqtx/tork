//go:build !linux && !darwin && !windows

package filemove

import "errors"

func Rename(_, _ string) error {
	return errors.New("safe file moves are unsupported on this platform")
}
