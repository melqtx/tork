// Package proxy builds the one outbound SOCKS5 route tork can use. It keeps
// the proxy's credentials out of diagnostics and gives every caller the same
// context-aware TCP dialer.
package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	xproxy "golang.org/x/net/proxy"
)

// Runtime is an optional SOCKS5 route. A zero Runtime means direct networking.
type Runtime struct {
	enabled        bool
	hasCredentials bool
	endpoint       string
	contextDialer  xproxy.ContextDialer
}

// EgressCheckURL is the Tor Project endpoint used for an intentional proxy
// verification. It reports the address seen by the service and whether it is
// a Tor exit; it does not attempt to identify the user's normal connection.
const EgressCheckURL = "https://check.torproject.org/api/ip"

// Egress is the deliberately small result of a proxy verification.
type Egress struct {
	IP    string
	IsTor bool
}

// EgressFailure says whether a failed verification could not establish its
// SOCKS route, or reached the route but could not use the verification service.
type EgressFailure int

const (
	EgressRouteFailure EgressFailure = iota
	EgressServiceFailure
)

// EgressError never includes a configured proxy URL or its credentials.
type EgressError struct {
	kind EgressFailure
	err  error
}

func (e *EgressError) Error() string {
	if e == nil || e.kind == EgressRouteFailure {
		return "proxy route unavailable"
	}
	return "proxy verification service unavailable"
}

func (e *EgressError) Unwrap() error { return e.err }

// IsRouteFailure reports whether err means a SOCKS route could not be opened.
// Callers use this to show a strong warning without interpreting a third-party
// check endpoint outage as a leak or a dead proxy.
func IsRouteFailure(err error) bool {
	var e *EgressError
	return errors.As(err, &e) && e.kind == EgressRouteFailure
}

// New validates raw and builds a SOCKS5 runtime. socks5 and socks5h both send
// the destination hostname to the proxy, so destination DNS is remote.
func New(raw string) (*Runtime, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return &Runtime{}, nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		// url.Parse includes its input in some errors. Never echo a URL that
		// may contain the user's password.
		return nil, fmt.Errorf("invalid SOCKS5 proxy URL")
	}
	if u.Scheme != "socks5" && u.Scheme != "socks5h" {
		return nil, fmt.Errorf("proxy scheme must be socks5 or socks5h")
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("SOCKS5 proxy host is required")
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("SOCKS5 proxy must not include a path, query, or fragment")
	}
	if u.Port() != "" {
		port, err := strconv.ParseUint(u.Port(), 10, 16)
		if err != nil || port == 0 {
			return nil, fmt.Errorf("SOCKS5 proxy port must be between 1 and 65535")
		}
	}
	if u.User != nil {
		username := u.User.Username()
		if username == "" {
			return nil, fmt.Errorf("SOCKS5 proxy username is empty")
		}
		if len(username) > 255 {
			return nil, fmt.Errorf("SOCKS5 proxy username must be at most 255 bytes")
		}
		if password, _ := u.User.Password(); len(password) > 255 {
			return nil, fmt.Errorf("SOCKS5 proxy password must be at most 255 bytes")
		}
	}

	dialer, err := xproxy.FromURL(u, &net.Dialer{})
	if err != nil {
		return nil, fmt.Errorf("configure SOCKS5 proxy")
	}
	contextDialer, ok := dialer.(xproxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("SOCKS5 proxy dialer does not support cancellation")
	}

	port := u.Port()
	if port == "" {
		port = "1080"
	}
	return &Runtime{
		enabled:        true,
		hasCredentials: u.User != nil,
		endpoint:       net.JoinHostPort(u.Hostname(), port),
		contextDialer:  contextDialer,
	}, nil
}

func (r *Runtime) Enabled() bool { return r != nil && r.enabled }

func (r *Runtime) HasCredentials() bool { return r != nil && r.hasCredentials }

// Endpoint is safe to display: it contains neither username nor password.
func (r *Runtime) Endpoint() string {
	if !r.Enabled() {
		return ""
	}
	return r.endpoint
}

// DialContext always routes through SOCKS5 when enabled. The zero runtime is
// deliberately direct for callers that use it outside proxy mode.
func (r *Runtime) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if !r.Enabled() {
		var direct net.Dialer
		return direct.DialContext(ctx, network, addr)
	}
	return r.contextDialer.DialContext(ctx, network, addr)
}

// Dial implements the torrent client's dialer interface.
func (r *Runtime) Dial(ctx context.Context, addr string) (net.Conn, error) {
	return r.DialContext(ctx, "tcp", addr)
}

func (r *Runtime) DialerNetwork() string { return "tcp" }

// HTTPClient returns a fresh client whose transport cannot fall back to
// HTTP_PROXY, HTTPS_PROXY, or ALL_PROXY. Callers choose their own timeouts.
func (r *Runtime) HTTPClient(timeout, responseHeaderTimeout time.Duration) *http.Client {
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           r.DialContext,
		ResponseHeaderTimeout: responseHeaderTimeout,
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

// VerifyEgress makes one bounded HTTPS request through this runtime's SOCKS
// transport, never directly. Doctor does so only on request, and the TUI only
// while a transfer is active.
func (r *Runtime) VerifyEgress(ctx context.Context) (Egress, error) {
	return r.VerifyEgressAt(ctx, EgressCheckURL)
}

// VerifyEgressAt is VerifyEgress against endpoint. It exists so tests can use
// a local service; production callers use VerifyEgress.
func (r *Runtime) VerifyEgressAt(ctx context.Context, endpoint string) (Egress, error) {
	if !r.Enabled() {
		return Egress{}, &EgressError{kind: EgressRouteFailure}
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Egress{}, &EgressError{kind: EgressServiceFailure, err: err}
	}
	resp, err := r.HTTPClient(0, 8*time.Second).Do(req)
	if err != nil {
		kind := EgressServiceFailure
		if isSOCKSRouteError(err) {
			kind = EgressRouteFailure
		}
		return Egress{}, &EgressError{kind: kind, err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return Egress{}, &EgressError{kind: EgressServiceFailure}
	}
	var payload struct {
		IP    string `json:"IP"`
		IsTor bool   `json:"IsTor"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&payload); err != nil {
		return Egress{}, &EgressError{kind: EgressServiceFailure, err: err}
	}
	if _, err := netip.ParseAddr(payload.IP); err != nil {
		return Egress{}, &EgressError{kind: EgressServiceFailure, err: err}
	}
	return Egress{IP: payload.IP, IsTor: payload.IsTor}, nil
}

// isSOCKSRouteError reports whether err means the SOCKS route itself is
// unusable. x/net wraps every SOCKS-stage failure in a net.OpError whose Op
// starts with "socks". Inside that wrapper, a nested net.OpError means the
// TCP connection to the proxy endpoint failed (the dial, or a read/write
// during the handshake), and negotiation errors name the authentication or
// protocol problem: both mean the route is down. The proxy's reply about the
// target ("unknown error host unreachable") is a plain error and means the
// route worked but the destination did not - a service failure, so a check
// endpoint outage is never reported as a dead proxy.
func isSOCKSRouteError(err error) bool {
	var opErr *net.OpError
	if !errors.As(err, &opErr) || !strings.HasPrefix(opErr.Op, "socks") {
		return false
	}
	var nested *net.OpError
	if errors.As(opErr.Err, &nested) {
		return true
	}
	msg := opErr.Err.Error()
	return strings.Contains(msg, "authentication") ||
		strings.Contains(msg, "username/password") ||
		strings.Contains(msg, "protocol version")
}
