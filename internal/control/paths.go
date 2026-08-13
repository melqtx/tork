package control

import "path/filepath"

// name is the stem of every runtime file, and of the runtime directory itself.
const name = "tork"

// paths is where the daemon lives. It stays unexported: the socket location is
// an implementation detail of Listen/Dial, and anything that learns it will
// eventually try to open it by hand.
type paths struct {
	dir    string
	socket string
	lock   string
}

// resolvePaths is linked into both the daemon and the client, which is what
// stops them from disagreeing about where the socket is.
func resolvePaths() (paths, error) {
	dir, err := runtimeDir(name)
	if err != nil {
		return paths{}, err
	}
	sock, err := socketPath(name, dir)
	if err != nil {
		return paths{}, err
	}
	return paths{dir: dir, socket: sock, lock: filepath.Join(dir, name+".lock")}, nil
}
