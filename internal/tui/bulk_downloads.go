package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/engine"
)

type bulkDownloads struct {
	items                 []downloadItem
	pause, cancelled      bool
	next, changed, failed int
	lastErr               error
}

func bulkEligible(it downloadItem, pause bool) bool {
	if !pause {
		return it.State == engine.StatePaused
	}
	return it.Live && (it.State == engine.StateDownloading || it.State == engine.StateFetchingMeta || it.State == engine.StateSeeding)
}

func (a *App) startBulkDownloads(pause bool) tea.Cmd {
	if a.pendingAdds > 0 || a.downloads.savingOrganization {
		return a.showError("wait for the pending operation to finish")
	}
	p := &bulkDownloads{pause: pause}
	for _, item := range a.downloadItems() {
		if a.st.Find(item.Magnet) != nil && bulkEligible(item, pause) {
			p.items = append(p.items, item)
		}
	}
	a.downloads.bulk = p
	return a.nextBulkDownload()
}

func (a *App) nextBulkDownload() tea.Cmd {
	p := a.downloads.bulk
	a.downloads.snaps = a.eng.Snapshots()
	for !p.cancelled && p.next < len(p.items) {
		it, ok := a.downloadItemByMagnet(a.buildDownloadItems(), p.items[p.next].Magnet)
		if ok && bulkEligible(it, p.pause) {
			return a.bulkTransferCmd(it, p.pause)
		}
		p.next++
	}
	a.downloads.bulk = nil
	a.refreshDownloadItems()
	verb := "resumed"
	if p.pause {
		verb = "paused"
	}
	text := fmt.Sprintf("%s %d downloads", verb, p.changed)
	if p.failed > 0 {
		return tea.Batch(a.showError(fmt.Sprintf("%s · %d failed: %v", text, p.failed, p.lastErr)), a.ensureTick())
	}
	return tea.Batch(a.syncCompletedToState(), a.showToast(text, toastOK, toastQuick), a.ensureTick())
}

func (a *App) onBulkTransfer(msg bulkTransferMsg) tea.Cmd {
	p := a.downloads.bulk
	if p == nil {
		return nil
	}
	if msg.err != nil {
		p.failed++
		p.lastErr = msg.err
	} else if e := a.st.Find(msg.entry.Magnet); e != nil {
		*e = msg.entry
		p.changed++
	}
	p.next++
	a.downloads.snaps = a.eng.Snapshots()
	a.refreshDownloadItems()
	if err := a.st.Save(a.cfg.StatePath()); err != nil {
		a.downloads.bulk = nil
		return tea.Batch(a.showError("save failed: "+err.Error()), a.ensureTick())
	}
	return a.nextBulkDownload()
}
