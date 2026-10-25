// Package session keeps a Bubble Tea model alive independently of its terminal.
// A private local socket carries key events and rendered frames; one UI at a
// time attaches to the same model, engine, and state writer.
package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
	"github.com/melqtx/tork/internal/clipboard"
	"github.com/melqtx/tork/internal/intake"
)

const protocolVersion = 1
const connectTimeout = time.Second
const maxMessage = 2 << 20

type Launch struct {
	Target        *intake.Target
	DownloadDir   string
	Evil          bool
	Build         string
	Width, Height int
	ColorProfile  int
	Dark          bool
}

type request struct {
	Version       int
	Type          string
	Launch        Launch
	Key           tea.Key
	Width, Height int
	ClipboardID   uint64
	ClipboardOK   bool
}

type Frame struct {
	View      string
	Info      string
	Error     string
	Quit      bool
	PID       int
	Clipboard *Clipboard `json:",omitempty"`
}

type wire struct {
	conn   net.Conn
	reader *bufio.Reader
}

func newWire(conn net.Conn) *wire { return &wire{conn: conn, reader: bufio.NewReader(conn)} }
func (w *wire) read(v any) error {
	// A bounded line reader also bounds JSON strings and arrays before decode.
	var line []byte
	for {
		part, prefix, err := w.reader.ReadLine()
		if len(line)+len(part) > maxMessage {
			return errors.New("session message is too large")
		}
		line = append(line, part...)
		if err != nil {
			return err
		}
		if !prefix {
			return json.Unmarshal(line, v)
		}
	}
}
func (w *wire) write(v any) error {
	w.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	return json.NewEncoder(w.conn).Encode(v)
}

func Control(dir, op string) (Frame, error) {
	conn, err := dial(dir)
	if err != nil {
		return Frame{}, err
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	w := newWire(conn)
	if err := w.write(request{Version: protocolVersion, Type: op}); err != nil {
		return Frame{}, err
	}
	var frame Frame
	if err := w.read(&frame); err != nil {
		return frame, err
	}
	if frame.Error != "" {
		return frame, errors.New(frame.Error)
	}
	return frame, nil
}

type Client struct {
	wire    *wire
	initial Frame
}

func Attach(dir string, launch Launch) (*Client, error) {
	conn, err := dial(dir)
	if err != nil {
		return nil, err
	}
	w := newWire(conn)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if err := w.write(request{Version: protocolVersion, Type: "attach", Launch: launch}); err != nil {
		conn.Close()
		return nil, err
	}
	var frame Frame
	if err := w.read(&frame); err != nil {
		conn.Close()
		return nil, err
	}
	if frame.Error != "" {
		conn.Close()
		return nil, errors.New(frame.Error)
	}
	conn.SetReadDeadline(time.Time{})
	return &Client{wire: w, initial: frame}, nil
}

func (c *Client) Close() error   { return c.wire.conn.Close() }
func (c *Client) Initial() Frame { return c.initial }
func (c *Client) SendKey(key tea.KeyMsg) error {
	return c.wire.write(request{Type: "key", Key: tea.Key(key)})
}
func (c *Client) Resize(width, height int) error {
	return c.wire.write(request{Type: "resize", Width: width, Height: height})
}
func (c *Client) Read() (Frame, error) { var f Frame; err := c.wire.read(&f); return f, err }

type frameMsg struct {
	frame Frame
	err   error
}
type clipboardWrittenMsg struct {
	id  uint64
	err error
}
type terminalModel struct {
	client        *Client
	view          string
	err           error
	info          string
	copy          func(string) error
	lastClipboard uint64
}

func (m *terminalModel) Init() tea.Cmd { return m.wait() }
func (m *terminalModel) wait() tea.Cmd {
	return func() tea.Msg { f, err := m.client.Read(); return frameMsg{f, err} }
}
func (m *terminalModel) View() string { return m.view }
func (m *terminalModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case frameMsg:
		if msg.err != nil {
			if errors.Is(msg.err, io.EOF) {
				m.err = errors.New("background session disconnected; reopen tork to reconnect")
			} else {
				m.err = msg.err
			}
			return m, tea.Quit
		}
		if msg.frame.Error != "" {
			m.err = errors.New(msg.frame.Error)
			return m, tea.Quit
		}
		if msg.frame.Quit {
			m.info = msg.frame.Info
			return m, tea.Quit
		}
		m.view = msg.frame.View
		if copy := msg.frame.Clipboard; copy != nil && copy.ID > m.lastClipboard {
			m.lastClipboard = copy.ID
			return m, tea.Batch(m.wait(), func() tea.Msg {
				if m.copy == nil {
					return clipboardWrittenMsg{id: copy.ID, err: errors.New("no clipboard handler")}
				}
				return clipboardWrittenMsg{id: copy.ID, err: m.copy(copy.Text)}
			})
		}
		return m, m.wait()
	case clipboardWrittenMsg:
		m.err = m.client.wire.write(request{Type: "clipboard", ClipboardID: msg.id, ClipboardOK: msg.err == nil})
	case tea.KeyMsg:
		m.err = m.client.SendKey(msg)
	case tea.WindowSizeMsg:
		m.err = m.client.Resize(msg.Width, msg.Height)
	}
	if m.err != nil {
		return m, tea.Quit
	}
	return m, nil
}

func (c *Client) Run(options ...tea.ProgramOption) (string, error) {
	defer c.Close()
	out := clipboard.NewOutput(os.Stdout)
	var clipboardOut io.Writer
	if term.IsTerminal(os.Stdout.Fd()) {
		clipboardOut = out
	}
	m := &terminalModel{client: c, view: c.initial.View, copy: func(text string) error { return clipboard.Write(text, clipboardOut) }}
	options = append(options, tea.WithOutput(out))
	_, err := tea.NewProgram(m, options...).Run()
	if err != nil {
		return "", err
	}
	return m.info, m.err
}
