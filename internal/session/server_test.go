package session

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type progressMsg struct{}
type progressModel struct {
	progress      int
	text          string
	width, height int
}

func (m *progressModel) Init() tea.Cmd { return m.tick() }
func (m *progressModel) tick() tea.Cmd {
	return tea.Tick(10*time.Millisecond, func(time.Time) tea.Msg { return progressMsg{} })
}
func (m *progressModel) View() string {
	return fmt.Sprintf("%d|%s|%dx%d", m.progress, m.text, m.width, m.height)
}
func (m *progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case progressMsg:
		m.progress++
		return m, m.tick()
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Batch(tea.Quit, func() tea.Msg { return nil })
		}
		m.text += msg.String()
	}
	return m, nil
}

func testServer(t *testing.T, hooks Hooks, models ...tea.Model) (string, <-chan error) {
	t.Helper()
	if !Supported {
		t.Skip("Unix session transport")
	}
	dir, err := os.MkdirTemp("", "tork-session-")
	if err != nil {
		t.Fatal(err)
	}
	// macOS's default temp prefix can exceed the Unix socket path limit.
	if len(filepath.Join(dir, "session", "ui.sock")) > 100 {
		os.RemoveAll(dir)
		dir, err = os.MkdirTemp("/tmp", "tork-session-")
		if err != nil {
			t.Fatal(err)
		}
	}
	ln, cleanup, err := Listen(dir)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var model tea.Model = &progressModel{}
	if len(models) > 0 {
		model = models[0]
	}
	go func() { done <- Serve(ctx, ln, model, hooks) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("session did not shut down")
		}
		cleanup()
		os.RemoveAll(dir)
	})
	return dir, done
}

func readUntil(t *testing.T, c *Client, predicate func(Frame) bool) Frame {
	t.Helper()
	c.wire.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	defer c.wire.conn.SetReadDeadline(time.Time{})
	for {
		f, err := c.Read()
		if err != nil {
			t.Fatal(err)
		}
		if predicate(f) {
			return f
		}
	}
}

func TestSessionKeepsWorkingAfterQuitAndAbruptDisconnect(t *testing.T) {
	var saves atomic.Int32
	dir, _ := testServer(t, Hooks{Checkpoint: func() error { saves.Add(1); return nil }})
	launch := Launch{Width: 80, Height: 24, ColorProfile: 3, Dark: true}
	c, err := Attach(dir, launch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Attach(dir, launch); err == nil || !strings.Contains(err.Error(), "another terminal") {
		t.Fatalf("second UI accepted: %v", err)
	}
	if err := c.SendKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("saved query")}); err != nil {
		t.Fatal(err)
	}
	f := readUntil(t, c, func(f Frame) bool { return strings.Contains(f.View, "saved query") })
	before, _ := strconv.Atoi(strings.Split(f.View, "|")[0])
	if err := c.SendKey(tea.KeyMsg{Type: tea.KeyCtrlC}); err != nil {
		t.Fatal(err)
	}
	f = readUntil(t, c, func(f Frame) bool { return f.Quit })
	if !strings.Contains(f.Info, "continue") {
		t.Fatal("quit did not explain background transfers")
	}
	c.Close()
	time.Sleep(60 * time.Millisecond)
	c, err = Attach(dir, launch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.initial.View, "saved query") {
		t.Fatal("reattach lost model state")
	}
	after, _ := strconv.Atoi(strings.Split(c.initial.View, "|")[0])
	if after <= before {
		t.Fatal("work stopped when UI quit")
	}
	if err := c.Resize(40, 10); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, func(f Frame) bool { return strings.Contains(f.View, "40x10") })
	// Closing a socket without a quit key models a terminal crash or SIGHUP.
	c.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		c, err = Attach(dir, launch)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer c.Close()
	if !strings.Contains(c.initial.View, "saved query") {
		t.Fatal("terminal crash lost session state")
	}
	f, err = Control(dir, "status")
	if err != nil || !strings.Contains(f.Info, "UI attached") {
		t.Fatalf("status: %+v %v", f, err)
	}
	f, err = Control(dir, "stop")
	if err != nil || !strings.Contains(f.Info, "stopped") {
		t.Fatalf("stop: %+v %v", f, err)
	}
	readUntil(t, c, func(f Frame) bool { return f.Quit })
	if saves.Load() == 0 {
		t.Fatal("stop skipped persistence")
	}
}

func TestStopSaveFailureLeavesSessionAvailable(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	dir, _ := testServer(t, Hooks{Checkpoint: func() error {
		if fail.Load() {
			return errors.New("disk full")
		}
		return nil
	}})
	if _, err := Control(dir, "stop"); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatal("stop hid save failure")
	}
	if _, err := Control(dir, "status"); err != nil {
		t.Fatal("failed save still killed the session")
	}
	fail.Store(false)
	if _, err := Control(dir, "stop"); err != nil {
		t.Fatal(err)
	}
}

func TestSessionLockPermissionsAndStaleSocket(t *testing.T) {
	if !Supported {
		t.Skip("Unix session transport")
	}
	dir, err := os.MkdirTemp("/tmp", "tork-session-lock-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ln, cleanup, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for path, want := range map[string]os.FileMode{filepath.Join(dir, "session"): 0700, filepath.Join(dir, "session", "ui.sock"): 0600} {
		fi, err := os.Stat(path)
		if err != nil || fi.Mode().Perm() != want {
			t.Fatalf("unsafe session permissions %s: %v", path, err)
		}
	}
	if _, _, err := Listen(dir); !errors.Is(err, ErrRunning) {
		t.Fatalf("two workers acquired ownership: %v", err)
	}
	cleanup()
	// Create an abandoned socket without a worker holding the owner lock.
	stale, err := net.Listen("unix", filepath.Join(dir, "session", "ui.sock"))
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	stale.Close()
	ln, cleanup2, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup2()
	ln.Close()
}

func TestSessionRejectsSymlinkDirectoryAndUnknownProtocol(t *testing.T) {
	if !Supported {
		t.Skip("Unix session transport")
	}
	dir := t.TempDir()
	other := t.TempDir()
	if err := os.Symlink(other, filepath.Join(dir, "session")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Listen(dir); err == nil {
		t.Fatal("followed a symlink for the private session directory")
	}
	dir, _ = testServer(t, Hooks{})
	conn, err := dial(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	w := newWire(conn)
	if err := w.write(request{Version: protocolVersion + 1, Type: "attach"}); err != nil {
		t.Fatal(err)
	}
	var frame Frame
	if err := w.read(&frame); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(frame.Error, "incompatible") {
		t.Fatal("accepted an incompatible UI protocol")
	}
	if _, err := Control(dir, "status"); err != nil {
		t.Fatal("bad client killed worker")
	}
}
