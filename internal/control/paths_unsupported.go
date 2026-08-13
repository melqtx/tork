//go:build !linux && !darwin

package control

import (
	"fmt"
	"runtime"
)

func runtimeDir(string) (string, error) {
	return "", fmt.Errorf("%s daemon unsupported", runtime.GOOS)
}
