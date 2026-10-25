package session

import (
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ClipboardRequest asks the attached terminal to copy and reports the result
// to the application only after that terminal acknowledges the write.
type ClipboardRequest struct {
	Text  string
	Reply func(error) tea.Msg
}

type Clipboard struct {
	ID   uint64
	Text string
}

type clipboardRequestMsg struct {
	peer    *peer
	request ClipboardRequest
}
type clipboardAckMsg struct {
	peer *peer
	id   uint64
	ok   bool
}
type clipboardExpiredMsg uint64
type pendingClipboard struct {
	Clipboard
	peer  *peer
	reply func(error) tea.Msg
}

func clipboardReply(reply func(error) tea.Msg, err error) tea.Cmd {
	if reply == nil {
		return nil
	}
	return func() tea.Msg { return reply(err) }
}

func (m *ownerModel) requestClipboard(msg clipboardRequestMsg) tea.Cmd {
	if msg.peer == nil || msg.peer != m.peer || !msg.peer.alive.Load() {
		return clipboardReply(msg.request.Reply, errors.New("clipboard copy cancelled: terminal closed"))
	}
	if len(m.clipboard) >= 8 || len(msg.request.Text) > 256<<10 {
		return clipboardReply(msg.request.Reply, errors.New("clipboard request is too large or clipboard is busy"))
	}
	m.clipboardSeq++
	id := m.clipboardSeq
	m.clipboard = append(m.clipboard, pendingClipboard{Clipboard: Clipboard{ID: id, Text: msg.request.Text}, peer: msg.peer, reply: msg.request.Reply})
	m.publish()
	return tea.Tick(10*time.Second, func(time.Time) tea.Msg { return clipboardExpiredMsg(id) })
}

func (m *ownerModel) finishClipboard(id uint64, err error) tea.Cmd {
	for i, pending := range m.clipboard {
		if pending.ID != id {
			continue
		}
		copy(m.clipboard[i:], m.clipboard[i+1:])
		m.clipboard[len(m.clipboard)-1] = pendingClipboard{}
		m.clipboard = m.clipboard[:len(m.clipboard)-1]
		m.publish()
		return clipboardReply(pending.reply, err)
	}
	return nil
}

func (m *ownerModel) cancelClipboard() tea.Cmd {
	var cmds []tea.Cmd
	for _, pending := range m.clipboard {
		cmds = append(cmds, clipboardReply(pending.reply, errors.New("clipboard copy cancelled: terminal closed")))
	}
	m.clipboard = nil
	return tea.Batch(cmds...)
}
