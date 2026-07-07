// Package engine wraps anacrolix/torrent: adding magnets, progress
// snapshots, seeding/pause toggles, and resume via bolt piece completion.
package engine

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	analog "github.com/anacrolix/log"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"

	"github.com/melqtx/tork/internal/config"
)

type TorrentState int

const (
	StateFetchingMeta TorrentState = iota
	StatePreviewing
	StateDownloading
	StateSeeding
	StateDone
	StatePaused
)

func (s TorrentState) String() string {
	switch s {
	case StateFetchingMeta:
		return "fetching metadata"
	case StatePreviewing:
		return "previewing"
	case StateDownloading:
		return "downloading"
	case StateSeeding:
		return "seeding"
	case StateDone:
		return "done"
	case StatePaused:
		return "paused"
	}
	return "unknown"
}

// FileInfo describes one file inside a torrent, for the preview screen.
type FileInfo struct {
	Index  int
	Path   string
	Length int64
}

type Snapshot struct {
	Hash           metainfo.Hash
	Name           string
	Magnet         string
	BytesCompleted int64
	Length         int64 // 0 until metadata arrives
	SpeedBps       float64
	ETA            time.Duration // 0 when unknown or done
	PeersActive    int
	PeersTotal     int
	State          TorrentState
}

func (s Snapshot) Progress() float64 {
	if s.Length == 0 {
		return 0
	}
	return float64(s.BytesCompleted) / float64(s.Length)
}

type item struct {
	t             *torrent.Torrent // nil while paused
	magnet        string
	name          string // last known name, survives pause
	length        int64  // last known length
	done          int64  // last known completed bytes
	paused        bool
	preview       bool // fetched metadata only; awaiting StartDownload
	seeding       bool
	excluded      []int // file indices not to download
	selectedBytes int64 // sum of non-excluded file lengths (0 until applied)
	samples       ring
}

type Engine struct {
	client  *torrent.Client
	cfg     *config.Config
	mu      sync.Mutex
	items   map[metainfo.Hash]*item
	pcClose func() // bolt piece-completion closer
}

func New(cfg *config.Config) (*Engine, error) {
	dbPath := filepath.Join(cfg.PieceCompletionDir(), ".torrent.bolt.db")
	pc, err := storage.NewBoltPieceCompletion(cfg.PieceCompletionDir())
	if err != nil {
		if strings.Contains(err.Error(), "timeout") {
			// a lock timeout means another instance holds the data dir - do
			// NOT reset it, that would corrupt the running download.
			return nil, fmt.Errorf("piece database is locked - another tork is already running "+
				"(find it with `pgrep -fl tork`, then quit or kill it). db: %s", dbPath)
		}
		// otherwise the db is corrupt: preserve it and start fresh (downloads
		// simply re-verify against data on disk).
		_ = os.Rename(dbPath, dbPath+".corrupt")
		pc, err = storage.NewBoltPieceCompletion(cfg.PieceCompletionDir())
		if err != nil {
			return nil, fmt.Errorf("open piece completion db: %w", err)
		}
		fmt.Fprintf(os.Stderr, "tork: piece database was corrupt; reset it (downloads will re-verify). old file: %s.corrupt\n", dbPath)
	}

	cc := torrent.NewDefaultClientConfig()
	cc.DataDir = cfg.DownloadDir
	cc.Seed = cfg.SeedAfterComplete
	cc.ListenPort = cfg.ListenPort
	cc.DefaultStorage = storage.NewFileWithCompletion(cfg.DownloadDir, pc)
	if cfg.MaxConnections > 0 {
		cc.EstablishedConnsPerTorrent = cfg.MaxConnections
	}
	// the TUI owns the terminal; drop anacrolix's stderr logging entirely
	silent := analog.NewLogger("tork")
	silent.Handlers = []analog.Handler{analog.DiscardHandler}
	cc.Logger = silent

	client, err := torrent.NewClient(cc)
	if err != nil {
		pc.Close()
		return nil, fmt.Errorf("start torrent client: %w", err)
	}
	return &Engine{
		client:  client,
		cfg:     cfg,
		items:   make(map[metainfo.Hash]*item),
		pcClose: func() { pc.Close() },
	}, nil
}

// Add starts downloading a magnet, skipping the given file indices (nil =
// everything). It never blocks on metadata: the torrent is registered
// immediately and downloading begins once info arrives. Adding an existing
// torrent is a no-op returning its hash.
func (e *Engine) Add(magnet string, excluded []int) (metainfo.Hash, error) {
	t, err := e.client.AddMagnet(magnet)
	if err != nil {
		return metainfo.Hash{}, err
	}
	h := t.InfoHash()

	e.mu.Lock()
	if existing, ok := e.items[h]; ok {
		if existing.paused { // re-adding a paused torrent resumes it
			existing.t = t
			existing.paused = false
			existing.preview = false
			e.startWhenReady(t, h)
		}
		e.mu.Unlock()
		return h, nil
	}
	e.items[h] = &item{t: t, magnet: magnet, seeding: e.cfg.SeedAfterComplete, excluded: excluded}
	e.mu.Unlock()

	e.startWhenReady(t, h)
	return h, nil
}

// torrentClient fetches .torrent files; a plain timeout is enough since these
// are small and served by official mirrors.
var torrentClient = &http.Client{Timeout: 30 * time.Second}

// maxTorrentBytes caps a fetched .torrent file. Even a large multi-GiB image
// has a metainfo of a few MiB at most.
const maxTorrentBytes = 16 << 20

// AddTorrentURL fetches an official .torrent file over HTTP and starts
// downloading it, exactly like Add does for a magnet. It derives a magnet URI
// from the metainfo and returns it (alongside the image name), so state.json,
// resume, and seeding all work unchanged - everything downstream keys off the
// magnet string.
func (e *Engine) AddTorrentURL(ctx context.Context, url string) (h metainfo.Hash, name, magnet string, err error) {
	mi, err := fetchMetaInfo(ctx, url)
	if err != nil {
		return metainfo.Hash{}, "", "", fmt.Errorf("fetch torrent: %w", err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return metainfo.Hash{}, "", "", fmt.Errorf("read torrent: %w", err)
	}

	t, err := e.client.AddTorrent(mi)
	if err != nil {
		return metainfo.Hash{}, "", "", err
	}
	h = t.InfoHash()
	name = info.Name
	magnet = mi.Magnet(&h, &info).String()

	e.mu.Lock()
	if existing, ok := e.items[h]; ok {
		if existing.paused { // re-adding a paused torrent resumes it
			existing.t = t
			existing.paused = false
			existing.preview = false
			e.startWhenReady(t, h)
		}
		magnet = existing.magnet
		e.mu.Unlock()
		return h, name, magnet, nil
	}
	e.items[h] = &item{t: t, magnet: magnet, name: name, seeding: e.cfg.SeedAfterComplete}
	e.mu.Unlock()

	e.startWhenReady(t, h)
	return h, name, magnet, nil
}

func fetchMetaInfo(ctx context.Context, url string) (*metainfo.MetaInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := torrentClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: unexpected status %d", url, resp.StatusCode)
	}
	return metainfo.Load(io.LimitReader(resp.Body, maxTorrentBytes))
}

// AddForPreview fetches metadata only: the torrent is registered but no data
// is downloaded until StartDownload is called. If already tracked, it is a
// no-op returning the hash.
func (e *Engine) AddForPreview(magnet string) (metainfo.Hash, error) {
	t, err := e.client.AddMagnet(magnet)
	if err != nil {
		return metainfo.Hash{}, err
	}
	h := t.InfoHash()

	e.mu.Lock()
	if _, ok := e.items[h]; ok {
		e.mu.Unlock()
		return h, nil
	}
	e.items[h] = &item{t: t, magnet: magnet, seeding: e.cfg.SeedAfterComplete, preview: true}
	e.mu.Unlock()

	e.startWhenReady(t, h) // no-ops until preview is cleared
	return h, nil
}

func (e *Engine) startWhenReady(t *torrent.Torrent, h metainfo.Hash) {
	go func() {
		defer func() { _ = recover() }() // a torrent callback must never crash the app
		select {
		case <-t.GotInfo():
		case <-t.Closed():
			return
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		it, ok := e.items[h]
		if !ok || it.preview {
			return // dropped, or waiting on StartDownload
		}
		e.applyExclusions(t, it, it.excluded)
	}()
}

// applyExclusions sets per-file priorities; an empty list downloads
// everything. It records the selected-bytes total. Caller must hold e.mu.
func (e *Engine) applyExclusions(t *torrent.Torrent, it *item, excluded []int) {
	if len(excluded) == 0 {
		t.DownloadAll()
		it.selectedBytes = torrentLength(t)
		return
	}
	ex := make(map[int]bool, len(excluded))
	for _, i := range excluded {
		ex[i] = true
	}
	var selected int64
	for i, f := range t.Files() {
		if ex[i] {
			f.SetPriority(torrent.PiecePriorityNone)
		} else {
			f.Download()
			selected += f.Length()
		}
	}
	it.selectedBytes = selected
}

// Files lists a torrent's files, or (nil, false) if metadata isn't ready yet.
func (e *Engine) Files(h metainfo.Hash) ([]FileInfo, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	it, ok := e.items[h]
	if !ok || it.t == nil || it.t.Info() == nil {
		return nil, false
	}
	files := it.t.Files()
	out := make([]FileInfo, len(files))
	for i, f := range files {
		out[i] = FileInfo{Index: i, Path: f.Path(), Length: f.Length()}
	}
	return out, true
}

// StartDownload flips a previewing torrent to downloading, excluding the
// given file indices.
func (e *Engine) StartDownload(h metainfo.Hash, excluded []int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	it, ok := e.items[h]
	if !ok || it.t == nil {
		return
	}
	it.preview = false
	it.excluded = excluded
	e.applyExclusions(it.t, it, excluded)
}

// Pause drops the torrent from the client but keeps its entry; piece
// completion makes resuming cheap.
func (e *Engine) Pause(h metainfo.Hash) {
	e.mu.Lock()
	defer e.mu.Unlock()
	it, ok := e.items[h]
	if !ok || it.paused || it.t == nil {
		return
	}
	it.name = displayName(it)
	it.length = torrentLength(it.t)
	it.done = it.t.BytesCompleted()
	it.t.Drop()
	it.t = nil
	it.paused = true
	it.samples = ring{}
}

// Resume re-adds a paused torrent.
func (e *Engine) Resume(h metainfo.Hash) error {
	e.mu.Lock()
	it, ok := e.items[h]
	if !ok || !it.paused {
		e.mu.Unlock()
		return nil
	}
	magnet := it.magnet
	excluded := it.excluded
	e.mu.Unlock()
	_, err := e.Add(magnet, excluded)
	return err
}

// SetSeeding toggles uploading for a torrent by capping its connections.
func (e *Engine) SetSeeding(h metainfo.Hash, on bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	it, ok := e.items[h]
	if !ok {
		return
	}
	it.seeding = on
	if it.t == nil {
		return
	}
	if on {
		max := e.cfg.MaxConnections
		if max <= 0 {
			max = 50
		}
		it.t.SetMaxEstablishedConns(max)
	} else {
		it.t.SetMaxEstablishedConns(0)
	}
}

// Remove drops the torrent and forgets it; optionally deletes downloaded data.
func (e *Engine) Remove(h metainfo.Hash, deleteData bool) error {
	e.mu.Lock()
	it, ok := e.items[h]
	if !ok {
		e.mu.Unlock()
		return nil
	}
	var dataPath string
	if deleteData {
		if name := displayName(it); name != "" && name != "?" {
			if p, ok := safeDataPath(e.cfg.DownloadDir, name); ok {
				dataPath = p
			}
		}
	}
	if it.t != nil {
		it.t.Drop()
	}
	delete(e.items, h)
	e.mu.Unlock()

	if dataPath != "" {
		return os.RemoveAll(dataPath)
	}
	return nil
}

// safeDataPath resolves a torrent's data path under dir, refusing anything that
// escapes it. A torrent's name is untrusted metadata: without this a crafted
// name like "../../x" would let delete-data RemoveAll a path outside the
// download directory (and "." would target the whole directory).
func safeDataPath(dir, name string) (string, bool) {
	joined := filepath.Join(dir, name)
	rel, err := filepath.Rel(dir, joined)
	if err != nil || rel == "." || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return joined, true
}

// Magnet returns the original magnet URI for a tracked torrent.
func (e *Engine) Magnet(h metainfo.Hash) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if it, ok := e.items[h]; ok {
		return it.magnet
	}
	return ""
}

// Snapshots samples progress for every tracked torrent. Call it on a tick;
// each call feeds the speed estimator.
func (e *Engine) Snapshots() []Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	out := make([]Snapshot, 0, len(e.items))
	for h, it := range e.items {
		out = append(out, e.snapshot(h, it, now))
	}
	return out
}

func (e *Engine) snapshot(h metainfo.Hash, it *item, now time.Time) Snapshot {
	s := Snapshot{Hash: h, Magnet: it.magnet, Name: displayName(it)}

	if it.paused || it.t == nil {
		s.State = StatePaused
		s.BytesCompleted = it.done
		s.Length = it.length
		return s
	}

	t := it.t
	s.BytesCompleted = t.BytesCompleted()
	s.Length = effectiveLength(it)
	stats := t.Stats()
	s.PeersActive = stats.ActivePeers
	s.PeersTotal = stats.TotalPeers

	it.samples.push(sample{at: now, bytes: s.BytesCompleted})
	s.SpeedBps = it.samples.speedBps()

	switch {
	case t.Info() == nil:
		s.State = StateFetchingMeta
	case it.preview:
		s.State = StatePreviewing
	case s.Length > 0 && s.BytesCompleted >= s.Length:
		if it.seeding {
			s.State = StateSeeding
		} else {
			s.State = StateDone
		}
	default:
		s.State = StateDownloading
		if s.SpeedBps > 0 {
			remaining := float64(s.Length - s.BytesCompleted)
			s.ETA = time.Duration(remaining / s.SpeedBps * float64(time.Second))
		}
	}
	return s
}

// Close shuts the client down gracefully, flushing piece completion.
func (e *Engine) Close() {
	e.client.Close()
	<-e.client.Closed()
	e.pcClose()
}

func displayName(it *item) string {
	if it.t != nil {
		if n := it.t.Name(); n != "" {
			return n
		}
	}
	if it.name != "" {
		return it.name
	}
	return "?"
}

func torrentLength(t *torrent.Torrent) int64 {
	if t.Info() == nil {
		return 0
	}
	return t.Length()
}

// effectiveLength is the selected-bytes total for a partial download, or the
// full torrent length when nothing is excluded yet.
func effectiveLength(it *item) int64 {
	if it.selectedBytes > 0 {
		return it.selectedBytes
	}
	return torrentLength(it.t)
}
