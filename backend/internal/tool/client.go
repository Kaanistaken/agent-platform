package tool

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"
)

// Limits (D74, D80). The 128 KB model limit is reviewed before delivery (D95).
const (
	CallTimeout      = 2 * time.Minute
	MaxResponseBytes = 10 << 20  // read from the network
	MaxModelBytes    = 128 << 10 // sent to the model and stored
)

// Secret is a tool secret resolved for one request: its value and the host it
// is bound to (D32).
type Secret struct {
	Value string
	Host  string
}

// Result is what came back from a tool call, already masked and truncated.
type Result struct {
	Status int    // HTTP status; 0 if no response
	Text   string // what the model sees and what is stored
}

// Failure explains why a call did not produce a response.
type Failure struct {
	Code    string // reason code, e.g. host_mismatch, address_refused, timeout
	Msg     string
	MaySent bool // the request may have reached the server (D63)
}

func (f *Failure) Error() string { return f.Msg }

// Client sends tool requests under the platform's rules (D81, D94): https
// only, port 443, no private or special addresses (checked on the IP actually
// dialled, so DNS rebinding is covered), no proxy, no redirects.
type Client struct {
	http *http.Client
}

// NewClient returns the production client. Nothing can switch its address
// policy off: no environment variable, flag or API (D110).
func NewClient() *Client {
	return newClient(checkAddress, nil, nil)
}

// newClient is the only constructor. Tests in this package pass a dial
// mapping and a trusted test certificate; production code cannot.
func newClient(check func(netip.AddrPort) error, redirect func(string) string, tlsCfg *tls.Config) *Client {
	dialer := &net.Dialer{
		Timeout: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return &Failure{Code: "address_refused", Msg: "address refused: " + address}
			}
			if err := check(ap); err != nil {
				return err
			}
			return nil
		},
	}
	dial := dialer.DialContext
	if redirect != nil {
		// Test only: keep the logical host:443 but connect to the test server.
		dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, redirect(addr))
		}
	}
	transport := &http.Transport{
		Proxy:                 nil, // proxy environment variables are ignored (D81)
		DialContext:           dial,
		TLSClientConfig:       tlsCfg,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: CallTimeout,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &Client{http: &http.Client{
		Transport: transport,
		// Redirects are not followed; the 3xx response goes to the model (D81).
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// special-use ranges that netip's helpers do not cover (D94).
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
}

// checkAddress is the production address policy.
func checkAddress(ap netip.AddrPort) error {
	if ap.Port() != 443 {
		return &Failure{Code: "address_refused", Msg: "only port 443 is allowed"}
	}
	ip := ap.Addr().Unmap() // IPv4-mapped IPv6 is checked as IPv4
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() ||
		ip.IsUnspecified() || ip == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
		return &Failure{Code: "address_refused", Msg: "destination address is not allowed: " + ip.String()}
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return &Failure{Code: "address_refused", Msg: "destination address is not allowed: " + ip.String()}
		}
	}
	return nil
}

// Do sends the request. secrets maps each referenced secret name to the
// starter's secret (D32); a missing secret or a host mismatch fails before
// anything is sent. GET is retried once on network errors, 5xx and 429 (D91).
func (c *Client) Do(ctx context.Context, r Request, secrets map[string]Secret) (Result, error) {
	u, err := url.Parse(r.URL)
	if err != nil || u.Scheme != "https" {
		return Result{}, &Failure{Code: "address_refused", Msg: "invalid URL"}
	}
	host, err := NormalizeHost(u.Host)
	if err != nil || host != r.Host {
		return Result{}, &Failure{Code: "address_refused", Msg: "invalid host"}
	}

	var values []string
	for _, h := range r.Headers {
		if h.Secret == "" {
			continue
		}
		s, ok := secrets[h.Secret]
		if !ok || s.Value == "" {
			return Result{}, &Failure{Code: "secret_missing", Msg: fmt.Sprintf("you have no secret named %s for host %s", h.Secret, host)}
		}
		// The secret-attaching step re-checks the final host (D81).
		if s.Host != host {
			return Result{}, &Failure{Code: "host_mismatch", Msg: fmt.Sprintf("secret %s is bound to %s, not %s", h.Secret, s.Host, host)}
		}
		values = append(values, s.Value)
	}

	attempts := 1
	if r.Method == http.MethodGet {
		attempts = 2
	}
	var res Result
	for i := 0; i < attempts; i++ {
		res, err = c.once(ctx, r, secrets)
		retryable := false
		var f *Failure
		if errors.As(err, &f) {
			retryable = f.Code == "network" || f.Code == "timeout"
		} else if err == nil {
			retryable = res.Status >= 500 || res.Status == 429
		}
		if !retryable || i == attempts-1 || ctx.Err() != nil {
			break
		}
	}
	if err != nil {
		var f *Failure
		if errors.As(err, &f) {
			f.Msg = Mask(f.Msg, values)
		}
		return Result{}, err
	}
	res.Text = Mask(res.Text, values)
	res.Text = Truncate(res.Text, MaxModelBytes, -1)
	return res, nil
}

func (c *Client) once(ctx context.Context, r Request, secrets map[string]Secret) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()

	var wrote atomic.Bool // set by the transport's goroutine
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		WroteHeaders: func() { wrote.Store(true) },
	})
	var body io.Reader
	if r.Body != "" {
		body = strings.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, body)
	if err != nil {
		return Result{}, &Failure{Code: "address_refused", Msg: "invalid request"}
	}
	req.Header.Set("User-Agent", "vodafone-case-agent-platform")
	for _, h := range r.Headers {
		if h.Secret != "" {
			// The secret is attached only here, in memory (D33).
			req.Header.Set(h.Name, h.Prefix+secrets[h.Secret].Value)
		} else {
			req.Header.Set(h.Name, h.Value)
		}
	}

	resp, err := c.http.Do(req)
	if err != nil {
		var f *Failure
		if errors.As(err, &f) {
			return Result{}, f // refused by the address policy: nothing was sent
		}
		code := "network"
		if errors.Is(err, context.DeadlineExceeded) {
			code = "timeout"
		}
		if errors.Is(err, context.Canceled) {
			code = "interrupted"
		}
		return Result{}, &Failure{Code: code, Msg: "request failed: " + err.Error(), MaySent: wrote.Load()}
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil && len(data) == 0 {
		return Result{}, &Failure{Code: "network", Msg: "reading the response failed: " + err.Error(), MaySent: true}
	}
	return Result{Status: resp.StatusCode, Text: formatResponse(resp, data)}, nil
}

// formatResponse is what the model sees: status line, content type and body.
// Non-text bodies are described by type and size only (D80).
func formatResponse(resp *http.Response, data []byte) string {
	ct := resp.Header.Get("Content-Type")
	var b strings.Builder
	fmt.Fprintf(&b, "HTTP %s\n", resp.Status)
	if ct != "" {
		fmt.Fprintf(&b, "Content-Type: %s\n", ct)
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		fmt.Fprintf(&b, "Location: %s\n", loc)
	}
	b.WriteString("\n")
	over := len(data) > MaxResponseBytes
	if over {
		data = data[:MaxResponseBytes]
	}
	if !isText(ct, data) {
		fmt.Fprintf(&b, "[non-text response: %s, %d bytes]", ct, len(data))
		return b.String()
	}
	b.Write(data)
	if over {
		fmt.Fprintf(&b, "\n[truncated after %d bytes]", MaxResponseBytes)
	}
	return b.String()
}

func isText(ct string, data []byte) bool {
	if ct == "" {
		return utf8.Valid(data)
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return utf8.Valid(data)
	}
	return strings.HasPrefix(mt, "text/") || strings.Contains(mt, "json") || strings.Contains(mt, "xml") ||
		strings.Contains(mt, "javascript") || mt == "application/x-www-form-urlencoded"
}

// Mask replaces each secret value, and its base64 and URL-encoded forms, with
// *** (D28, D33). It runs before truncation (D94).
func Mask(s string, values []string) string {
	for _, v := range values {
		if v == "" {
			continue
		}
		forms := []string{
			v,
			base64.StdEncoding.EncodeToString([]byte(v)),
			base64.RawStdEncoding.EncodeToString([]byte(v)),
			base64.URLEncoding.EncodeToString([]byte(v)),
			base64.RawURLEncoding.EncodeToString([]byte(v)),
			url.QueryEscape(v),
			url.PathEscape(v),
		}
		for _, f := range forms {
			if f != "" {
				s = strings.ReplaceAll(s, f, "***")
			}
		}
	}
	return s
}

// Truncate cuts s to at most max bytes without splitting a UTF-8 character
// and appends a note with the original size (total < 0 means use len(s)).
func Truncate(s string, max int, total int) string {
	if len(s) <= max {
		return s
	}
	if total < 0 {
		total = len(s)
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n[truncated: %d of %d bytes]", cut, total)
}
