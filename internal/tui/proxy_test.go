package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
	proxyroute "github.com/melqtx/tork/internal/proxy"
	"github.com/melqtx/tork/internal/state"
)

func proxyTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("proxy:\n  socks5: socks5://127.0.0.1:9050\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadReadOnlyFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg, nil, nil, &state.State{}, nil)
}

func TestProxyBadgeShowsProofAndUncertaintyStates(t *testing.T) {
	a := proxyTestApp(t)
	if got := a.proxyStatusTail(); !strings.Contains(got, "SOCKS strict · unverified") {
		t.Fatalf("initial badge = %q", got)
	}
	a.onProxyCheck(proxyCheckMsg{isTor: true})
	if got := a.proxyStatusTail(); !strings.Contains(got, "Tor strict") {
		t.Fatalf("Tor badge = %q", got)
	}
	a.onProxyCheck(proxyCheckMsg{isTor: false})
	if got := a.proxyStatusTail(); !strings.Contains(got, "SOCKS strict") || strings.Contains(got, "Tor") {
		t.Fatalf("generic SOCKS badge = %q", got)
	}
	a.onProxyCheck(proxyCheckMsg{err: errors.New("echo endpoint down")})
	if got := a.proxyStatusTail(); !strings.Contains(got, "check unavailable") {
		t.Fatalf("service-outage badge = %q", got)
	}
	if out := a.footerBar("help"); !strings.Contains(out, "check unavailable") {
		t.Fatalf("chrome footer omitted proxy badge: %q", out)
	}
	a.width, a.height = 100, 30
	if out := a.viewSearch(); !strings.Contains(out, "check unavailable") {
		t.Fatalf("search footer omitted proxy badge: %q", out)
	}
}

func TestProxyBadgeMarksTwoRouteFailuresWithoutPausing(t *testing.T) {
	a := proxyTestApp(t)
	dead, err := proxyroute.New("socks5://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	_, routeErr := dead.VerifyEgressAt(context.Background(), "http://127.0.0.1:9")
	if routeErr == nil || !proxyroute.IsRouteFailure(routeErr) {
		t.Fatalf("route error = %v", routeErr)
	}
	a.onProxyCheck(proxyCheckMsg{err: routeErr})
	if got := a.proxyStatusTail(); !strings.Contains(got, "retrying") {
		t.Fatalf("first route failure badge = %q", got)
	}
	a.onProxyCheck(proxyCheckMsg{err: routeErr})
	if got := a.proxyStatusTail(); !strings.Contains(got, "PROXY UNREACHABLE") || !strings.Contains(got, "strict mode remains on") {
		t.Fatalf("second route failure badge = %q", got)
	}
}

func TestProxyChecksRunOnlyDuringTransfers(t *testing.T) {
	a := proxyTestApp(t)
	called := 0
	a.proxyCheck = func(context.Context, *proxyroute.Runtime) (proxyroute.Egress, error) {
		called++
		return proxyroute.Egress{IP: "185.220.101.7", IsTor: true}, nil
	}

	if cmd := a.startProxyCheck(time.Now()); cmd != nil {
		t.Fatal("idle app started a proxy egress check")
	}

	// An active transfer checks immediately, then on the five-minute cadence.
	a.downloads.snaps = []engine.Snapshot{{State: engine.StateDownloading}}
	cmd := a.startProxyCheck(time.Now())
	if cmd == nil {
		t.Fatal("active transfer did not start a proxy egress check")
	}
	msg, ok := cmd().(proxyCheckMsg)
	if !ok || called != 1 || !msg.isTor || msg.err != nil {
		t.Fatalf("active proxy check = %#v, called=%d", msg, called)
	}
	a.onProxyCheck(msg)
	if cmd := a.startProxyCheck(time.Now()); cmd != nil {
		t.Fatal("proxy check ignored its five-minute cadence")
	}
}

func TestProxyBadgeEscalatesAcrossInterleavedServiceFailures(t *testing.T) {
	a := proxyTestApp(t)
	dead, err := proxyroute.New("socks5://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	_, routeErr := dead.VerifyEgressAt(context.Background(), "http://127.0.0.1:9")
	if routeErr == nil || !proxyroute.IsRouteFailure(routeErr) {
		t.Fatalf("route error = %v", routeErr)
	}
	a.onProxyCheck(proxyCheckMsg{err: routeErr})
	if got := a.proxyStatusTail(); !strings.Contains(got, "retrying") {
		t.Fatalf("first route failure badge = %q", got)
	}
	// A check-endpoint outage between two route failures must not launder the
	// route's track record back to zero.
	a.onProxyCheck(proxyCheckMsg{err: errors.New("echo endpoint down")})
	if got := a.proxyStatusTail(); !strings.Contains(got, "check unavailable") {
		t.Fatalf("interleaved service failure badge = %q", got)
	}
	a.onProxyCheck(proxyCheckMsg{err: routeErr})
	if got := a.proxyStatusTail(); !strings.Contains(got, "PROXY UNREACHABLE") {
		t.Fatalf("badge after interleaved failures = %q", got)
	}
	// Only a successful verification clears the record.
	a.onProxyCheck(proxyCheckMsg{isTor: true})
	a.onProxyCheck(proxyCheckMsg{err: routeErr})
	if got := a.proxyStatusTail(); !strings.Contains(got, "retrying") {
		t.Fatalf("badge after recovery then one failure = %q", got)
	}
}
