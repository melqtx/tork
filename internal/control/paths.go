package control

import "net"

type Paths struct {
	Dir    string
	Socket net.Addr
	Lock   string
}

func ResolvePaths() (*Paths, error) {
	dir, err := runtimeDir("tork")
	if err != nil {
		return nil, err
	}
	sock, err := socketAddr("tork", dir)
	if err != nil {
		return nil, err
	}
	return &Paths{Dir: dir, Socket: sock}, nil
}
