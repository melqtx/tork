//go:build !unix

package health

// Non-Unix builds retain in-process serialization. tork's supported package
// targets are Unix, where lock_unix.go also protects separate processes.
func withFileLock(_ string, fn func() error) error { return fn() }
