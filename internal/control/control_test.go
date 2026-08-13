//go:build unix

package control

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

// isolate points runtimeDir at a scratch directory so the tests never touch a
// real daemon's socket, and returns where it landed.
func isolate(t *testing.T) paths {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root: runtimeDir would resolve to /run")
	}
	dir := t.TempDir()
	t.Setenv("RUNTIME_DIRECTORY", "") // systemd's override is checked first
	t.Setenv("XDG_RUNTIME_DIR", dir)  // linux
	t.Setenv("TMPDIR", dir)           // darwin, and the linux fallback
	p, err := resolvePaths()
	if err != nil {
		t.Fatalf("resolvePaths: %v", err)
	}
	return p
}

func TestListenIsExclusive(t *testing.T) {
	isolate(t)
	l, err := Listen()
	if err != nil {
		t.Fatalf("first Listen: %v", err)
	}
	defer l.Close()

	// Same process, second descriptor. flock conflicts across open file
	// descriptions even within one process, which is what makes this a real
	// test rather than a coincidence — fcntl(F_SETLK) would happily grant it.
	if _, err := Listen(); !errors.Is(err, ErrDaemonRunning) {
		t.Fatalf("second Listen: got %v, want ErrDaemonRunning", err)
	}
}

func TestListenAfterClose(t *testing.T) {
	isolate(t)
	l, err := Listen()
	if err != nil {
		t.Fatalf("first Listen: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// The lock file is deliberately left on disk. Its existence must not be
	// what a restart trips over.
	l2, err := Listen()
	if err != nil {
		t.Fatalf("Listen after Close: %v", err)
	}
	l2.Close()
}

func TestListenClearsStaleSocket(t *testing.T) {
	p := isolate(t)
	l0, err := Listen() // creates the runtime directory, then gets out of the way
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if err := l0.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// A crashed daemon leaves the socket behind: nothing unlinks it, and the
	// next bind would fail with EADDRINUSE if Listen did not clear it.
	if err := os.WriteFile(p.socket, nil, 0o600); err != nil {
		t.Fatalf("plant stale socket: %v", err)
	}
	l, err := Listen()
	if err != nil {
		t.Fatalf("Listen over stale socket: %v", err)
	}
	l.Close()
}

func TestDialWithoutDaemon(t *testing.T) {
	isolate(t)
	if _, err := Dial(context.Background()); !errors.Is(err, ErrNoDaemon) {
		t.Fatalf("Dial: got %v, want ErrNoDaemon", err)
	}
}

func TestDialRoundTrip(t *testing.T) {
	isolate(t)
	l, err := Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer l.Close()

	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		io.Copy(c, c)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	c.(*net.UnixConn).CloseWrite()
	got, err := io.ReadAll(c)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "ping" {
		t.Fatalf("got %q, want %q", got, "ping")
	}
}

func TestSocketModeIsPrivate(t *testing.T) {
	p := isolate(t)
	l, err := Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer l.Close()

	st, err := os.Stat(p.socket)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if perm := st.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("socket mode %#o, want no group or other bits", perm)
	}
}
