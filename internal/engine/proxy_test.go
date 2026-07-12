package engine

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/melqtx/tork/internal/config"
)

func strictProxyConfig(t *testing.T, endpoint string) *config.Config {
	t.Helper()
	t.Setenv("XDG_DOWNLOAD_DIR", filepath.Join(t.TempDir(), "Downloads"))
	dir := filepath.Join(t.TempDir(), ".tork")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("proxy:\n  socks5: socks5://"+endpoint+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestStrictProxyEngineHasNoDirectListeners(t *testing.T) {
	eng, err := New(strictProxyConfig(t, "127.0.0.1:9050"))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	if !eng.strictProxy {
		t.Fatal("engine did not enter strict proxy mode")
	}
	if got := len(eng.client.Listeners()); got != 0 {
		t.Fatalf("strict proxy engine opened %d direct listeners", got)
	}
	if got := len(eng.client.DhtServers()); got != 0 {
		t.Fatalf("strict proxy engine started %d DHT servers", got)
	}
	if got := eng.ListenPort(); got != 0 {
		t.Fatalf("strict proxy engine listen port = %d, want no listener", got)
	}
	for _, client := range []*http.Client{eng.torrentHTTP, eng.directHTTP} {
		transport, ok := client.Transport.(*http.Transport)
		if !ok || transport.Proxy != nil || transport.DialContext == nil {
			t.Fatalf("strict proxy HTTP client is not SOCKS-only: %#v", client.Transport)
		}
	}
}

func TestStrictProxyFiltersUnsafeTorrentTransports(t *testing.T) {
	eng, err := New(strictProxyConfig(t, "127.0.0.1:9050"))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	spec := &torrent.TorrentSpec{
		Trackers: [][]string{
			{"udp://tracker.example:6969", "https://tracker.example/announce"},
			{"http://tracker-two.example/announce"},
			{"wss://tracker.example/socket"},
		},
		Webseeds: []string{"https://seed.example/file", "ftp://seed.example/file"},
		Sources:  []string{"http://source.example/meta", "udp://source.example/meta"},
		DhtNodes: []string{"node.example:6881"},
	}
	eng.prepareSpec(spec, eng.cfg.DownloadDir)

	if want := [][]string{{"https://tracker.example/announce"}, {"http://tracker-two.example/announce"}}; !reflect.DeepEqual(spec.Trackers, want) {
		t.Fatalf("safe tracker tiers = %#v, want %#v", spec.Trackers, want)
	}
	if want := []string{"https://seed.example/file"}; !reflect.DeepEqual(spec.Webseeds, want) {
		t.Fatalf("safe webseeds = %#v, want %#v", spec.Webseeds, want)
	}
	if want := []string{"http://source.example/meta"}; !reflect.DeepEqual(spec.Sources, want) {
		t.Fatalf("safe sources = %#v, want %#v", spec.Sources, want)
	}
	if spec.DhtNodes != nil {
		t.Fatalf("strict proxy DHT nodes = %#v, want nil", spec.DhtNodes)
	}
}

func TestStrictProxySanitizesCachedDiscoveryHints(t *testing.T) {
	eng, err := New(strictProxyConfig(t, "127.0.0.1:9050"))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	infoBytes, err := bencode.Marshal(metainfo.Info{Name: "cached.bin", PieceLength: 16 << 10, Length: 1, Pieces: make([]byte, 20)})
	if err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{
		InfoBytes: infoBytes,
		AnnounceList: metainfo.AnnounceList{
			{"udp://tracker.example:6969", "https://tracker.example/announce"},
		},
		Nodes: []metainfo.Node{"node.example:6881"},
	}
	if err := eng.metainfo.Store(mi); err != nil {
		t.Fatal(err)
	}
	magnet := "magnet:?xt=urn:btih:" + mi.HashInfoBytes().HexString() +
		"&tr=http%3A%2F%2Ftracker-two.example%2Fannounce"
	h, _, err := eng.AddForPreview(magnet)
	if err != nil {
		t.Fatal(err)
	}
	status, ok := eng.MetadataDiscovery(h)
	if !ok || status.Source != MetadataCache || status.Trackers != 2 || status.DHTEnabled || !status.ProxyStrict {
		t.Fatalf("cached strict discovery = %+v, ok=%v", status, ok)
	}
}

func TestStrictProxyPeersDialThroughSOCKS5(t *testing.T) {
	socks := newSOCKSRecorder(t)
	eng, err := New(strictProxyConfig(t, socks.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	peer, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	port := peer.Addr().(*net.TCPAddr).Port

	_, err = eng.Add("magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&x.pe=127.0.0.1:"+strconv.Itoa(port), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := socks.nextTarget(t); got != "127.0.0.1:"+strconv.Itoa(port) {
		t.Fatalf("peer dial target = %q, want SOCKS5 target 127.0.0.1:%d", got, port)
	}
}

// socksRecorder accepts the SOCKS5 connect request but intentionally never
// creates an outbound socket. Seeing a peer target here proves tork handed the
// peer address to SOCKS instead of opening a direct connection itself.
type socksRecorder struct {
	ln      net.Listener
	targets chan string
}

func newSOCKSRecorder(t *testing.T) *socksRecorder {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &socksRecorder{ln: ln, targets: make(chan string, 2)}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *socksRecorder) Addr() string { return s.ln.Addr().String() }

func (s *socksRecorder) nextTarget(t *testing.T) string {
	t.Helper()
	select {
	case target := <-s.targets:
		return target
	case <-time.After(5 * time.Second):
		t.Fatal("torrent peer was not routed through SOCKS5")
		return ""
	}
}

func (s *socksRecorder) serve(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(conn)
	version, err := r.ReadByte()
	if err != nil || version != 5 {
		return
	}
	n, err := r.ReadByte()
	if err != nil || n == 0 {
		return
	}
	methods := make([]byte, n)
	if _, err := io.ReadFull(r, methods); err != nil {
		return
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil || header[0] != 5 || header[1] != 1 || header[3] != 1 {
		return
	}
	ip := make([]byte, net.IPv4len)
	if _, err := io.ReadFull(r, ip); err != nil {
		return
	}
	var port [2]byte
	if _, err := io.ReadFull(r, port[:]); err != nil {
		return
	}
	s.targets <- net.JoinHostPort(net.IP(ip).String(), strconv.Itoa(int(binary.BigEndian.Uint16(port[:]))))
	_, _ = conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
}
