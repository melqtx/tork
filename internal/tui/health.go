package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/melqtx/tork/internal/health"
)

// compassModel is the health screen: how the source fleet is holding up, and
// whether the swarms behind the library still have anyone in them. It reads a
// snapshot of the trends taken on entry (and after a manual re-check), because
// history only changes when a check runs - never on the stats tick.
type compassModel struct {
	providers []health.ProviderTrend
	swarms    []health.SwarmTrend
	sourcesAt time.Time
	libraryAt time.Time
	from      screen // where H was pressed, so esc goes back there
	probing   bool
	win       listWindow
}

// openHealth loads trends and shows the screen. It never runs a probe: an
// empty history is a legitimate state ("no check yet"), and blocking the UI on
// the network to fill it would be the wrong trade.
func (a *App) openHealth() tea.Cmd {
	from := a.screen
	a.compass = compassModel{from: from}
	a.screen = screenHealth
	a.refreshCompass()
	a.compass.win.cursor = selectableIdx(a.compassBody(), 0, 1) // never open on a heading
	return nil
}

func (a *App) refreshCompass() {
	if a.health == nil {
		return
	}
	a.compass.sourcesAt = time.Time{}
	a.compass.libraryAt = time.Time{}
	log := a.health.Log()
	a.compass.providers = log.ProviderTrends()
	a.compass.swarms = log.SwarmTrends()
	if len(a.compass.providers) > 0 {
		a.compass.sourcesAt = a.compass.providers[0].At
	}
	if len(a.compass.swarms) > 0 {
		a.compass.libraryAt = a.compass.swarms[0].At
	}
}

// runHealthCheck probes every provider and samples the live swarms, recording
// the result as a manual-kind snapshot: it was asked for, so it must not stand
// in for the day's scheduled reading.
func (a *App) runHealthCheck() tea.Cmd {
	return func() (msg tea.Msg) {
		defer guard(&msg, func(r any) tea.Msg {
			return healthDoneMsg{err: fmt.Errorf("health check panicked: %v", r)}
		})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, err := health.Run(ctx, health.KindManual, a.agg.Providers(), a.eng.Snapshots(), a.cfg.SearchTimeout(), a.health)
		return healthDoneMsg{err: err}
	}
}

func (a *App) onHealthDone(msg healthDoneMsg) tea.Cmd {
	a.compass.probing = false
	if msg.err != nil {
		a.errText = "health check failed: " + msg.err.Error()
		return clearErrCmd()
	}
	a.refreshCompass()
	return nil
}

func (a *App) updateHealth(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return a, nil
	}
	c := &a.compass
	rows := a.compassRows()
	switch key.String() {
	case "q":
		return a, tea.Quit
	case "esc":
		a.screen = c.from
		return a, nil
	case "up", "k":
		a.moveCompass(-1, rows)
	case "down", "j":
		a.moveCompass(1, rows)
	case "pgup":
		a.moveCompass(-rows, rows)
	case "pgdown":
		a.moveCompass(rows, rows)
	case "g", "home":
		c.win.home()
		a.moveCompass(0, rows)
	case "G", "end":
		c.win.end(a.compassLines(), rows)
		a.moveCompass(0, rows)
	case "r":
		if !c.probing && a.health != nil {
			c.probing = true
			return a, a.runHealthCheck()
		}
	}
	return a, nil
}

// moveCompass shifts the cursor by delta and then slides it off any section
// heading it landed on, since headings are labels rather than choices. A delta
// of 0 just normalizes wherever the cursor currently is.
func (a *App) moveCompass(delta, rows int) {
	lines := a.compassBody()
	c := &a.compass
	c.win.move(delta, len(lines), rows)
	step := 1
	if delta < 0 {
		step = -1 // keep sliding the way the user was already heading
	}
	c.win.cursor = selectableIdx(lines, c.win.cursor, step)
	c.win.clamp(len(lines), rows)
}

// selectableIdx finds the nearest selectable line starting at i, searching in
// direction step and falling back to the opposite direction at the list edges.
// When nothing is selectable it returns i unchanged; headings and placeholders
// render the same either way, so no phantom highlight appears.
func selectableIdx(lines []compassLine, i, step int) int {
	if len(lines) == 0 {
		return 0
	}
	if step == 0 {
		step = 1
	}
	for j := i; j >= 0 && j < len(lines); j += step {
		if lines[j].selectable() {
			return j
		}
	}
	for j := i; j >= 0 && j < len(lines); j -= step {
		if lines[j].selectable() {
			return j
		}
	}
	return i
}

// compassLine is one rendered row of the scrolling body: either a section
// heading or an entry under it. Flattening both into one list keeps a single
// cursor and one scroll window for the whole screen.
type compassLine struct {
	heading  string
	provider *health.ProviderTrend
	swarm    *health.SwarmTrend
	note     string // an empty-section placeholder
}

// selectable reports whether the cursor may rest here. Headings are labels and
// notes are placeholders; only real entries are choices.
func (l compassLine) selectable() bool { return l.provider != nil || l.swarm != nil }

func (a *App) compassBody() []compassLine {
	c := &a.compass
	lines := []compassLine{{heading: "sources"}}
	if len(c.providers) == 0 {
		lines = append(lines, compassLine{note: "no check recorded yet - press r to run one"})
	}
	for i := range c.providers {
		lines = append(lines, compassLine{provider: &c.providers[i]})
	}
	lines = append(lines, compassLine{heading: "library"})
	if len(c.swarms) == 0 {
		lines = append(lines, compassLine{note: "no torrents were active at the last check"})
	}
	for i := range c.swarms {
		lines = append(lines, compassLine{swarm: &c.swarms[i]})
	}
	return lines
}

func (a *App) compassLines() int { return len(a.compassBody()) }

// compassRows is the height of the scrolling body: everything but the status
// line above it.
func (a *App) compassRows() int { return max(1, a.bodyHeight()-1) }

func (a *App) viewHealth() string {
	width := a.contentWidth()
	lines := a.compassBody()

	var b strings.Builder
	b.WriteString(a.compassStatus(width) + "\n")
	b.WriteString(renderWindow(&a.compass.win, len(lines), a.compassRows(), width, func(i int, selected bool) string {
		l := lines[i]
		switch {
		case l.heading != "":
			return styleFaint.Render(l.heading)
		case l.note != "":
			return styleDim.Render(l.note)
		case l.provider != nil:
			return a.compassProviderRow(*l.provider, width, selected)
		default:
			return a.compassSwarmRow(*l.swarm, width, selected)
		}
	}))

	help := hints(hint("↑↓", "move"), hint("r", "re-check"), hint("esc", "back"), hint("q", "quit"))
	if a.compass.probing {
		help = styleDim.Render("checking providers and swarms…")
	}
	return a.chrome("health", b.String(), help)
}

// compassStatus is the one-line summary above the fleet: how many sources are
// answering, how many downloads are starving, and how stale the reading is.
func (a *App) compassStatus(width int) string {
	c := &a.compass
	if c.sourcesAt.IsZero() && c.libraryAt.IsZero() {
		return styleDim.Render("no health check recorded yet")
	}
	up := 0
	for _, p := range c.providers {
		if p.OK {
			up++
		}
	}
	dying := 0
	for _, s := range c.swarms {
		if s.Dying {
			dying++
		}
	}

	head := styleOK.Render(fmt.Sprintf("%d/%d sources up", up, len(c.providers)))
	if up < len(c.providers) {
		head = styleHealthMid.Render(fmt.Sprintf("%d/%d sources up", up, len(c.providers)))
	}
	if up == 0 && len(c.providers) > 0 {
		head = styleErr.Render("no sources answering")
	}
	line := head
	if dying > 0 {
		line += styleFaint.Render("  ·  ") + styleHealthBad.Render(fmt.Sprintf("%d dying", dying))
	}
	if !c.sourcesAt.IsZero() {
		line += styleFaint.Render("  ·  ") + styleDim.Render("sources checked "+humanAgo(time.Since(c.sourcesAt))+" ago")
	}
	if !c.libraryAt.IsZero() {
		line += styleFaint.Render("  ·  ") + styleDim.Render("library sampled "+humanAgo(time.Since(c.libraryAt))+" ago")
	}
	// Only advertise a cadence there actually is one; otherwise `r` is the only
	// thing that ever refreshes this screen.
	if a.cfg.Health.Enabled {
		line += styleFaint.Render(fmt.Sprintf("  ·  every %s", humanAgo(a.cfg.Health.Interval())))
	} else {
		line += styleFaint.Render("  ·  ") + styleDim.Render("automatic checks off")
	}
	return truncate(line, width)
}

// compassProviderRow: dot, name, latency, streak, result count.
func (a *App) compassProviderRow(p health.ProviderTrend, width int, selected bool) string {
	dot := providerDot(p, selected)
	name := padRight(truncate(p.Name, 14), 14)

	detail := ""
	switch {
	case p.OK:
		detail = fmt.Sprintf("%-8s %s", fmt.Sprintf("%dms", p.LatencyMS), plural(p.Results, "result"))
	case p.Blocked:
		detail = "blocked by site protection"
	default:
		detail = p.Err
	}
	if !selected && !p.OK {
		detail = styleFaint.Render(detail)
	}

	line := fmt.Sprintf("%s %s %s", dot, name, detail)
	if s := streakLabel(p.Streak, selected); s != "" {
		line = padRight(line, max(1, width-14)) + s
	}
	return truncate(line, width-1)
}

// streakLabel reads the run of consecutive agreeing checks. A one-off is not a
// streak worth naming, so it stays blank until there are two.
func streakLabel(streak int, plain bool) string {
	switch {
	case streak >= 2:
		return colorize(plain, styleFaint, fmt.Sprintf("up %d", streak))
	case streak <= -2:
		return colorize(plain, styleHealthBad, fmt.Sprintf("down %d", -streak))
	}
	return ""
}

func providerDot(p health.ProviderTrend, plain bool) string {
	if plain {
		return "●"
	}
	switch {
	case p.OK:
		return styleHealthGood.Render("●")
	case p.Blocked:
		return styleHealthMid.Render("●")
	}
	return styleHealthBad.Render("●")
}

// compassSwarmRow: dot, name, seeders with trend arrow, peers, dying badge.
func (a *App) compassSwarmRow(s health.SwarmTrend, width int, selected bool) string {
	dot := healthDot(s.Seeders)
	if selected {
		dot = "●"
		if s.Seeders == 0 {
			dot = "○"
		}
	}

	badge := ""
	switch {
	case s.Dying:
		badge = colorize(selected, styleHealthBad, "dying")
	case s.Done:
		badge = colorize(selected, styleFaint, "seeding")
	}

	nameW := max(10, width-34)
	name := padRight(truncate(s.Name, nameW), nameW)
	seeds := fmt.Sprintf("S%d%s", s.Seeders, deltaArrow(s.Delta, selected))
	stats := fmt.Sprintf("%-10s %s", seeds, padRight(plural(s.Peers, "peer"), 10))
	if !selected {
		stats = styleDim.Render(stats)
	}
	return truncate(fmt.Sprintf("%s %s %s %s", dot, name, stats, badge), width-1)
}

// deltaArrow shows which way the swarm moved since the previous reading. The
// arrow carries the sign, so no "+" is needed.
func deltaArrow(delta int, plain bool) string {
	switch {
	case delta > 0:
		return colorize(plain, styleHealthGood, "↑")
	case delta < 0:
		return colorize(plain, styleHealthBad, "↓")
	}
	return " "
}

// plural renders "1 peer" but "2 peers".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// humanAgo renders a duration the way a person would say it out loud.
func humanAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "moments"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
