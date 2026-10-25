//go:build windows

package session

import (
	"errors"
	"net"
)

// Windows keeps the existing foreground mode until an authenticated named-pipe
// transport is available. Never expose the transfer controls on a TCP port.
const Supported = false

var ErrRunning = errors.New("background sessions are unavailable on Windows")

func Listen(string) (net.Listener, func(), error) { return nil, nil, ErrRunning }
func dial(string) (net.Conn, error)               { return nil, ErrRunning }
func Start(string, string, []string) error        { return ErrRunning }
