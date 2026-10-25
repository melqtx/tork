package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/health"
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/sources"
)

type sourceEditor struct {
	id             string             // empty for a new source; edits retain the original stable ID
	inputs         [3]textinput.Model // name, endpoint, API key
	focus          int                // name, type, endpoint, key
	typ            string
	generation     uint64
	saveAfterCheck bool
	checking       bool
	cancel         context.CancelFunc
	reading        *sourceReading
}

type sourceDraftCheckedMsg struct {
	generation uint64
	reading    sourceReading
}
type sourceConfigSavedMsg struct {
	id     string
	cfg    config.ProviderConfig
	remove bool
	err    error
}

func (a *App) openSourceEditor(s *sources.Source) tea.Cmd {
	a.sources.editorSeq++
	e := &sourceEditor{typ: "torznab", generation: a.sources.editorSeq}
	for i := range e.inputs {
		in := textinput.New()
		in.CharLimit = 4096
		in.Prompt = "❯ "
		in.PromptStyle, in.TextStyle = styleBrand, styleFg
		e.inputs[i] = in
	}
	e.inputs[0].CharLimit = 80
	e.inputs[0].Placeholder = "e.g. My Linux index"
	e.inputs[1].Placeholder = "HTTP(S) API endpoint"
	e.inputs[2].Placeholder = "optional"
	e.inputs[2].EchoMode = textinput.EchoPassword
	e.inputs[2].EchoCharacter = '•'
	if s != nil {
		if s.Config.Type != "rss" && s.Config.Type != "torznab" {
			return a.showError("edit this source's adapter settings in config.yaml")
		}
		e.id, e.typ = s.ID, s.Config.Type
		e.inputs[0].SetValue(s.Label)
		e.inputs[1].SetValue(strings.ReplaceAll(s.Config.SearchURL, "%s", "{query}"))
		e.inputs[2].SetValue(s.Config.APIKey)
		if e.typ == "torznab" {
			if _, normalized, err := sources.CustomConfig(s.Label, e.typ, s.Config.SearchURL, s.Config.APIKey); err == nil {
				e.inputs[1].SetValue(normalized.SearchURL)
				e.inputs[2].SetValue(normalized.APIKey)
			}
		}
	}
	a.sources.editor = e
	return e.inputs[0].Focus()
}

func (a *App) closeSourceEditor() {
	if e := a.sources.editor; e != nil && e.cancel != nil {
		e.cancel()
	}
	a.sources.editor = nil
}

func (e *sourceEditor) inputIndex() int {
	switch e.focus {
	case 0:
		return 0
	case 2:
		return 1
	case 3:
		return 2
	}
	return -1
}

func (a *App) invalidateSourceDraft() {
	e := a.sources.editor
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	a.sources.editorSeq++
	e.saveAfterCheck = false
	e.generation, e.reading, e.checking = a.sources.editorSeq, nil, false
}

func (a *App) sourceDraft() (string, config.ProviderConfig, error) {
	e := a.sources.editor
	id, p, err := sources.CustomConfig(e.inputs[0].Value(), e.typ, e.inputs[1].Value(), e.inputs[2].Value())
	if err != nil {
		return "", p, err
	}
	if e.id != "" {
		id = e.id
	} else if _, exists := a.cfg.Providers[id]; exists {
		return "", p, fmt.Errorf("a source with this name already exists")
	}
	return id, p, nil
}

func (a *App) updateSourceEditor(msg tea.Msg) (tea.Model, tea.Cmd) {
	e := a.sources.editor
	if a.sources.saving != "" {
		return a, nil
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			a.closeSourceEditor()
			return a, nil
		case "tab", "shift+tab", "up", "down":
			delta := 1
			if key.String() == "shift+tab" || key.String() == "up" {
				delta = -1
			}
			for i := range e.inputs {
				e.inputs[i].Blur()
			}
			e.focus = (e.focus + delta + 4) % 4
			if i := e.inputIndex(); i >= 0 {
				return a, e.inputs[i].Focus()
			}
			return a, nil
		case "left", "right", " ":
			if e.focus == 1 {
				if e.typ == "torznab" {
					e.typ = "rss"
				} else {
					e.typ = "torznab"
				}
				a.invalidateSourceDraft()
				return a, nil
			}
		case "ctrl+t":
			if e.checking {
				return a, nil
			}
			id, p, err := a.sourceDraft()
			if err != nil {
				return a, a.showError(err.Error())
			}
			// Move a copied Torznab URL's key to the masked field before rendering.
			e.inputs[1].SetValue(p.SearchURL)
			e.inputs[2].SetValue(p.APIKey)
			endpoint, err := sources.Build(id, p, a.cfg.ProxyHTTPClient())
			if err != nil {
				return a, a.showError(err.Error())
			}
			a.invalidateSourceDraft()
			e.checking = true
			ctx, cancel := context.WithCancel(context.Background())
			e.cancel = cancel
			generation, timeout := e.generation, a.cfg.SearchTimeout()
			if timeout <= 0 {
				timeout = 15 * time.Second
			}
			return a, func() tea.Msg {
				defer cancel()
				probes := health.ProbeProviders(ctx, []provider.Provider{endpoint}, timeout)
				return sourceDraftCheckedMsg{generation: generation, reading: sourceReading{probe: probes[0], at: time.Now()}}
			}
		case "ctrl+s":
			if e.reading == nil && !e.checking {
				model, cmd := a.updateSourceEditor(tea.KeyMsg{Type: tea.KeyCtrlT})
				e.saveAfterCheck = e.checking
				return model, cmd
			}
			if e.checking {
				return a, a.showError("wait for the check or edit a field to cancel it")
			}
			id, p, err := a.sourceDraft()
			if err != nil {
				return a, a.showError(err.Error())
			}
			p.Enabled = e.reading != nil && e.reading.probe.OK
			a.sources.saving = id
			cfg, replace := a.cfg, e.id != ""
			return a, func() tea.Msg {
				return sourceConfigSavedMsg{id: id, cfg: p, err: cfg.WriteCustomSource(id, p, replace)}
			}
		case "enter":
			// Keep Enter as field navigation; Ctrl+S starts the explicit save flow.
			return a.updateSourceEditor(tea.KeyMsg{Type: tea.KeyTab})
		}
	}
	if i := e.inputIndex(); i >= 0 {
		before := e.inputs[i].Value()
		var cmd tea.Cmd
		e.inputs[i], cmd = e.inputs[i].Update(msg)
		if e.inputs[i].Value() != before {
			a.invalidateSourceDraft()
		}
		return a, cmd
	}
	return a, nil
}

func (a *App) onSourceDraftChecked(msg sourceDraftCheckedMsg) tea.Cmd {
	e := a.sources.editor
	if e == nil || e.generation != msg.generation {
		return nil
	}
	e.checking, e.cancel = false, nil
	msg.reading.probe.Err = sourceFailure(msg.reading.probe)
	e.reading = &msg.reading
	shouldSave := e.saveAfterCheck
	e.saveAfterCheck = false
	if shouldSave && e.reading.probe.OK {
		_, cmd := a.updateSourceEditor(tea.KeyMsg{Type: tea.KeyCtrlS})
		return cmd
	}
	return nil
}

func (a *App) onSourceConfigSaved(msg sourceConfigSavedMsg) tea.Cmd {
	if a.sources.saving != msg.id {
		return nil
	}
	a.sources.saving = ""
	if msg.err != nil {
		return a.showError("source change was not saved: " + msg.err.Error())
	}
	delete(a.sources.checking, msg.id)
	delete(a.sources.checks, msg.id)
	if msg.remove {
		delete(a.cfg.Providers, msg.id)
		delete(a.sources.readings, msg.id)
		delete(a.sources.checking, msg.id)
		a.sources.removing = ""
	} else {
		a.cfg.Providers[msg.id] = msg.cfg
		a.ensureSources()
		a.sources.readings[msg.id] = sourceReading{unverified: true}
		if e := a.sources.editor; e != nil && e.reading != nil {
			a.sources.readings[msg.id] = *e.reading
		}
		a.closeSourceEditor()
	}
	a.nextAgg = a.agg.WithProviders(sources.Enabled(a.cfg))
	for i, s := range sources.List(a.cfg) {
		if s.ID == msg.id {
			a.sources.win.cursor = i
			break
		}
	}
	text := "source saved disabled · test it before enabling"
	if msg.remove {
		text = "source removed · applies to next search"
	} else if msg.cfg.Enabled {
		text = "source saved and enabled · applies to next search"
	}
	return a.showToast(text, toastOK, toastLong)
}

func (a *App) viewSourceEditor() string {
	e := a.sources.editor
	width := max(1, a.contentWidth()-4)
	for i := range e.inputs {
		e.inputs[i].Width = width
	}
	kind := "Torznab API"
	if e.typ == "rss" {
		kind = "RSS search feed"
	}
	marker := "  "
	if e.focus == 1 {
		marker = "→ "
	}
	lines := []string{styleTitle.Render("Custom search source"), styleDim.Render("Public magnet/infohash feeds. Private trackers and .torrent-only feeds are unsupported."), "", "Name", e.inputs[0].View(), "", "Type  " + marker + kind + "  ←→", "", "Endpoint URL", e.inputs[1].View(), "", "API key (Torznab, optional)", e.inputs[2].View(), ""}
	if e.typ == "rss" {
		lines = append(lines, styleDim.Render("Put {query} in the RSS search URL."))
	}
	status := "Save tests this source, then enables it if the check succeeds."
	if e.checking {
		status = "Checking with the 1080p query…"
	} else if e.reading != nil {
		if e.reading.probe.OK {
			status = fmt.Sprintf("Responded · %d usable results. Save enables this source.", e.reading.probe.Results)
		} else {
			status = sourceFailure(e.reading.probe) + ". Save keeps this source disabled."
		}
	}
	if a.sources.saving != "" {
		status = "Saving…"
	}
	lines = append(lines, styleDim.Render(status))
	// Keep the active field visible in short terminals, plus status at the end.
	if a.bodyHeight() < len(lines) {
		start := max(0, min(len(lines)-max(1, a.bodyHeight()-2), []int{2, 5, 7, 10}[e.focus]))
		end := min(len(lines)-1, start+max(1, a.bodyHeight()-2))
		lines = append(append([]string{}, lines[start:end]...), "", styleDim.Render(status))
	}
	saveLabel := "test & save"
	if e.reading != nil {
		saveLabel = "save disabled"
		if e.reading.probe.OK {
			saveLabel = "save & enable"
		}
	}
	return a.chrome("sources", strings.Join(lines, "\n"), hints(hint("^t", "test"), hint("^s", saveLabel), hint("tab", "next field"), hint("esc", "cancel")))
}
