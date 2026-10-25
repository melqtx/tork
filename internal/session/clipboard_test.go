package session

import (
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type copyResult struct{ err error }
type copyModel struct{ progressModel }

func (m *copyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "c" {
		return m, func() tea.Msg {
			return ClipboardRequest{Text: "magnet:test", Reply: func(err error) tea.Msg { return copyResult{err} }}
		}
	}
	if result, ok := msg.(copyResult); ok {
		m.text = "copied"
		if result.err != nil {
			m.text = "copy failed"
		}
		return m, nil
	}
	_, cmd := m.progressModel.Update(msg)
	return m, cmd
}

func TestClipboardDeliverySurvivesFramesAndRequiresTerminalAcknowledgement(t *testing.T) {
	dir, _ := testServer(t, Hooks{}, &copyModel{})
	c, err := Attach(dir, Launch{Width: 80, Height: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.SendKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")}); err != nil {
		t.Fatal(err)
	}
	f := readUntil(t, c, func(f Frame) bool { return f.Clipboard != nil })
	id := f.Clipboard.ID
	for i := 0; i < 3; i++ {
		f = readUntil(t, c, func(f Frame) bool { return f.Clipboard != nil })
		if f.Clipboard.ID != id || f.Clipboard.Text != "magnet:test" || strings.Contains(f.View, "copied") {
			t.Fatal("progress lost the pending copy or reported success before acknowledgement")
		}
	}
	if err := c.wire.write(request{Type: "clipboard", ClipboardID: id, ClipboardOK: true}); err != nil {
		t.Fatal(err)
	}
	f = readUntil(t, c, func(f Frame) bool { return strings.Contains(f.View, "copied") })
	if f.Clipboard != nil {
		t.Fatal("acknowledged clipboard request was replayed")
	}
	if err := c.SendKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")}); err != nil {
		t.Fatal(err)
	}
	f = readUntil(t, c, func(f Frame) bool { return f.Clipboard != nil })
	if err := c.wire.write(request{Type: "clipboard", ClipboardID: f.Clipboard.ID, ClipboardOK: false}); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, func(f Frame) bool { return strings.Contains(f.View, "copy failed") })
}

func TestPendingClipboardNeverMovesToAnotherTerminal(t *testing.T) {
	p := &peer{frames: make(chan Frame, 1)}
	p.alive.Store(true)
	m := &ownerModel{model: &progressModel{}, peer: p}
	var result error
	reply := func(err error) tea.Msg { result = err; return nil }
	m.Update(clipboardRequestMsg{peer: p, request: ClipboardRequest{Text: "private magnet", Reply: reply}})
	id := m.clipboard[0].ID
	// A disconnected peer can be replaced before its detach event arrives.
	p.alive.Store(false)
	newPeer := &peer{frames: make(chan Frame, 1)}
	newPeer.alive.Store(true)
	_, cmd := m.Update(attachMsg{peer: newPeer, launch: Launch{Width: 80, Height: 24}})
	runClipboardCommands(cmd)
	if result == nil || len(m.clipboard) != 0 || (<-newPeer.frames).Clipboard != nil {
		t.Fatal("old terminal's copy reached the new terminal")
	}
	_, cmd = m.Update(clipboardRequestMsg{peer: p, request: ClipboardRequest{Text: "late copy", Reply: reply}})
	runClipboardCommands(cmd)
	if len(m.clipboard) != 0 {
		t.Fatal("late command from disconnected terminal queued a copy")
	}
	if _, cmd = m.Update(clipboardAckMsg{peer: p, id: id, ok: true}); cmd != nil {
		t.Fatal("stale acknowledgement changed the new terminal")
	}
}

func runClipboardCommands(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			runClipboardCommands(c)
		}
	}
}

func TestClipboardTimeoutReportsFailureAndIgnoresLateAcknowledgement(t *testing.T) {
	p := &peer{frames: make(chan Frame, 1)}
	p.alive.Store(true)
	m := &ownerModel{model: &progressModel{}, peer: p}
	var result error
	m.Update(clipboardRequestMsg{peer: p, request: ClipboardRequest{Text: "test", Reply: func(err error) tea.Msg { result = err; return nil }}})
	id := m.clipboard[0].ID
	_, cmd := m.Update(clipboardExpiredMsg(id))
	runClipboardCommands(cmd)
	if result == nil || len(m.clipboard) != 0 {
		t.Fatal("unacknowledged copy reported success or remained stuck")
	}
	if _, cmd = m.Update(clipboardAckMsg{peer: p, id: id, ok: true}); cmd != nil {
		t.Fatal("late acknowledgement reported success")
	}
}

func TestTerminalExecutesClipboardOnceAndAcknowledgesWriteResult(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "success"
		if failed {
			name = "write failure"
		}
		t.Run(name, func(t *testing.T) {
			terminal, server := net.Pipe()
			defer terminal.Close()
			defer server.Close()
			c := &Client{wire: newWire(terminal)}
			copies := 0
			m := &terminalModel{client: c, copy: func(text string) error {
				copies++
				if text != "magnet:test" {
					t.Error("copied the wrong value")
				}
				if failed {
					return errors.New("output failed")
				}
				return nil
			}}
			p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler())
			done := make(chan error, 1)
			go func() { _, err := p.Run(); done <- err }()
			defer p.Kill()
			w := newWire(server)
			server.SetReadDeadline(time.Now().Add(3 * time.Second))
			frame := Frame{View: "results", Clipboard: &Clipboard{ID: 1, Text: "magnet:test"}}
			if err := w.write(frame); err != nil {
				t.Fatal(err)
			}
			var ack request
			if err := w.read(&ack); err != nil {
				t.Fatal(err)
			}
			if ack.Type != "clipboard" || ack.ClipboardID != 1 || ack.ClipboardOK == failed {
				t.Fatal("clipboard result was not acknowledged honestly")
			}
			if err := w.write(frame); err != nil {
				t.Fatal(err)
			}
			if err := w.write(Frame{Quit: true}); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("terminal did not close")
			}
			if copies != 1 {
				t.Fatal("coalesced frame copied repeatedly")
			}
		})
	}
}
