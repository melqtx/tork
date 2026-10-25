package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/sources"
)

// Report search outcomes, not general source health. A reachable source with
// zero matches and a failed source are different outcomes.
func searchSourceOutcome(ev aggregator.StatusEvent, ok, searching bool) string {
	switch {
	case !ok && searching, ok && ev.State == aggregator.StateSearching:
		return "searching…"
	case !ok:
		return "not searched"
	case ev.State == aggregator.StateFailed && ev.Count == 0 && ev.RetryAt.After(time.Now()):
		return blockedRetryLabel(ev.RetryAt)
	case ev.State == aggregator.StateDone && ev.Count > 0:
		return plural(ev.Count, "result")
	case ev.State == aggregator.StateDone:
		return "no matches"
	case ev.Count > 0:
		return fmt.Sprintf("%d results · partial", ev.Count)
	default:
		return searchSourceFailure(ev.Err)
	}
}

func blockedRetryLabel(until time.Time) string {
	delay := time.Until(until)
	if delay <= 0 {
		return "blocked · ready to retry"
	}
	minutes := (delay + time.Minute - 1) / time.Minute
	return fmt.Sprintf("blocked · retry in %dm", minutes)
}

// Raw errors may contain feed URLs or credentials. Keep the same safe labels
// in the compact list and the expanded search-status panel.
func searchSourceFailure(err error) string {
	switch {
	case errors.Is(err, provider.ErrFeedAuth):
		return "auth failed"
	case errors.Is(err, provider.ErrFeedFormat):
		return "invalid feed"
	case errors.Is(err, provider.ErrFeedUnsupported):
		return "unsupported links"
	case errors.Is(err, provider.ErrFeedAPI):
		return "search rejected"
	case err != nil && strings.Contains(err.Error(), "blocked"):
		return "blocked"
	default:
		return "unavailable"
	}
}

func (a *App) resultSourceSummary(width int) string {
	ready, empty, partial, failed, pending, paused := 0, 0, 0, 0, 0, 0
	for _, p := range a.agg.Providers() {
		ev, ok := a.results.status[provider.DisplayName(p)]
		switch {
		case !ok && a.results.searching, ok && ev.State == aggregator.StateSearching:
			pending++
		case !ok:
			continue
		case ev.State == aggregator.StateFailed && ev.Count > 0:
			partial++
		case ev.State == aggregator.StateFailed && ev.RetryAt.After(time.Now()):
			paused++
		case ev.State == aggregator.StateFailed:
			failed++
		case ev.Count > 0:
			ready++
		default:
			empty++
		}
	}
	parts := []string{}
	if ready > 0 {
		parts = append(parts, styleOK.Render(fmt.Sprintf("%d with results", ready)))
	}
	if failed > 0 {
		parts = append(parts, styleHealthMid.Render(fmt.Sprintf("%d failed", failed)))
	}
	if paused > 0 {
		parts = append(parts, styleHealthMid.Render(fmt.Sprintf("%d paused", paused)))
	}
	if partial > 0 {
		parts = append(parts, styleHealthMid.Render(fmt.Sprintf("%d partial", partial)))
	}
	if pending > 0 {
		parts = append(parts, styleDim.Render(fmt.Sprintf("%d searching", pending)))
	}
	if empty > 0 {
		parts = append(parts, styleDim.Render(fmt.Sprintf("%d empty", empty)))
	}
	left := styleDim.Render("Sources  ") + strings.Join(parts, styleFaint.Render(" · "))
	if len(parts) == 0 {
		label := "not searched"
		if len(a.agg.Providers()) == 0 {
			label = "none enabled"
		}
		left += styleDim.Render(label)
	}
	return splitResultLine(left, hint("s", "details"), width)
}

// Reserve the action on the right, even when the source fleet grows.
func splitResultLine(left, right string, width int) string {
	if right == "" {
		return truncate(left, width)
	}
	if width < lipgloss.Width(right)+4 {
		return truncate(left, width)
	}
	left = truncate(left, width-lipgloss.Width(right)-2)
	return padRight(left, width-lipgloss.Width(right)) + right
}

func (a *App) updateResultSources(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := &a.results
	n, h := len(a.agg.Providers()), max(1, a.bodyHeight()-5)
	switch msg.String() {
	case "esc", "s":
		r.sourcePanel = false
	case "up", "k":
		r.sourceWin.move(-1, n, h)
	case "down", "j":
		r.sourceWin.move(1, n, h)
	case "pgup":
		r.sourceWin.move(-h, n, h)
	case "pgdown":
		r.sourceWin.move(h, n, h)
	case "g", "home":
		r.sourceWin.home()
	case "G", "end":
		r.sourceWin.end(n, h)
	case "t":
		if n == 0 {
			return a, nil
		}
		r.sourceWin.clamp(n, h)
		name := provider.DisplayName(a.agg.Providers()[r.sourceWin.cursor])
		for i, s := range sources.List(a.cfg) {
			if s.RuntimeName == name {
				r.sourcePanel = false
				a.sources.win.cursor = i
				navigate := a.navigate(screenSources)
				return a, tea.Batch(navigate, a.checkSource(s))
			}
		}
		return a, a.showError("source settings changed · open Sources to check the current configuration")
	case "q":
		return a, tea.Quit
	}
	return a, nil
}

func (a *App) viewResultSources() string {
	r := &a.results
	w := a.contentWidth()
	providers := a.agg.Providers()
	caption := "Search complete. Empty sources are reachable, with no matches."
	if r.searching {
		caption = "Results keep arriving while this panel is open."
	}
	body := styleTitle.Render("Sources for this search") + "\n" + styleDim.Render(caption) + "\n"
	if len(providers) == 0 {
		body += "\n" + styleDim.Render("No sources enabled. Add one from Sources on home.")
	} else {
		body += renderWindow(&r.sourceWin, len(providers), min(len(providers), max(1, a.bodyHeight()-5)), w, func(i int, _ bool) string {
			name := provider.DisplayName(providers[i])
			ev, ok := r.status[name]
			nameW := max(1, (w-3)/2)
			return padRight(truncate(providerTag(name), nameW), nameW) + "  " + styleDim.Render(searchSourceOutcome(ev, ok, r.searching))
		})
		name := provider.DisplayName(providers[r.sourceWin.cursor])
		ev, ok := r.status[name]
		body += "\n" + rule(w) + "\n" + styleFg.Render(name) + "\n" + styleDim.Render(searchSourceOutcome(ev, ok, r.searching))
		if ok && ev.State == aggregator.StateFailed && ev.Count > 0 {
			body += styleDim.Render(" · " + searchSourceFailure(ev.Err))
		}
		if ev.Hidden > 0 {
			body += styleDim.Render(fmt.Sprintf(" · %d hidden by content settings", ev.Hidden))
		}
		if ok && ev.State == aggregator.StateFailed && errors.Is(ev.Err, provider.ErrBlocked) {
			body += "\n" + styleDim.Render("Other sources continue. t retry this source now.")
		}
	}
	return a.chrome("results · "+r.query+" · sources", body, a.keyStrip(a.helpBudget(w)))
}
