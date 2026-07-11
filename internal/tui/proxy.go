package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/proxy"
)

const proxyCheckInterval = 5 * time.Minute

type proxyBadgeState uint8

const (
	proxyBadgeNone proxyBadgeState = iota
	proxyBadgeUnverified
	proxyBadgeTor
	proxyBadgeSOCKS
	proxyBadgeRetrying
	proxyBadgeUnreachable
	proxyBadgeUnavailable
)

type proxyBadge struct {
	state         proxyBadgeState
	routeFailures int
	nextCheck     time.Time
	checking      bool
}

type proxyChecker func(context.Context, *proxy.Runtime) (proxy.Egress, error)

func defaultProxyChecker(ctx context.Context, runtime *proxy.Runtime) (proxy.Egress, error) {
	return runtime.VerifyEgress(ctx)
}

func (a *App) proxyCheckDue(now time.Time) bool {
	runtime := a.cfg.ProxyRuntime()
	if runtime == nil || !runtime.Enabled() || a.proxy.checking || !a.hasActiveTransfers() {
		return false
	}
	return a.proxy.nextCheck.IsZero() || !now.Before(a.proxy.nextCheck)
}

func (a *App) startProxyCheck(now time.Time) tea.Cmd {
	if !a.proxyCheckDue(now) {
		return nil
	}
	a.proxy.checking = true
	runtime := a.cfg.ProxyRuntime()
	check := a.proxyCheck
	if check == nil {
		check = defaultProxyChecker
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		egress, err := check(ctx, runtime)
		return proxyCheckMsg{isTor: egress.IsTor, err: err}
	}
}

func (a *App) onProxyCheck(msg proxyCheckMsg) {
	a.proxy.checking = false
	a.proxy.nextCheck = time.Now().Add(proxyCheckInterval)
	if msg.err == nil {
		a.proxy.routeFailures = 0
		if msg.isTor {
			a.proxy.state = proxyBadgeTor
		} else {
			a.proxy.state = proxyBadgeSOCKS
		}
		return
	}
	if proxy.IsRouteFailure(msg.err) {
		a.proxy.routeFailures++
		if a.proxy.routeFailures >= 2 {
			a.proxy.state = proxyBadgeUnreachable
		} else {
			a.proxy.state = proxyBadgeRetrying
		}
		return
	}
	// The SOCKS route may be fine when only the third-party check endpoint is
	// unavailable. Treat that as an honest lack of proof, not a traffic leak.
	// routeFailures deliberately survives this: only a success clears it, so
	// a route that alternates failure kinds still escalates to UNREACHABLE.
	a.proxy.state = proxyBadgeUnavailable
}

func (a *App) hasActiveTransfers() bool {
	for _, s := range a.downloads.snaps {
		switch s.State {
		case engine.StateFetchingMeta, engine.StatePreviewing, engine.StateDownloading, engine.StateSeeding:
			return true
		}
	}
	return false
}

func (a *App) proxyStatusTail() string {
	switch a.proxy.state {
	case proxyBadgeUnverified:
		return styleFaint.Render("SOCKS strict · unverified")
	case proxyBadgeTor:
		return styleBrand.Render("((o)) Tor strict")
	case proxyBadgeSOCKS:
		return styleBrand.Render("SOCKS strict")
	case proxyBadgeRetrying:
		return styleBest.Render("SOCKS strict · retrying")
	case proxyBadgeUnreachable:
		return styleErr.Render("⚠ PROXY UNREACHABLE · strict mode remains on")
	case proxyBadgeUnavailable:
		return styleBest.Render("SOCKS strict · check unavailable")
	default:
		return ""
	}
}
