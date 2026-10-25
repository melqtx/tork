package clipboard

import (
	"context"
	"os"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
)

type sizeModel struct{ width, height int }

func (m *sizeModel) Init() tea.Cmd { return nil }
func (m *sizeModel) View() string  { return "Checking terminal dimensions" }
func (m *sizeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = size.Width, size.Height
		return m, tea.Quit
	}
	return m, nil
}

// Run the compiled test binary inside a PTY to exercise the real renderer's
// terminal detection; ordinary noninteractive test runs use the interface test.
func TestOutputDetectsTTYSize(t *testing.T) {
	if !term.IsTerminal(os.Stdout.Fd()) {
		t.Skip("requires a terminal or PTY")
	}
	width, height, err := term.GetSize(os.Stdout.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	m := &sizeModel{}
	_, err = tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(NewOutput(os.Stdout)), tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	if err != nil {
		t.Fatal(err)
	}
	if m.width != width || m.height != height {
		t.Fatalf("got %dx%d, want %dx%d", m.width, m.height, width, height)
	}
}
