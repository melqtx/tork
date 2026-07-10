//go:build !unix

package health

import "errors"

// freeBytes has no portable implementation off unix; the caller degrades to
// reporting the directory as writable without a free-space figure.
func freeBytes(string) (int64, error) {
	return 0, errors.New("free space unavailable on this platform")
}
