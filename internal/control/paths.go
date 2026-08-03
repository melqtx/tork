package control

import "net"

// SocketAddr returns a *net.Addr for the control socket.
func SocketAddr(name string) (net.Addr, error) {
	dir, err := RuntimeDir(name)
	if err != nil {
		return nil, err
	}
	return socketAddr(name, dir)
}
