package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/provider"
)

// Search messages carry the generation that created them. A cancelled search
// can already have a command queued in Bubble Tea when the next search starts;
// the generation prevents that late result or provider status from entering the
// new result set.
type resultMsg struct {
	searchID uint64
	r        provider.Result
}

type resultsClosedMsg struct{ searchID uint64 }

type statusMsg struct {
	searchID uint64
	ev       aggregator.StatusEvent
}

type statusClosedMsg struct{ searchID uint64 }

type magnetResolvedMsg struct {
	searchID  uint64
	resolveID uint64
	res       provider.Result
	magnet    string
	preview   bool // route to the preview screen instead of downloading directly
	yank      bool // copy the magnet to the clipboard instead of acting on it
	err       error
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

// downloadPathsMsg returns filesystem checks performed outside View. Slow or
// remote download folders must never block Bubble Tea's render loop.
type downloadPathsMsg struct {
	checkID uint64
	exists  map[string]bool
}

// clearErrMsg uses the same generation rule as toasts: an old timer must never
// erase an error that happened more recently.
type clearErrMsg struct{ gen uint64 }

// clearToastMsg hides the transient confirmation box; gen must match the App's
// current toast generation so a timer from an earlier toast can't cut a newer
// one short.
type clearToastMsg struct{ gen int }

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

type verifyDoneMsg struct {
	hash   metainfo.Hash
	magnet string
	result engine.VerifyResult
	err    error
}

// waitForResult pumps one item off the search results channel into the tea
// loop, then re-arms itself from Update - the idiomatic streaming pattern.
func waitForResult(searchID uint64, ch <-chan provider.Result) tea.Cmd {
	return func() tea.Msg {
		r, ok := <-ch
		if !ok {
			return resultsClosedMsg{searchID: searchID}
		}
		return resultMsg{searchID: searchID, r: r}
	}
}

func waitForStatus(searchID uint64, ch <-chan aggregator.StatusEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return statusClosedMsg{searchID: searchID}
		}
		return statusMsg{searchID: searchID, ev: ev}
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

func clearErrCmd(gen uint64) tea.Cmd {
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg { return clearErrMsg{gen: gen} })
}

// clearToastCmd hides a confirmation after its dwell time, which is shorter
// than clearErrCmd's because a confirmation needs less reading than an error.
func clearToastCmd(gen int, dwell time.Duration) tea.Cmd {
	return tea.Tick(dwell, func(time.Time) tea.Msg { return clearToastMsg{gen: gen} })
}
