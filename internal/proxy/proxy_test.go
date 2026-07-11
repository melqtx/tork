package proxy

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewValidatesAndRedacts(t *testing.T) {
	runtime, err := New("socks5h://alice:secret@[::1]:9050")
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.Enabled() || !runtime.HasCredentials() || runtime.Endpoint() != "[::1]:9050" {
		t.Fatalf("runtime = enabled:%v credentials:%v endpoint:%q", runtime.Enabled(), runtime.HasCredentials(), runtime.Endpoint())
	}

	for _, raw := range []string{
		"http://alice:secret@127.0.0.1:9050",
		"socks5://alice:secret@",
		"socks5://alice:secret@127.0.0.1:0",
		"socks5://alice:secret@127.0.0.1:9050/path",
	} {
		_, err := New(raw)
		if err == nil {
			t.Fatalf("New(%q) succeeded", raw)
		}
		if strings.Contains(err.Error(), "alice") || strings.Contains(err.Error(), "secret") {
			t.Fatalf("New(%q) leaked credentials: %v", raw, err)
		}
	}
}

func TestNewRejectsOverlongSOCKS5Credentials(t *testing.T) {
	tooLong := strings.Repeat("a", 256)
	for _, raw := range []string{
		"socks5://" + tooLong + "@127.0.0.1:9050",
		"socks5://alice:" + tooLong + "@127.0.0.1:9050",
	} {
		_, err := New(raw)
		if err == nil || !strings.Contains(err.Error(), "255 bytes") {
			t.Fatalf("New(overlong credentials) = %v, want clear length error", err)
		}
		if strings.Contains(err.Error(), tooLong) {
			t.Fatalf("credential error leaked the configured value: %v", err)
		}
	}
}

func TestRuntimeRoutesHTTPAndRemoteDNSThroughSOCKS5(t *testing.T) {
	proxy := newSOCKS5Server(t, "alice", "secret")
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("through socks"))
	}))
	defer target.Close()

	runtime, err := New("socks5://alice:secret@" + proxy.Addr())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := runtime.DialContext(context.Background(), "tcp", "catalog.example:443")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if got := proxy.nextTarget(t); got != "catalog.example:443" {
		t.Fatalf("SOCKS target = %q, want remote hostname", got)
	}

	// A poisoned environment proxy must not replace the configured SOCKS route.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	resp, err := runtime.HTTPClient(5*time.Second, 0).Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "through socks" {
		t.Fatalf("HTTP body = %q", body)
	}
	if got := proxy.nextTarget(t); !strings.HasPrefix(got, "127.0.0.1:") {
		t.Fatalf("HTTP request bypassed SOCKS proxy; target = %q", got)
	}
}

func TestVerifyEgressUsesSOCKSAndParsesTorResult(t *testing.T) {
	socks := newSOCKS5Server(t, "alice", "secret")
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"IP":"185.220.101.7","IsTor":true}`))
	}))
	defer echo.Close()

	runtime, err := New("socks5://alice:secret@" + socks.Addr())
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "http://localhost" + strings.TrimPrefix(echo.URL, "http://127.0.0.1")
	egress, err := runtime.VerifyEgressAt(context.Background(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if egress.IP != "185.220.101.7" || !egress.IsTor {
		t.Fatalf("egress = %+v", egress)
	}
	if target := socks.nextTarget(t); !strings.HasPrefix(target, "localhost:") {
		t.Fatalf("egress check bypassed remote-DNS SOCKS route: %q", target)
	}
}

func TestVerifyEgressDistinguishesServiceAndRouteFailures(t *testing.T) {
	socks := newSOCKS5Server(t, "alice", "secret")
	badService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer badService.Close()
	runtime, err := New("socks5://alice:secret@" + socks.Addr())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.VerifyEgressAt(context.Background(), badService.URL); err == nil || IsRouteFailure(err) || err.Error() != "proxy verification service unavailable" {
		t.Fatalf("service failure = %v, want non-route unavailable error", err)
	}

	dead, err := New("socks5://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dead.VerifyEgressAt(context.Background(), badService.URL); err == nil || !IsRouteFailure(err) || err.Error() != "proxy route unavailable" {
		t.Fatalf("route failure = %v, want classified SOCKS route failure", err)
	}
}

func TestVerifyEgressUnreachableTargetIsServiceFailureNotRouteFailure(t *testing.T) {
	// The relay is healthy but its dial to the check endpoint fails, so it
	// answers the CONNECT with "host unreachable". That is the check service
	// being down, not the proxy: it must never show as a dead route.
	socks := newSOCKS5Server(t, "alice", "secret")
	runtime, err := New("socks5://alice:secret@" + socks.Addr())
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtime.VerifyEgressAt(context.Background(), "http://127.0.0.1:1")
	if err == nil || IsRouteFailure(err) || err.Error() != "proxy verification service unavailable" {
		t.Fatalf("unreachable target = %v, want service failure", err)
	}
	if got := socks.nextTarget(t); got != "127.0.0.1:1" {
		t.Fatalf("SOCKS target = %q", got)
	}
}

func TestVerifyEgressAuthFailureIsRouteFailure(t *testing.T) {
	socks := newSOCKS5Server(t, "alice", "secret")
	runtime, err := New("socks5://alice:wrong@" + socks.Addr())
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtime.VerifyEgressAt(context.Background(), "http://127.0.0.1:1")
	if err == nil || !IsRouteFailure(err) {
		t.Fatalf("rejected credentials = %v, want route failure", err)
	}
	if msg := err.Error(); strings.Contains(msg, "alice") || strings.Contains(msg, "wrong") {
		t.Fatalf("route failure leaked credentials: %v", msg)
	}
}

func TestVerifyEgressRejectsMalformedPayload(t *testing.T) {
	socks := newSOCKS5Server(t, "alice", "secret")
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"IP":"not-an-ip","IsTor":false}`))
	}))
	defer echo.Close()
	runtime, err := New("socks5://alice:secret@" + socks.Addr())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.VerifyEgressAt(context.Background(), echo.URL); err == nil || IsRouteFailure(err) {
		t.Fatalf("malformed egress = %v, want service failure", err)
	}
}

// socks5Server is a tiny authenticated SOCKS5 relay for proxy integration
// tests. It records the exact target before connecting, which lets the tests
// assert that hostnames, not locally resolved IPs, reach the proxy.
type socks5Server struct {
	ln      net.Listener
	user    string
	pass    string
	targets chan string
	done    chan struct{}
	wg      sync.WaitGroup
}

func newSOCKS5Server(t *testing.T, user, pass string) *socks5Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &socks5Server{ln: ln, user: user, pass: pass, targets: make(chan string, 8), done: make(chan struct{})}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-s.done:
					return
				default:
					return
				}
			}
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				s.serve(conn)
			}()
		}
	}()
	t.Cleanup(func() {
		close(s.done)
		_ = s.ln.Close()
		s.wg.Wait()
	})
	return s
}

func (s *socks5Server) Addr() string { return s.ln.Addr().String() }

func (s *socks5Server) nextTarget(t *testing.T) string {
	t.Helper()
	select {
	case target := <-s.targets:
		return target
	case <-time.After(5 * time.Second):
		t.Fatal("SOCKS server received no connection")
		return ""
	}
}

func (s *socks5Server) serve(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(conn)
	version, err := r.ReadByte()
	if err != nil || version != 5 {
		return
	}
	nMethods, err := r.ReadByte()
	if err != nil || nMethods == 0 {
		return
	}
	methods := make([]byte, nMethods)
	if _, err := io.ReadFull(r, methods); err != nil || !contains(methods, 2) {
		return
	}
	if _, err := conn.Write([]byte{5, 2}); err != nil {
		return
	}
	if !s.authenticate(r, conn) {
		return
	}
	target, ok := readSOCKSTarget(r)
	if !ok {
		return
	}
	s.targets <- target
	if strings.HasPrefix(target, "catalog.example:") {
		_, _ = conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	upstream, err := net.Dial("tcp", target)
	if err != nil {
		_, _ = conn.Write([]byte{5, 4, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer upstream.Close()
	if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	_ = upstream.SetDeadline(time.Time{})
	go func() { _, _ = io.Copy(upstream, r); _ = upstream.Close() }()
	_, _ = io.Copy(conn, upstream)
}

func (s *socks5Server) authenticate(r *bufio.Reader, conn net.Conn) bool {
	version, err := r.ReadByte()
	if err != nil || version != 1 {
		return false
	}
	name, ok := readSOCKSString(r)
	if !ok {
		return false
	}
	pass, ok := readSOCKSString(r)
	if !ok || name != s.user || pass != s.pass {
		_, _ = conn.Write([]byte{1, 1})
		return false
	}
	_, err = conn.Write([]byte{1, 0})
	return err == nil
}

func readSOCKSTarget(r *bufio.Reader) (string, bool) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil || header[0] != 5 || header[1] != 1 {
		return "", false
	}
	var host string
	switch header[3] {
	case 1:
		buf := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", false
		}
		host = net.IP(buf).String()
	case 3:
		name, ok := readSOCKSString(r)
		if !ok {
			return "", false
		}
		host = name
	case 4:
		buf := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", false
		}
		host = net.IP(buf).String()
	default:
		return "", false
	}
	var portBytes [2]byte
	if _, err := io.ReadFull(r, portBytes[:]); err != nil {
		return "", false
	}
	return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(portBytes[:])))), true
}

func readSOCKSString(r *bufio.Reader) (string, bool) {
	n, err := r.ReadByte()
	if err != nil || n == 0 {
		return "", false
	}
	buf := make([]byte, n)
	_, err = io.ReadFull(r, buf)
	return string(buf), err == nil
}

func contains(values []byte, want byte) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
