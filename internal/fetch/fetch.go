// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package fetch is the one outbound HTTP fetcher for a URL a person supplied.
//
// The server and the CLI both resolve a paper reference by fetching whatever
// it points at, and a URL out of a request body is an instruction to this
// process to open a connection somewhere. The fetcher therefore settles three
// questions once, for every caller, rather than leaving each call site to
// remember them:
//
//   - where it may connect: public addresses only. A loopback, private,
//     link-local, or cloud-metadata address is refused at dial time, after
//     the name has resolved, so a hostname that resolves to one is caught as
//     surely as a literal;
//   - how far it may follow: a bounded number of redirects, each dialled
//     through the same check;
//   - how much it may read: a body over the cap is an error, never a
//     truncated success.
//
// It carries no proxy. A proxy would be dialled in place of the target and
// the check above would judge the proxy, not where the request ends up.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	xotel "latere.ai/x/pkg/otel"
)

// DefaultMaxBytes bounds a fetched body. A paper landing page or a metadata
// record is kilobytes; eight megabytes leaves room for a large PDF without
// letting one URL fill the process.
const DefaultMaxBytes int64 = 8 << 20

// DefaultTimeout bounds one fetch, redirects included.
const DefaultTimeout = 30 * time.Second

// maxRedirects is how many hops a fetch follows before it gives up.
const maxRedirects = 5

var (
	// ErrTooLarge reports a body over the cap.
	ErrTooLarge = errors.New("fetch: body exceeds the size cap")
	// ErrForbiddenAddress reports a URL that resolves to an address this
	// process must not reach out to.
	ErrForbiddenAddress = errors.New("fetch: address is not public")
	// ErrScheme reports a URL that is not http or https.
	ErrScheme = errors.New("fetch: scheme is not http or https")
	// ErrTooManyRedirects reports a chain longer than the fetcher follows.
	ErrTooManyRedirects = errors.New("fetch: too many redirects")
)

// StatusError reports a response that was not 200. Retryable says whether a
// later attempt could see a different answer.
type StatusError struct {
	URL  string
	Code int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s returned %d %s", e.URL, e.Code, http.StatusText(e.Code))
}

// Retryable is true for a rate limit or a server-side failure.
func (e *StatusError) Retryable() bool {
	return e.Code == http.StatusTooManyRequests || e.Code >= 500
}

// Retryable reports whether err is the kind of failure a later attempt can
// recover from: a timeout, a network error, or a 429 or 5xx status. A refused
// address, a body over the cap, or a 4xx is not.
func Retryable(err error) bool {
	var status *StatusError
	if errors.As(err, &status) {
		return status.Retryable()
	}
	if errors.Is(err, ErrTooLarge) || errors.Is(err, ErrForbiddenAddress) || errors.Is(err, ErrScheme) || errors.Is(err, ErrTooManyRedirects) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// Client fetches URLs under the package's rules. The zero value is not usable;
// build one with New.
type Client struct {
	http      *http.Client
	maxBytes  int64
	timeout   time.Duration
	userAgent string
	// permit judges a resolved address. The default admits public addresses
	// only; tests widen it to reach a server on loopback.
	permit func(ip net.IP) bool
	// resolve turns a host into addresses; swapped in tests.
	resolve func(ctx context.Context, host string) ([]net.IPAddr, error)
}

// Option configures a Client.
type Option func(*Client)

// WithMaxBytes caps the body.
func WithMaxBytes(n int64) Option { return func(c *Client) { c.maxBytes = n } }

// WithTimeout bounds one fetch, redirects included.
func WithTimeout(d time.Duration) Option { return func(c *Client) { c.timeout = d } }

// AllowPrivate admits loopback and private addresses. A test reaching an
// httptest server needs it; nothing serving people does.
func AllowPrivate() Option { return func(c *Client) { c.permit = func(net.IP) bool { return true } } }

// New builds a Client. The transport is instrumented like every other
// outbound client in the process, over a dialler that checks each address.
func New(opts ...Option) *Client {
	c := &Client{
		maxBytes:  DefaultMaxBytes,
		timeout:   DefaultTimeout,
		userAgent: "replichai",
		permit:    Public,
		resolve:   net.DefaultResolver.LookupIPAddr,
	}
	for _, opt := range opts {
		opt(c)
	}
	base := &http.Transport{
		Proxy:               nil,
		DialContext:         c.dial,
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   true,
	}
	c.http = &http.Client{
		Transport: xotel.Transport(base),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return ErrTooManyRedirects
			}
			return checkScheme(req.URL)
		},
	}
	return c
}

// Fetch GETs url and returns its body. It satisfies paper.Fetcher.
func (c *Client) Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("fetch %q: %w", rawURL, err)
	}
	if err := checkScheme(u); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, &StatusError{URL: rawURL, Code: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > c.maxBytes {
		return nil, fmt.Errorf("%w: %s is over %d bytes", ErrTooLarge, rawURL, c.maxBytes)
	}
	return body, nil
}

// dial resolves the host, refuses any address the client may not reach, and
// connects to a checked address by its literal, so the address judged is the
// address dialled.
func (c *Client) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	addrs, err := c.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("fetch: %s resolves to no address", host)
	}
	for _, a := range addrs {
		if !c.permit(a.IP) {
			return nil, fmt.Errorf("%w: %s resolves to %s", ErrForbiddenAddress, host, a.IP)
		}
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	var lastErr error
	for _, a := range addrs {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(a.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// Public reports whether ip is one a process serving the internet may reach
// out to: not loopback, not private, not link-local (which is where cloud
// metadata services live), not multicast, not unspecified.
func Public(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		// 0.0.0.0/8 (this network) and 100.64.0.0/10 (carrier NAT) are not
		// public either, and net.IP has no predicate for them.
		if v4[0] == 0 || (v4[0] == 100 && v4[1]&0xc0 == 64) {
			return false
		}
	}
	return true
}

func checkScheme(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: %q", ErrScheme, u.Scheme)
	}
	return nil
}
