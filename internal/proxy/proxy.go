// Package proxy parses proxy definitions and provides one cached HTTP client
// per proxy. SOCKS5 is handled by net/http's built-in support.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Types of proxies.
var Types = []string{"http", "https", "socks5"}

// Spec is a proxy endpoint with optional credentials.
type Spec struct {
	Type     string
	Host     string
	Port     int
	Username string
	Password string
}

// URL returns the proxy URL used by http.Transport.
func (s Spec) URL() *url.URL {
	u := &url.URL{Scheme: s.Type, Host: net.JoinHostPort(s.Host, strconv.Itoa(s.Port))}
	if s.Username != "" {
		u.User = url.UserPassword(s.Username, s.Password)
	}
	return u
}

// Identity identifies a proxy for duplicate detection.
func (s Spec) Identity() string {
	return fmt.Sprintf("%s|%s|%d|%s", s.Type, strings.ToLower(s.Host), s.Port, s.Username)
}

// ValidType reports whether t is a supported proxy type.
func ValidType(t string) bool {
	for _, v := range Types {
		if v == t {
			return true
		}
	}
	return false
}

// ParseLine parses one bulk-import line. Accepted formats:
//
//	scheme://user:pass@host:port
//	scheme://host:port
//	user:pass@host:port   (defaultType)
//	host:port             (defaultType)
//	host:port:user:pass   (defaultType; the password may contain ':')
func ParseLine(line, defaultType string) (Spec, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return Spec{}, errors.New("empty line")
	}
	if strings.ContainsAny(line, " \t") {
		return Spec{}, errors.New("unexpected whitespace")
	}
	if !strings.Contains(line, "://") {
		if strings.Contains(line, "@") || strings.HasPrefix(line, "[") {
			return parseURL(defaultType + "://" + line)
		}
		parts := strings.SplitN(line, ":", 4)
		switch len(parts) {
		case 2, 4:
		default:
			return Spec{}, errors.New("expected host:port or host:port:user:pass")
		}
		s := Spec{Type: defaultType, Host: parts[0]}
		if len(parts) == 4 {
			s.Username, s.Password = parts[2], parts[3]
			if s.Username == "" {
				return Spec{}, errors.New("empty username")
			}
		}
		port, err := parsePort(parts[1])
		if err != nil {
			return Spec{}, err
		}
		s.Port = port
		return s, validate(s)
	}
	return parseURL(line)
}

func parseURL(raw string) (Spec, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Spec{}, errors.New("not a valid proxy URL")
	}
	s := Spec{Type: strings.ToLower(u.Scheme), Host: u.Hostname()}
	if s.Type == "socks5h" {
		s.Type = "socks5"
	}
	if u.Path != "" && u.Path != "/" {
		return Spec{}, errors.New("unexpected path in proxy URL")
	}
	if u.User != nil {
		s.Username = u.User.Username()
		s.Password, _ = u.User.Password()
	}
	if u.Port() == "" {
		return Spec{}, errors.New("missing port")
	}
	port, err := parsePort(u.Port())
	if err != nil {
		return Spec{}, err
	}
	s.Port = port
	return s, validate(s)
}

func parsePort(v string) (int, error) {
	p, err := strconv.Atoi(v)
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("invalid port %q", v)
	}
	return p, nil
}

func validate(s Spec) error {
	if !ValidType(s.Type) {
		return fmt.Errorf("unsupported scheme %q (use http, https or socks5)", s.Type)
	}
	if s.Host == "" {
		return errors.New("missing host")
	}
	return nil
}

// Assign distributes proxies over keys round-robin, in the given order.
// It returns key ID -> proxy ID. With one proxy every key gets it.
func Assign(keyIDs, proxyIDs []int64) map[int64]int64 {
	out := make(map[int64]int64, len(keyIDs))
	if len(proxyIDs) == 0 {
		return out
	}
	for i, k := range keyIDs {
		out[k] = proxyIDs[i%len(proxyIDs)]
	}
	return out
}

// Clients caches one *http.Client per proxy URL (and one for direct connections).
// Clients have no overall timeout: callers bound requests with contexts.
type Clients struct {
	mu      sync.Mutex
	clients map[string]*http.Client
}

// NewClients returns an empty cache.
func NewClients() *Clients { return &Clients{clients: map[string]*http.Client{}} }

// Get returns the client for proxy u; nil means a direct connection.
func (c *Clients) Get(u *url.URL) *http.Client {
	key := ""
	if u != nil {
		key = u.String()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if cl, ok := c.clients[key]; ok {
		return cl
	}
	cl := &http.Client{Transport: newTransport(u)}
	c.clients[key] = cl
	return cl
}

// Reset drops all cached clients (after proxies were edited or deleted).
func (c *Clients) Reset() {
	c.mu.Lock()
	old := c.clients
	c.clients = map[string]*http.Client{}
	c.mu.Unlock()
	for _, cl := range old {
		cl.CloseIdleConnections()
	}
}

func newTransport(u *url.URL) *http.Transport {
	t := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	if u != nil {
		t.Proxy = http.ProxyURL(u)
	}
	return t
}

// TestResult is the outcome of a connectivity test.
type TestResult struct {
	OK         bool   `json:"ok"`
	StatusCode int    `json:"status_code"`
	LatencyMS  int64  `json:"latency_ms"`
	Error      string `json:"error"`
}

// Test requests target with client and reports latency. A response counts as
// OK unless it is a 5xx or a 407 (proxy authentication required).
func Test(ctx context.Context, client *http.Client, target string) TestResult {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return TestResult{Error: "invalid test URL: " + err.Error()}
	}
	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return TestResult{LatencyMS: latency, Error: err.Error()}
	}
	resp.Body.Close()
	r := TestResult{StatusCode: resp.StatusCode, LatencyMS: latency}
	r.OK = resp.StatusCode < 500 && resp.StatusCode != http.StatusProxyAuthRequired
	if !r.OK {
		r.Error = resp.Status
	}
	return r
}
