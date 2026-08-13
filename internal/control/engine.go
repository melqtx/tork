package control

import (
	"context"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/melqtx/tork/internal/engine"
)

// Engine is the front end's view of the torrent engine. Every method here has
// to survive a round trip: no channels, no callbacks, no handing back live
// engine state.
//
// Note what is absent: Close. Shutting the engine down is the lifetime of
// whoever owns it, and once that owner is a daemon shared by other clients, a
// front end asking it to close is asking to hang up on somebody else.
type Engine interface {
	// Add queues a magnet and starts downloading it.
	Add(magnet string, excluded []int) (metainfo.Hash, error)
	AddWithOptions(magnet string, opts engine.AddOptions) (metainfo.Hash, error)

	// AddDirect queues a plain HTTP(S) download (the ISO catalog path).
	AddDirect(url, name, sum string) (metainfo.Hash, error)
	AddDirectWithOptions(url, name, sum string, opts engine.AddOptions) (metainfo.Hash, error)

	// AddTorrentURL fetches a .torrent over HTTP and queues it.
	AddTorrentURL(ctx context.Context, url string) (h metainfo.Hash, name, magnet string, err error)

	// The ForPreview adds park a torrent in the engine only to pull its
	// metadata, and report whether the caller now owns it (owned == it was not
	// already present, so dropping the preview should remove it again).
	AddForPreview(magnet string) (h metainfo.Hash, owned bool, err error)
	AddTorrentURLForPreview(ctx context.Context, url string) (h metainfo.Hash, name, magnet string, owned bool, err error)
	AddTorrentFileForPreview(path string) (h metainfo.Hash, name, magnet string, owned bool, err error)

	// StartDownload promotes a previewed torrent to a real download.
	StartDownload(h metainfo.Hash, excluded []int)
	Pause(h metainfo.Hash) error
	SetSeeding(h metainfo.Hash, on bool) error
	Remove(h metainfo.Hash, deleteData bool) error

	// Verify rehashes a completed download. It runs for as long as the data is
	// large, so ctx is the only way to abandon it.
	Verify(ctx context.Context, h metainfo.Hash) (engine.VerifyResult, error)

	Files(h metainfo.Hash) ([]engine.FileInfo, bool)
	Magnet(h metainfo.Hash) string
	Snapshots() []engine.Snapshot
	Snapshot(h metainfo.Hash) (engine.Snapshot, bool)
	MetadataDiscovery(h metainfo.Hash) (engine.MetadataStatus, bool)
}

// The in-process implementation. If a signature drifts, this fails to compile
// here rather than at the call site.
var _ Engine = (*engine.Engine)(nil)
