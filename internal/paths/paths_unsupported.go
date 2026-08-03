//go:build !linux && !darwin

package paths

import (
	"fmt"
	"net"
	"runtime"
)

func RuntimeDir(string) (string, error) {
	return "", fmt.Errorf("%s daemon unsupported", runtime.GOOS)
}

func socketAddr(string, string) (net.Addr, error) {
	return nil, fmt.Errorf("%s daemon unsupported", runtime.GOOS)
}
