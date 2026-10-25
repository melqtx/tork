package session

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

type Hooks struct {
	Attach     func(Launch) (tea.Cmd, error)
	Detach     func() tea.Cmd
	Checkpoint func() error
	Status     func() string
}

type peer struct {
	wire     *wire
	frames   chan Frame
	alive    atomic.Bool
	done     chan struct{}
	finished chan struct{}
}

func (p *peer) send(f Frame) {
	select {
	case p.frames <- f:
		return
	default:
	}
	select {
	case <-p.frames:
	default:
	}
	select {
	case p.frames <- f:
	default:
	}
}

type attachMsg struct {
	peer   *peer
	launch Launch
}
type detachMsg struct{ peer *peer }
type inputMsg struct {
	peer *peer
	msg  tea.Msg
}
type controlMsg struct {
	op      string
	reply   chan Frame
	written chan struct{}
}

type ownerModel struct {
	model        tea.Model
	hooks        Hooks
	peer         *peer
	stopping     bool
	clipboard    []pendingClipboard
	clipboardSeq uint64
}

func (m *ownerModel) Init() tea.Cmd { return m.wrap(m.model.Init(), nil) }
func (m *ownerModel) View() string  { return "" }

// Keep a quit from the application local to the terminal connection. The
// persistent event loop and transfers only stop on the explicit stop command.
func (m *ownerModel) wrap(cmd tea.Cmd, p *peer) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		switch msg := cmd().(type) {
		case ClipboardRequest:
			return clipboardRequestMsg{peer: p, request: msg}
		case tea.QuitMsg:
			return detachMsg{p}
		case tea.BatchMsg:
			batch := make(tea.BatchMsg, len(msg))
			for i, c := range msg {
				batch[i] = m.wrap(c, p)
			}
			return batch
		default:
			return msg
		}
	}
}

func (m *ownerModel) publish() {
	if m.peer != nil && m.peer.alive.Load() {
		frame := Frame{View: m.model.View(), PID: os.Getpid()}
		// Keep the request in every coalesced frame until acknowledgement; a
		// progress update must never overwrite a clipboard side effect.
		if len(m.clipboard) > 0 && m.clipboard[0].peer == m.peer {
			copy := m.clipboard[0].Clipboard
			frame.Clipboard = &copy
		}
		m.peer.send(frame)
	}
}

func (m *ownerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case clipboardRequestMsg:
		return m, m.wrap(m.requestClipboard(msg), msg.peer)
	case clipboardAckMsg:
		if msg.peer != m.peer || len(m.clipboard) == 0 || m.clipboard[0].peer != msg.peer || m.clipboard[0].ID != msg.id {
			return m, nil
		}
		var err error
		if !msg.ok {
			err = errors.New("terminal clipboard write failed")
		}
		return m, m.wrap(m.finishClipboard(msg.id, err), msg.peer)
	case clipboardExpiredMsg:
		return m, m.wrap(m.finishClipboard(uint64(msg), errors.New("terminal clipboard did not respond; retry the copy")), m.peer)
	case attachMsg:
		if m.stopping {
			msg.peer.send(Frame{Error: "background session is shutting down; wait before reopening tork"})
			return m, nil
		}
		if m.peer != nil && m.peer.alive.Load() {
			msg.peer.send(Frame{Error: "another terminal is attached; close that UI first"})
			return m, nil
		}
		var cmd tea.Cmd
		var err error
		if m.hooks.Attach != nil {
			cmd, err = m.hooks.Attach(msg.launch)
		}
		if err != nil {
			msg.peer.send(Frame{Error: err.Error()})
			return m, nil
		}
		cancelClipboard := m.cancelClipboard()
		m.peer = msg.peer
		if msg.launch.ColorProfile >= int(termenv.TrueColor) && msg.launch.ColorProfile <= int(termenv.Ascii) {
			lipgloss.SetColorProfile(termenv.Profile(msg.launch.ColorProfile))
		}
		lipgloss.SetHasDarkBackground(msg.launch.Dark)
		var resize tea.Cmd
		m.model, resize = m.model.Update(tea.WindowSizeMsg{Width: boundedWidth(msg.launch.Width), Height: boundedHeight(msg.launch.Height)})
		m.publish()
		return m, m.wrap(tea.Batch(cancelClipboard, cmd, resize), m.peer)
	case detachMsg:
		if m.peer == nil || m.peer != msg.peer {
			return m, nil
		}
		m.peer.send(Frame{Quit: true, Info: "UI closed. Downloads and seeding continue in the background. Reopen tork to reconnect; use tork stop to stop transfers."})
		m.peer = nil
		cancelClipboard := m.cancelClipboard()
		if m.hooks.Detach != nil {
			return m, m.wrap(tea.Batch(cancelClipboard, m.hooks.Detach()), nil)
		}
		return m, m.wrap(cancelClipboard, nil)
	case inputMsg:
		if m.peer != msg.peer {
			return m, nil
		}
		msgToModel := msg.msg
		var cmd tea.Cmd
		m.model, cmd = m.model.Update(msgToModel)
		m.publish()
		return m, m.wrap(cmd, m.peer)
	case controlMsg:
		frame := Frame{PID: os.Getpid()}
		if m.hooks.Status != nil {
			frame.Info = m.hooks.Status()
		}
		if m.peer != nil && m.peer.alive.Load() {
			frame.Info += " · UI attached"
		} else {
			frame.Info += " · UI closed"
		}
		if msg.op == "stop" {
			if m.hooks.Checkpoint != nil {
				if err := m.hooks.Checkpoint(); err != nil {
					frame.Error = "could not save session: " + err.Error()
					msg.reply <- frame
					return m, nil
				}
			}
			frame.Info = "Background session stopped. Downloads are saved and will resume when tork starts again."
			m.stopping = true
			var uiFinished <-chan struct{}
			if m.peer != nil {
				m.peer.send(Frame{Quit: true, Info: frame.Info})
				uiFinished = m.peer.finished
				m.peer = nil
			}
			msg.reply <- frame
			return m, func() tea.Msg {
				select {
				case <-msg.written:
				case <-time.After(3 * time.Second):
				}
				if uiFinished != nil {
					select {
					case <-uiFinished:
					case <-time.After(3 * time.Second):
					}
				}
				return tea.Quit()
			}
		}
		msg.reply <- frame
		return m, nil
	}
	var cmd tea.Cmd
	m.model, cmd = m.model.Update(msg)
	m.publish()
	return m, m.wrap(cmd, m.peer)
}

func boundedWidth(w int) int {
	if w <= 0 {
		return 100
	}
	return max(8, min(500, w))
}
func boundedHeight(h int) int {
	if h <= 0 {
		return 30
	}
	return max(5, min(200, h))
}

// Serve runs the model without a terminal renderer. Network goroutines never
// touch application state; all input, snapshots, and persistence stay on its
// existing event loop. Slow/disconnected terminals cannot stall transfers.
func Serve(ctx context.Context, ln net.Listener, model tea.Model, hooks Hooks) error {
	owner := &ownerModel{model: model, hooks: hooks}
	program := tea.NewProgram(owner, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler(), tea.WithContext(ctx))
	var wg sync.WaitGroup
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	finished := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(finished)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections[conn] = true
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { conn.Close(); mu.Lock(); delete(connections, conn); mu.Unlock() }()
				serveConnection(conn, program, exited)
			}()
		}
	}()
	_, err := program.Run()
	close(exited)
	ln.Close()
	<-finished // no more wg.Add calls before Wait
	mu.Lock()
	for conn := range connections {
		conn.Close()
	}
	mu.Unlock()
	wg.Wait()
	if hooks.Checkpoint != nil {
		if saveErr := hooks.Checkpoint(); saveErr != nil {
			return saveErr
		}
	}
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil
	}
	return err
}

func serveConnection(conn net.Conn, program *tea.Program, exited <-chan struct{}) {
	w := newWire(conn)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var req request
	if err := w.read(&req); err != nil {
		return
	}
	if req.Version != protocolVersion {
		_ = w.write(Frame{Error: "incompatible session protocol; stop the old session before restarting"})
		return
	}
	conn.SetReadDeadline(time.Time{})
	if req.Type == "status" || req.Type == "stop" {
		reply := make(chan Frame, 1)
		written := make(chan struct{})
		defer close(written)
		program.Send(controlMsg{op: req.Type, reply: reply, written: written})
		select {
		case frame := <-reply:
			_ = w.write(frame)
		case <-exited:
		}
		return
	}
	if req.Type != "attach" {
		_ = w.write(Frame{Error: "unknown session request"})
		return
	}
	p := &peer{wire: w, frames: make(chan Frame, 1), done: make(chan struct{}), finished: make(chan struct{})}
	p.alive.Store(true)
	go func() {
		defer close(p.finished)
		defer conn.Close()
		for {
			select {
			case f := <-p.frames:
				if err := w.write(f); err != nil {
					return
				}
				if f.Quit || f.Error != "" {
					return
				}
			case <-p.done:
				return
			}
		}
	}()
	program.Send(attachMsg{peer: p, launch: req.Launch})
	for {
		if err := w.read(&req); err != nil {
			break
		}
		switch req.Type {
		case "key":
			if len(req.Key.Runes) > 4096 {
				continue
			}
			program.Send(inputMsg{peer: p, msg: tea.KeyMsg(req.Key)})
		case "resize":
			program.Send(inputMsg{peer: p, msg: tea.WindowSizeMsg{Width: boundedWidth(req.Width), Height: boundedHeight(req.Height)}})
		case "clipboard":
			program.Send(clipboardAckMsg{peer: p, id: req.ClipboardID, ok: req.ClipboardOK})
		}
	}
	p.alive.Store(false)
	program.Send(detachMsg{p})
	close(p.done)
	<-p.finished
}
