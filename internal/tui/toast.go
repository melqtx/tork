package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// How long a toast sits on screen. A confirmation of something the user just
// asked for needs very little dwell; a verification result is worth reading.
const (
	toastQuick = 1500 * time.Millisecond
	toastLong  = 3 * time.Second
)

// toastTone picks the accent: calm green for "that worked", amber for a result
// that deserves a second look. Errors are not toasts - they take over the
// footer, where they stay put long enough to act on.
type toastTone int

const (
	toastOK toastTone = iota
	toastWarn
)

// toastState is the app's single transient-confirmation slot. Yanks, finished
// verifications, and queued downloads all pass through it, so only one box is
// ever on screen and the newest message always wins.
type toastState struct {
	text string
	tone toastTone
	gen  int // invalidates stale clear timers when toasts overlap
}

// showToast replaces whatever is on screen and returns the timer that clears
// it. The generation bump is what stops an older toast's timer from cutting a
// newer one short.
func (a *App) showToast(text string, tone toastTone, dwell time.Duration) tea.Cmd {
	a.toast.text = text
	a.toast.tone = tone
	a.toast.gen++
	return clearToastCmd(a.toast.gen, dwell)
}

func toastBox(t toastState) string {
	color, textStyle := colBrand, styleOK
	if t.tone == toastWarn {
		color, textStyle = colAmber, styleBest
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(color).Padding(0, 2)
	return box.Render(textStyle.Render(t.text))
}

// queuedToast names what just started, so a download that no longer yanks the
// user to the downloads screen still says plainly that it took.
func queuedToast(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == "?" {
		return "queued"
	}
	return "queued · " + truncate(name, 44)
}
