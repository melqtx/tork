// Package clipboard writes on the process that owns the user's terminal.
package clipboard

import (
	"errors"
	"io"
	"os"
	"sync"

	osclipboard "github.com/atotto/clipboard"
	"github.com/aymanbagabas/go-osc52/v2"
)

// Output serializes terminal clipboard sequences with renderer writes.
type Output struct {
	mu  sync.Mutex
	out io.Writer
}

func NewOutput(out io.Writer) *Output { return &Output{out: out} }
func (o *Output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.out.Write(p)
}

// Preserve the terminal interface through the synchronized writer. Bubble Tea
// needs all four methods to detect terminal size and clear changed rows.
func (o *Output) Fd() uintptr {
	if f, ok := o.out.(interface{ Fd() uintptr }); ok {
		return f.Fd()
	}
	return ^uintptr(0)
}
func (o *Output) Read(p []byte) (int, error) {
	if r, ok := o.out.(io.Reader); ok {
		return r.Read(p)
	}
	return 0, errors.New("output is not readable")
}
func (o *Output) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if c, ok := o.out.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

func Sequence(text string) osc52.Sequence {
	seq := osc52.New(text)
	switch {
	case os.Getenv("TMUX") != "":
		return seq.Tmux()
	case os.Getenv("STY") != "":
		return seq.Screen()
	default:
		return seq
	}
}

func Write(text string, out io.Writer) error {
	return write(text, out, osclipboard.WriteAll)
}

func write(text string, out io.Writer, systemCopy func(string) error) error {
	// Over SSH the desktop clipboard belongs to the connecting terminal, not
	// the remote host. Read terminal/tmux settings here, never in the daemon.
	if os.Getenv("SSH_TTY") == "" && os.Getenv("SSH_CONNECTION") == "" {
		if err := systemCopy(text); err == nil {
			return nil
		}
	}
	if out == nil {
		return errors.New("no terminal available for clipboard copy")
	}
	sequence := Sequence(text).String()
	n, err := out.Write([]byte(sequence))
	if err == nil && n != len(sequence) {
		return io.ErrShortWrite
	}
	return err
}
