// Package paths resolves the control socket location. Both the daemon and the
// client link this so they cannot disagree about where the socket lives.
package paths

import "net"

// SocketAddr returns a *net.Addr for the control socket.
func SocketAddr(name string) (net.Addr, error) {
	dir, err := RuntimeDir(name)
	if err != nil {
		return nil, err
	}
	return socketAddr(name, dir)
}
