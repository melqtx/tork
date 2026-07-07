package provider

import (
	"net/url"
	"strings"
)

// DefaultTrackers is a broad set of stable public trackers injected into every
// magnet we build ourselves. A wider list dramatically improves peer discovery
// for torrents whose source gave us only an infohash.
var DefaultTrackers = []string{
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://open.demonii.com:1337/announce",
	"udp://open.stealth.si:80/announce",
	"udp://tracker.torrent.eu.org:451/announce",
	"udp://exodus.desync.com:6969/announce",
	"udp://tracker.openbittorrent.com:6969/announce",
	"udp://explodie.org:6969/announce",
	"udp://tracker.dler.org:6969/announce",
	"udp://open.tracker.cl:1337/announce",
	"udp://tracker1.bt.moack.co.kr:80/announce",
	"udp://tracker.moeking.me:6969/announce",
	"udp://p4p.arenabg.com:1337/announce",
	"udp://opentracker.i2p.rocks:6969/announce",
	"udp://tracker.tiny-vps.com:6969/announce",
	"udp://tracker.bittor.pw:1337/announce",
	"https://tracker.tamersunion.org:443/announce",
}

// BuildMagnet assembles a magnet URI from an info hash (hex), display name,
// and tracker list.
func BuildMagnet(infoHash, displayName string, trackers []string) string {
	var b strings.Builder
	b.WriteString("magnet:?xt=urn:btih:")
	b.WriteString(strings.ToLower(infoHash))
	if displayName != "" {
		b.WriteString("&dn=")
		b.WriteString(url.QueryEscape(displayName))
	}
	for _, tr := range trackers {
		b.WriteString("&tr=")
		b.WriteString(url.QueryEscape(tr))
	}
	return b.String()
}
