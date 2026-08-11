//go:build !unix

package control

import (
	"context"
	"fmt"
	"net"
	"runtime"
)

// Stubs so the package still compiles where there is no unix socket to bind.
// Nothing here is reachable: runtimeDir already fails on these platforms, and
// resolvePaths gives up before any of it runs. They exist to keep `tork` on
// Windows a binary without a daemon, rather than no binary at all.
//
// This is also the seam a named-pipe backend would replace, which is the whole
// reason Listen/Dial are the exported surface and the socket path is not.

func socketPath(string, string) (string, error) {
	return "", fmt.Errorf("%s daemon unsupported", runtime.GOOS)
}

func listen(paths) (*Listener, error) {
	return nil, fmt.Errorf("%s daemon unsupported", runtime.GOOS)
}

func dial(context.Context, paths) (net.Conn, error) {
	return nil, fmt.Errorf("%s daemon unsupported", runtime.GOOS)
}
