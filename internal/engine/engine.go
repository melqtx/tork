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
	StateMissing
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
	case StateMissing:
		return "missing data"
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
	DownloadDir    string
	DataPath       string
	Seed           bool
	BytesCompleted int64
	Length         int64 // 0 until metadata arrives
	SpeedBps       float64
	ETA            time.Duration // 0 when unknown or done
	PeersActive    int
	PeersTotal     int
	Seeders        int // peers we are connected to that hold the complete torrent
	State          TorrentState
	Note           string // short human status (direct downloads: failure reason)
}

func (s Snapshot) Progress() float64 {
	if s.Length == 0 {
		return 0
	}
	return float64(s.BytesCompleted) / float64(s.Length)
}

type AddOptions struct {
	DownloadDir string
	Excluded    []int
	Seed        *bool
	Preview     bool
}

type item struct {
	t             *torrent.Torrent // nil while paused
	magnet        string
	name          string // last known name, survives pause
	downloadDir   string
	dataPath      string
	length        int64 // last known length
	done          int64 // last known completed bytes
	paused        bool
	preview       bool // fetched metadata only; awaiting StartDownload
	seeding       bool
	noSeedApplied bool  // connections already capped for a completed non-seeding item
	excluded      []int // file indices not to download
	selectedBytes int64 // sum of non-excluded file lengths (0 until applied)
	samples       ring
}

type Engine struct {
	client  *torrent.Client
	cfg     *config.Config
	pc      storage.PieceCompletion
	mu      sync.Mutex
	items   map[metainfo.Hash]*item
	direct  map[metainfo.Hash]*directItem // plain-HTTPS downloads (see direct.go)
	dwg     sync.WaitGroup                // running direct-download goroutines
	pcClose func()                        // bolt piece-completion closer
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
		pc:      pc,
		items:   make(map[metainfo.Hash]*item),
		direct:  make(map[metainfo.Hash]*directItem),
		pcClose: func() { pc.Close() },
	}, nil
}

func (e *Engine) normalizeOptions(opts AddOptions) AddOptions {
	if opts.DownloadDir == "" {
		opts.DownloadDir = e.cfg.DownloadDir
	}
	if abs, err := filepath.Abs(opts.DownloadDir); err == nil {
		opts.DownloadDir = abs
	}
	if opts.Seed == nil {
		seed := e.cfg.SeedAfterComplete
		opts.Seed = &seed
	}
	opts.Excluded = append([]int(nil), opts.Excluded...)
	return opts
}

func (e *Engine) storageForDir(dir string) storage.ClientImpl {
	return storage.NewFileWithCompletion(dir, e.pc)
}

// Add starts downloading a magnet, skipping the given file indices (nil =
// everything). It never blocks on metadata: the torrent is registered
// immediately and downloading begins once info arrives. Adding an existing
// torrent is a no-op returning its hash.
func (e *Engine) Add(magnet string, excluded []int) (metainfo.Hash, error) {
	return e.AddWithOptions(magnet, AddOptions{Excluded: excluded})
}

func (e *Engine) AddWithOptions(magnet string, opts AddOptions) (metainfo.Hash, error) {
	h, _, err := e.addMagnetWithOptions(magnet, opts)
	return h, err
}

func (e *Engine) addMagnetWithOptions(magnet string, opts AddOptions) (metainfo.Hash, bool, error) {
	opts = e.normalizeOptions(opts)
	spec, err := torrent.TorrentSpecFromMagnetUri(magnet)
	if err != nil {
		return metainfo.Hash{}, false, err
	}
	if opts.Preview && !spec.InfoHash.IsZero() {
		h := metainfo.Hash(spec.InfoHash)
		e.mu.Lock()
		if _, ok := e.items[h]; ok {
			e.mu.Unlock()
			return h, false, nil
		}
		e.mu.Unlock()
	}
	spec.Storage = e.storageForDir(opts.DownloadDir)

	t, _, err := e.client.AddTorrentSpec(spec)
	if err != nil {
		return metainfo.Hash{}, false, err
	}
	h := t.InfoHash()

	e.mu.Lock()
	if existing, ok := e.items[h]; ok {
		if existing.paused { // re-adding a paused torrent resumes it
			existing.t = t
			existing.paused = false
			existing.preview = opts.Preview
			existing.downloadDir = opts.DownloadDir
			existing.seeding = *opts.Seed
			existing.noSeedApplied = false
			e.startWhenReady(t, h)
		}
		e.mu.Unlock()
		return h, false, nil
	}
	e.items[h] = &item{
		t:           t,
		magnet:      magnet,
		downloadDir: opts.DownloadDir,
		seeding:     *opts.Seed,
		excluded:    opts.Excluded,
		preview:     opts.Preview,
	}
	e.mu.Unlock()

	e.startWhenReady(t, h)
	return h, true, nil
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
	return e.AddTorrentURLWithOptions(ctx, url, AddOptions{})
}

func (e *Engine) AddTorrentURLWithOptions(ctx context.Context, url string, opts AddOptions) (h metainfo.Hash, name, magnet string, err error) {
	opts = e.normalizeOptions(opts)
	mi, err := fetchMetaInfo(ctx, url)
	if err != nil {
		return metainfo.Hash{}, "", "", fmt.Errorf("fetch torrent: %w", err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return metainfo.Hash{}, "", "", fmt.Errorf("read torrent: %w", err)
	}

	spec := torrent.TorrentSpecFromMetaInfo(mi)
	spec.Storage = e.storageForDir(opts.DownloadDir)
	t, _, err := e.client.AddTorrentSpec(spec)
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
			existing.preview = opts.Preview
			existing.downloadDir = opts.DownloadDir
			existing.seeding = *opts.Seed
			existing.noSeedApplied = false
			e.startWhenReady(t, h)
		}
		magnet = existing.magnet
		e.mu.Unlock()
		return h, name, magnet, nil
	}
	e.items[h] = &item{
		t:           t,
		magnet:      magnet,
		name:        name,
		downloadDir: opts.DownloadDir,
		seeding:     *opts.Seed,
		excluded:    opts.Excluded,
		preview:     opts.Preview,
	}
	e.mu.Unlock()

	e.startWhenReady(t, h)
	return h, name, magnet, nil
}

// AddTorrentURLForPreview fetches a .torrent file and registers it in preview
// mode. It returns owned=false when the torrent was already tracked, so callers
// know cancel must not remove it.
func (e *Engine) AddTorrentURLForPreview(ctx context.Context, url string) (h metainfo.Hash, name, magnet string, owned bool, err error) {
	return e.addTorrentURLForPreviewWithOptions(ctx, url, AddOptions{Preview: true})
}

func (e *Engine) addTorrentURLForPreviewWithOptions(ctx context.Context, url string, opts AddOptions) (h metainfo.Hash, name, magnet string, owned bool, err error) {
	opts.Preview = true
	opts = e.normalizeOptions(opts)
	mi, err := fetchMetaInfo(ctx, url)
	if err != nil {
		return metainfo.Hash{}, "", "", false, fmt.Errorf("fetch torrent: %w", err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return metainfo.Hash{}, "", "", false, fmt.Errorf("read torrent: %w", err)
	}
	h = mi.HashInfoBytes()
	name = info.Name
	magnet = mi.Magnet(&h, &info).String()

	e.mu.Lock()
	if existing, ok := e.items[h]; ok {
		if existing.magnet != "" {
			magnet = existing.magnet
		}
		e.mu.Unlock()
		return h, name, magnet, false, nil
	}
	e.mu.Unlock()

	spec := torrent.TorrentSpecFromMetaInfo(mi)
	spec.Storage = e.storageForDir(opts.DownloadDir)
	t, _, err := e.client.AddTorrentSpec(spec)
	if err != nil {
		return metainfo.Hash{}, "", "", false, err
	}
	h = t.InfoHash()

	e.mu.Lock()
	e.items[h] = &item{
		t:           t,
		magnet:      magnet,
		name:        name,
		downloadDir: opts.DownloadDir,
		seeding:     *opts.Seed,
		excluded:    opts.Excluded,
		preview:     true,
	}
	e.mu.Unlock()

	e.startWhenReady(t, h)
	return h, name, magnet, true, nil
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

// AddForPreview fetches metadata only: the torrent is registered but no data is
// downloaded until StartDownload is called. If already tracked, it is a no-op
// returning owned=false so callers do not remove it on preview cancel.
func (e *Engine) AddForPreview(magnet string) (metainfo.Hash, bool, error) {
	return e.addMagnetWithOptions(magnet, AddOptions{Preview: true})
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
		it.name = displayName(it)
		it.dataPath = torrentDataPath(it)
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
	it.name = displayName(it)
	it.dataPath = torrentDataPath(it)
	e.applyExclusions(it.t, it, excluded)
}

// Pause drops the torrent from the client but keeps its entry; piece
// completion makes resuming cheap. A direct download keeps its .part file.
func (e *Engine) Pause(h metainfo.Hash) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if d, ok := e.direct[h]; ok {
		pauseDirectLocked(d)
		return
	}
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

// Resume re-adds a paused torrent, or restarts a paused direct download from
// its .part file.
func (e *Engine) Resume(h metainfo.Hash) error {
	e.mu.Lock()
	if d, ok := e.direct[h]; ok {
		if d.state == StatePaused {
			e.startDirectLocked(d)
		}
		e.mu.Unlock()
		return nil
	}
	it, ok := e.items[h]
	if !ok || !it.paused {
		e.mu.Unlock()
		return nil
	}
	magnet := it.magnet
	excluded := it.excluded
	downloadDir := it.downloadDir
	seed := it.seeding
	e.mu.Unlock()
	_, err := e.AddWithOptions(magnet, AddOptions{DownloadDir: downloadDir, Excluded: excluded, Seed: &seed})
	return err
}

// SetSeeding toggles uploading for a torrent by capping its connections.
// Direct downloads have nothing to seed; the toggle is inert for them.
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
		it.noSeedApplied = false
	} else {
		it.t.SetMaxEstablishedConns(0)
		it.noSeedApplied = true
	}
}

// Remove drops the torrent and forgets it; optionally deletes downloaded data.
func (e *Engine) Remove(h metainfo.Hash, deleteData bool) error {
	e.mu.Lock()
	if d, ok := e.direct[h]; ok {
		e.mu.Unlock()
		return e.removeDirect(h, d, deleteData)
	}
	it, ok := e.items[h]
	if !ok {
		e.mu.Unlock()
		return nil
	}
	var dataPath string
	if deleteData {
		dataPath = it.dataPath
		if dataPath == "" {
			dataPath = torrentDataPath(it)
		}
		if dataPath == "" || !safePathWithin(it.downloadDir, dataPath) {
			e.mu.Unlock()
			return fmt.Errorf("delete data refused: unknown or unsafe path")
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
	if dir == "" || strings.TrimSpace(name) == "" {
		return "", false
	}
	base, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	joined := filepath.Join(base, name)
	rel, err := filepath.Rel(base, joined)
	if err != nil || rel == "." || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return joined, true
}

func safePathWithin(dir, path string) bool {
	if dir == "" || path == "" {
		return false
	}
	base, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(base, target)
	return err == nil && rel != "." && rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

// Magnet returns the resume key for a tracked download: the magnet URI for a
// torrent, or the https URL for a direct download.
func (e *Engine) Magnet(h metainfo.Hash) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if it, ok := e.items[h]; ok {
		return it.magnet
	}
	if d, ok := e.direct[h]; ok {
		return d.url
	}
	return ""
}

// Snapshots samples progress for every tracked torrent. Call it on a tick;
// each call feeds the speed estimator.
func (e *Engine) Snapshots() []Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	out := make([]Snapshot, 0, len(e.items)+len(e.direct))
	for h, it := range e.items {
		out = append(out, e.snapshot(h, it, now))
	}
	for h, d := range e.direct {
		out = append(out, e.directSnapshot(h, d, now))
	}
	return out
}

func (e *Engine) Snapshot(h metainfo.Hash) (Snapshot, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	if it, ok := e.items[h]; ok {
		return e.snapshot(h, it, now), true
	}
	if d, ok := e.direct[h]; ok {
		return e.directSnapshot(h, d, now), true
	}
	return Snapshot{}, false
}

func (e *Engine) snapshot(h metainfo.Hash, it *item, now time.Time) Snapshot {
	name := displayName(it)
	if name != "?" {
		it.name = name
		if it.dataPath == "" {
			it.dataPath = torrentDataPath(it)
		}
	}
	s := Snapshot{Hash: h, Magnet: it.magnet, Name: name, DownloadDir: it.downloadDir, DataPath: it.dataPath, Seed: it.seeding}

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
	s.Seeders = stats.ConnectedSeeders

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
			// A non-seeding item must actually stop uploading once complete.
			// The client-wide Seed default keeps a finished torrent seeding, so
			// drop its connections here the first time we observe completion.
			// Idempotent via noSeedApplied; SetSeeding manages the flag when the
			// user toggles seeding back on.
			if !it.noSeedApplied && it.t != nil {
				it.t.SetMaxEstablishedConns(0)
				it.noSeedApplied = true
			}
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

// ListenPort is the port the torrent client actually bound, which differs from
// cfg.ListenPort whenever that is 0 (pick any free port).
func (e *Engine) ListenPort() int { return e.client.LocalPort() }

// Close shuts the client down gracefully, flushing piece completion and
// stopping direct downloads (their .part files resume on next start).
func (e *Engine) Close() {
	e.mu.Lock()
	for _, d := range e.direct {
		if d.cancel != nil {
			d.cancel()
			d.cancel = nil
		}
	}
	e.mu.Unlock()
	e.dwg.Wait()

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

func torrentDataPath(it *item) string {
	name := displayName(it)
	if name == "" || name == "?" {
		return ""
	}
	if p, ok := safeDataPath(it.downloadDir, name); ok {
		return p
	}
	return ""
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
