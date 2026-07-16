package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/provider"
)

type resultMsg struct{ r provider.Result }
type resultsClosedMsg struct{}
type statusMsg struct{ ev aggregator.StatusEvent }
type statusClosedMsg struct{}

type magnetResolvedMsg struct {
	res     provider.Result
	magnet  string
	preview bool // route to the preview screen instead of downloading directly
	yank    bool // copy the magnet to the clipboard instead of acting on it
	err     error
}

type torrentAddedMsg struct {
	hash   metainfo.Hash
	magnet string // resume key: magnet URI, or https URL for direct downloads
	name   string
	sha256 string // expected digest for direct downloads
	err    error
}

type previewReadyMsg struct {
	hash   metainfo.Hash
	magnet string
	name   string
	from   screen
	owned  bool
	err    error
}

type tickMsg time.Time
type clearErrMsg struct{}

// clearYankMsg hides the yank toast; gen must match the App's current yank
// generation so a timer from an earlier yank can't cut a newer toast short.
type clearYankMsg struct{ gen int }

// proxyCheckMsg is produced by the bounded, SOCKS-routed egress check. It
// deliberately carries no egress IP because the status bar only needs to show
// the route's state; doctor is the explicit place that prints the address.
type proxyCheckMsg struct {
	isTor bool
	err   error
}

// healthDoneMsg reports a manual health re-check finishing; the store already
// holds the new snapshot, so only the error travels.
type healthDoneMsg struct{ err error }

// waitForResult pumps one item off the search results channel into the tea
// loop, then re-arms itself from Update - the idiomatic streaming pattern.
func waitForResult(ch <-chan provider.Result) tea.Cmd {
	return func() tea.Msg {
		r, ok := <-ch
		if !ok {
			return resultsClosedMsg{}
		}
		return resultMsg{r}
	}
}

func waitForStatus(ch <-chan aggregator.StatusEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return statusClosedMsg{}
		}
		return statusMsg{ev}
	}
}

// guard converts a panic inside a tea.Cmd into a message, so a failing
// provider or engine call surfaces as an error instead of crashing the app.
func guard(msg *tea.Msg, onPanic func(any) tea.Msg) {
	if r := recover(); r != nil {
		*msg = onPanic(r)
	}
}

func tickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func clearErrCmd() tea.Cmd {
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg { return clearErrMsg{} })
}

// clearYankCmd hides the yank toast; shorter than clearErrCmd because a
// confirmation needs less dwell time than an error.
func clearYankCmd(gen int) tea.Cmd {
	return tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg { return clearYankMsg{gen: gen} })
}
