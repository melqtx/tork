package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/health"
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/sources"
)

type sourceReading struct {
	probe      health.ProviderProbe
	at         time.Time
	invalid    bool
	unverified bool // saved settings differ from the last checked endpoint
}

type sourcesModel struct {
	catalog   *sourceCatalog
	editor    *sourceEditor
	editorSeq uint64
	removing  string
	win       listWindow
	saving    string
	checking  map[string]bool
	checkSeq  uint64
	checks    map[string]uint64
	readings  map[string]sourceReading
}

type sourceSavedMsg struct {
	id      string
	enabled bool
	check   bool
	err     error
}
type sourceCheckedMsg struct {
	generation uint64
	id         string
	reading    sourceReading
}

const sourceReadingTTL = 5 * time.Minute

func (a *App) sourceRows() int { return max(1, a.bodyHeight()-6) }

func (a *App) ensureSources() {
	if a.sources.checking == nil {
		a.sources.checking = make(map[string]bool)
	}
	if a.sources.checks == nil {
		a.sources.checks = make(map[string]uint64)
	}
	if a.sources.readings == nil {
		a.sources.readings = make(map[string]sourceReading)
	}
}

// Opening Sources checks enabled indexes without blocking navigation. Recent
// readings and in-flight checks are reused, so tabbing back does not start a
// second round of requests. Disabled sources are only checked explicitly.
func (a *App) checkEnabledSources(force bool) tea.Cmd {
	if a.cfg == nil {
		return nil
	}
	a.ensureSources()
	var cmds []tea.Cmd
	for _, s := range sources.List(a.cfg) {
		if !s.Config.Enabled || a.sources.checking[s.ID] {
			continue
		}
		if !force {
			if !a.sourceBlockedUntil(s).IsZero() {
				continue
			}
			// History may describe an older endpoint with the same source ID.
			// Only a check performed in this session can suppress a fresh probe.
			if r, ok := a.sources.readings[s.ID]; ok {
				age := time.Since(r.at)
				if age >= 0 && age < sourceReadingTTL {
					continue
				}
			}
		}
		cmds = append(cmds, a.checkSource(s))
	}
	return tea.Batch(cmds...)
}

func (a *App) sourceReading(s sources.Source) (sourceReading, bool) {
	if reading, ok := a.sources.readings[s.ID]; ok {
		return reading, true
	}
	if a.health != nil {
		snapshots := a.health.Log().Snapshots
		for i := len(snapshots) - 1; i >= 0; i-- {
			for _, p := range snapshots[i].Providers {
				if p.Name == s.RuntimeName {
					return sourceReading{probe: p, at: snapshots[i].At}, true
				}
			}
		}
	}
	return sourceReading{}, false
}

func (a *App) updateSources(msg tea.Msg) (tea.Model, tea.Cmd) {
	if a.sources.catalog != nil {
		return a.updateSourceCatalog(msg)
	}
	if a.sources.editor != nil {
		return a.updateSourceEditor(msg)
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return a, nil
	}
	a.ensureSources()
	list := sources.List(a.cfg)
	if a.sources.removing != "" {
		switch key.String() {
		case "esc", "n":
			if a.sources.saving == "" {
				a.sources.removing = ""
			}
		case "y", "enter":
			if a.sources.saving != "" {
				return a, nil
			}
			id, cfg := a.sources.removing, a.cfg
			a.sources.saving = id
			return a, func() tea.Msg { return sourceConfigSavedMsg{id: id, remove: true, err: cfg.RemoveCustomSource(id)} }
		}
		return a, nil
	}
	switch key.String() {
	case "a":
		if a.sources.saving == "" {
			return a, a.openSourceCatalog()
		}
	case "e", "x":
		if len(list) == 0 || a.sources.saving != "" {
			return a, nil
		}
		a.sources.win.clamp(len(list), a.sourceRows())
		selected := list[a.sources.win.cursor]
		if config.IsBuiltinSource(selected.ID) {
			return a, a.showError("built-in sources can be toggled here; edit mirror URLs in config.yaml")
		}
		if key.String() == "e" {
			return a, a.openSourceEditor(&selected)
		}
		a.sources.removing = selected.ID
	case "esc":
		return a, a.navigate(screenSearch)
	case "up", "k":
		a.sources.win.move(-1, len(list), a.sourceRows())
	case "down", "j":
		a.sources.win.move(1, len(list), a.sourceRows())
	case "pgup":
		a.sources.win.move(-a.sourceRows(), len(list), a.sourceRows())
	case "pgdown":
		a.sources.win.move(a.sourceRows(), len(list), a.sourceRows())
	case "g", "home":
		a.sources.win.home()
	case "G", "end":
		a.sources.win.end(len(list), a.sourceRows())
	case "enter", " ":
		if len(list) == 0 || a.sources.saving != "" {
			return a, nil
		}
		a.sources.win.clamp(len(list), a.sourceRows())
		s := list[a.sources.win.cursor]
		enabled := !s.Config.Enabled
		if enabled {
			if _, err := sources.Build(s.ID, s.Config, a.cfg.ProxyHTTPClient()); err != nil {
				return a, a.showError("cannot enable source: " + err.Error())
			}
		}
		a.sources.saving = s.ID
		cfg := a.cfg
		return a, func() tea.Msg {
			return sourceSavedMsg{id: s.ID, enabled: enabled, err: cfg.WriteProviderEnabled(s.ID, enabled)}
		}
	case "t":
		if len(list) == 0 {
			return a, nil
		}
		a.sources.win.clamp(len(list), a.sourceRows())
		s := list[a.sources.win.cursor]
		if a.sources.checking[s.ID] {
			return a, nil
		}
		return a, a.checkSource(s)
	case "r":
		return a, a.checkEnabledSources(true)
	}
	return a, nil
}

// checkSource snapshots provider settings before starting work off the UI loop.
func (a *App) checkSource(s sources.Source) tea.Cmd {
	a.ensureSources()
	if a.sources.checking[s.ID] {
		return nil
	}
	p, err := sources.Build(s.ID, s.Config, a.cfg.ProxyHTTPClient())
	if err != nil {
		a.sources.readings[s.ID] = sourceReading{invalid: true, at: time.Now()}
		return a.showError("cannot check source: " + err.Error())
	}
	a.sources.checking[s.ID] = true
	a.sources.checkSeq++
	generation := a.sources.checkSeq
	a.sources.checks[s.ID] = generation
	timeout := a.cfg.SearchTimeout()
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return func() tea.Msg {
		probes := health.ProbeProviders(context.Background(), []provider.Provider{p}, timeout)
		return sourceCheckedMsg{generation: generation, id: s.ID, reading: sourceReading{probe: probes[0], at: time.Now()}}
	}
}

func (a *App) onSourceSaved(msg sourceSavedMsg) tea.Cmd {
	if a.sources.saving != msg.id {
		return nil
	}
	a.sources.saving = ""
	if msg.err != nil {
		return a.showError("source change was not saved: " + msg.err.Error())
	}
	p := a.cfg.Providers[msg.id]
	p.Enabled = msg.enabled
	a.cfg.Providers[msg.id] = p
	// Keep the current search's provider objects, including cached mirrors and
	// lazy resolvers. Only the next search uses this new list.
	a.nextAgg = a.agg.WithProviders(sources.Enabled(a.cfg))
	var check tea.Cmd
	if msg.enabled {
		for _, s := range sources.List(a.cfg) {
			if s.ID == msg.id {
				check = a.checkSource(s)
				break
			}
		}
	}
	if msg.check {
		if c := a.sources.catalog; c != nil {
			c.filter.SetValue("")
			c.win.clamp(len(a.catalogSources())+1, a.catalogRows())
		}
		for i, s := range sources.List(a.cfg) {
			if s.ID == msg.id {
				a.sources.win.cursor = i
				return tea.Batch(check, a.showToast(s.Label+" added · checking… · choose another or esc to finish", toastOK, toastQuick))
			}
		}
	}
	return tea.Batch(check, a.showToast("source saved · applies to next search", toastOK, toastQuick))
}

func (a *App) onSourceChecked(msg sourceCheckedMsg) {
	a.ensureSources()
	if !a.sources.checking[msg.id] || a.sources.checks[msg.id] != msg.generation {
		return
	}
	delete(a.sources.checking, msg.id)
	// Never retain a raw network error: URL query strings may contain API keys.
	msg.reading.probe.Err = sourceFailure(msg.reading.probe)
	a.sources.readings[msg.id] = msg.reading
	// An explicit probe bypasses search cooldowns. Its result controls the next
	// search pipeline, without modifying saved choices or current search rows.
	for _, s := range sources.List(a.cfg) {
		if s.ID != msg.id {
			continue
		}
		agg := a.agg
		if a.nextAgg != nil {
			agg = a.nextAgg
		}
		if msg.reading.probe.Blocked {
			agg.RecordBlocked(s.RuntimeName)
		} else {
			agg.ClearBlocked(s.RuntimeName)
		}
		break
	}
}

func (a *App) sourceBlockedUntil(s sources.Source) time.Time {
	if a.nextAgg != nil {
		return a.nextAgg.BlockedUntil(s.RuntimeName)
	}
	return a.agg.BlockedUntil(s.RuntimeName)
}

func sourceFailure(p health.ProviderProbe) string {
	if p.OK {
		return ""
	}
	if p.Blocked {
		return "blocked"
	}
	switch p.Err {
	case "timed out", "unreachable (dns)", "unreachable", "bad response", "no response", "blocked", "authentication failed", "not an RSS/Torznab feed", "unsupported torrent links", "feed rejected the search", "unrecognized search response":
		return p.Err
	default:
		return "check failed"
	}
}

func (a *App) sourceStatus(s sources.Source) string {
	if a.sources.checking[s.ID] {
		return "checking…"
	}
	if _, err := sources.Build(s.ID, s.Config, a.cfg.ProxyHTTPClient()); err != nil {
		return "configuration invalid"
	}
	if until := a.sourceBlockedUntil(s); !until.IsZero() {
		return blockedRetryLabel(until)
	}
	r, ok := a.sourceReading(s)
	if !ok {
		if !s.Config.Enabled {
			return "disabled"
		}
		return "not checked"
	}
	if r.invalid {
		return "configuration invalid"
	}
	if r.unverified {
		if !s.Config.Enabled {
			return "disabled"
		}
		return "not checked"
	}
	if r.probe.OK {
		return fmt.Sprintf("working · %d results · %dms", r.probe.Results, r.probe.LatencyMS)
	}
	if r.probe.Results > 0 {
		return fmt.Sprintf("%s · %d usable results", sourceFailure(r.probe), r.probe.Results)
	}
	return sourceFailure(r.probe)
}

func (a *App) viewSources() string {
	if a.sources.catalog != nil {
		return a.viewSourceCatalog()
	}
	if a.sources.editor != nil {
		return a.viewSourceEditor()
	}
	list := sources.List(a.cfg)
	width := a.contentWidth()
	lines := []string{styleTitle.Render("Search sources"), styleDim.Render("Choose an index below, or press a to add more sources."), ""}
	if len(list) == 0 {
		lines = append(lines, styleDim.Render("No sources configured."))
	} else {
		rows := renderWindow(&a.sources.win, len(list), a.sourceRows(), width, func(i int, selected bool) string {
			s := list[i]
			state := "off"
			if s.Config.Enabled {
				state = "on "
			}
			if a.sources.saving == s.ID {
				state = "…  "
			}
			label := padRight(s.Label, 23)
			if width < 65 {
				labelWidth := max(8, min(20, width-18))
				label = padRight(truncate(s.Label, labelWidth), labelWidth)
			}
			text := a.sourceStatus(s)
			status := styleDim.Render(text)
			if strings.HasPrefix(text, "working ·") {
				status = styleOK.Render(text)
			} else if !a.sources.checking[s.ID] && text != "not checked" && text != "disabled" {
				status = styleHealthMid.Render(text)
			}
			return fmt.Sprintf("[%s] %s %s", state, label, status)
		})
		lines = append(lines, rows, "")
		selected := list[a.sources.win.cursor]
		detail := a.sourceStatus(selected)
		if r, ok := a.sourceReading(selected); ok && !a.sources.checking[selected.ID] && !r.unverified {
			detail += " · checked " + r.at.Local().Format("Jan 2 15:04")
		}
		description := sources.Description(selected)
		if strings.HasPrefix(detail, "blocked") {
			description = "Site protection blocked access. Other sources continue. t retry now · enter enable / disable"
		}
		lines = append(lines, styleDim.Render(detail), styleDim.Render(description))
	}
	if a.sources.removing != "" {
		label := provider.SanitizeDisplayText(a.sources.removing, 64)
		lines = []string{styleTitle.Render("Remove custom source?"), "", label, "", styleDim.Render("Existing search results remain usable. Press y to remove, esc to cancel.")}
	}
	return a.chrome("sources", strings.Join(lines, "\n"), a.keyStrip(a.helpBudget(width)))
}
