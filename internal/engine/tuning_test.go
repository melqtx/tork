package engine

import (
	"strings"
	"testing"

	"github.com/anacrolix/torrent"

	"github.com/melqtx/tork/internal/config"
)

func TestApplyTorrentTuningZeroPreservesDefaults(t *testing.T) {
	cc := torrent.NewDefaultClientConfig()
	wantHalf, wantTotal := cc.HalfOpenConnsPerTorrent, cc.TotalHalfOpenConns
	wantHashers, wantBytes := cc.PieceHashersPerTorrent, cc.MaxUnverifiedBytes
	wantHigh, wantLow := cc.TorrentPeersHighWater, cc.TorrentPeersLowWater
	wantDial, wantDown, wantUp := cc.DialRateLimiter, cc.DownloadRateLimiter, cc.UploadRateLimiter
	if err := applyTorrentTuning(cc, config.TorrentTuningConfig{}); err != nil {
		t.Fatal(err)
	}
	if cc.HalfOpenConnsPerTorrent != wantHalf || cc.TotalHalfOpenConns != wantTotal ||
		cc.PieceHashersPerTorrent != wantHashers || cc.MaxUnverifiedBytes != wantBytes ||
		cc.TorrentPeersHighWater != wantHigh || cc.TorrentPeersLowWater != wantLow ||
		cc.DialRateLimiter != wantDial || cc.DownloadRateLimiter != wantDown || cc.UploadRateLimiter != wantUp {
		t.Fatal("zero tuning changed library defaults")
	}
}

func TestApplyTorrentTuningMapsSupportedFields(t *testing.T) {
	cc := torrent.NewDefaultClientConfig()
	tuning := config.TorrentTuningConfig{
		HalfOpenConnsPerTorrent: 31, TotalHalfOpenConns: 120,
		PieceHashersPerTorrent: 4, MaxUnverifiedBytes: 128 << 20,
		DialRateLimit: 17, PeerHighWater: 700, PeerLowWater: 70,
		DownloadRateLimit: 5 << 20, UploadRateLimit: 2 << 20,
		DisableAggressiveUpload: true, NoUpload: true,
	}
	if err := applyTorrentTuning(cc, tuning); err != nil {
		t.Fatal(err)
	}
	if cc.HalfOpenConnsPerTorrent != 31 || cc.TotalHalfOpenConns != 120 ||
		cc.PieceHashersPerTorrent != 4 || cc.MaxUnverifiedBytes != 128<<20 ||
		cc.TorrentPeersHighWater != 700 || cc.TorrentPeersLowWater != 70 {
		t.Fatalf("mapped config = %+v", cc)
	}
	if cc.DialRateLimiter.Limit() != 17 || cc.DialRateLimiter.Burst() != 17 {
		t.Fatalf("dial limiter = %v/%d", cc.DialRateLimiter.Limit(), cc.DialRateLimiter.Burst())
	}
	if cc.DownloadRateLimiter.Limit() != 5<<20 || cc.DownloadRateLimiter.Burst() != 1<<20 ||
		cc.UploadRateLimiter.Limit() != 2<<20 || cc.UploadRateLimiter.Burst() != 1<<20 {
		t.Fatal("transfer limiters were not configured safely")
	}
	if !cc.DisableAggressiveUpload || !cc.NoUpload {
		t.Fatal("upload flags were not mapped")
	}
}

func TestApplyTorrentTuningRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		tuning config.TorrentTuningConfig
		field  string
	}{
		{config.TorrentTuningConfig{DialRateLimit: -1}, "dial_rate_limit"},
		{config.TorrentTuningConfig{DownloadRateLimit: -1}, "download_rate_limit"},
		{config.TorrentTuningConfig{PeerHighWater: 20}, "peer_low_water"}, // effective default low-water is 50
		{config.TorrentTuningConfig{PeerHighWater: 100, PeerLowWater: 101}, "peer_low_water"},
	}
	for _, tc := range cases {
		err := applyTorrentTuning(torrent.NewDefaultClientConfig(), tc.tuning)
		if err == nil || !strings.Contains(err.Error(), tc.field) {
			t.Fatalf("invalid tuning %+v error = %v, want field %q", tc.tuning, err, tc.field)
		}
	}
}
