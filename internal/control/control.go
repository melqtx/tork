// Package control is the seam between the front end and the torrent engine.
//
// It owns two things: the [Engine] interface, which is the whole surface the
// TUI is allowed to touch, and the transport both sides use to reach a daemon
// that implements it. Callers never see a socket path — they get a [Listener]
// or a [net.Conn] — so the day this grows a Windows named-pipe backend, no
// caller changes.
//
// Today the only Engine implementation is *engine.Engine, called in-process; a
// daemon-backed client speaking over the socket becomes a second one, and the
// TUI cannot tell them apart.
package control

import (
	"context"
	"errors"
	"net"
	"os"
)

// ErrDaemonRunning is returned by [Listen] when another daemon already holds
// the runtime lock. It means "alive right now", not "a file was left behind" —
// see the lock handling in unix.go for why those are the same question.
var ErrDaemonRunning = errors.New("another tork daemon is already running")

// ErrNoDaemon is returned by [Dial] when nothing is listening. Callers use it
// to decide whether to fall back to an in-process engine, so it has to be
// distinguishable from a genuine transport failure.
var ErrNoDaemon = errors.New("no tork daemon is running")

// Listener is a control socket plus the exclusive claim that makes it safe to
// own. It is what [Listen] hands back, and closing it releases both.
type Listener struct {
	net.Listener
	lock *os.File
}

// Close tears down the socket before releasing the lock. The reverse order
// would open a window where the socket is still live and bindable by a second
// daemon, which is the exact race the lock exists to prevent.
func (l *Listener) Close() error {
	err := l.Listener.Close() // a unix listener unlinks its own socket
	if lerr := l.lock.Close(); err == nil {
		err = lerr
	}
	return err
}

// Listen claims the runtime directory and serves the control socket. It fails
// with [ErrDaemonRunning] rather than displacing a live daemon.
func Listen() (*Listener, error) {
	p, err := resolvePaths()
	if err != nil {
		return nil, err
	}
	return listen(p)
}

// Dial connects to a running daemon, or fails with [ErrNoDaemon].
func Dial(ctx context.Context) (net.Conn, error) {
	p, err := resolvePaths()
	if err != nil {
		return nil, err
	}
	return dial(ctx, p)
}
