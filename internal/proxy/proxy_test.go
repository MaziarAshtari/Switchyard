package proxy

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseLine(t *testing.T) {
	cases := []struct {
		in      string
		def     string
		want    Spec
		wantErr string
	}{
		{in: "socks5://user:p%40ss@1.2.3.4:1080", def: "http", want: Spec{"socks5", "1.2.3.4", 1080, "user", "p@ss"}},
		{in: "http://proxy.example.com:8080", def: "socks5", want: Spec{Type: "http", Host: "proxy.example.com", Port: 8080}},
		{in: "HTTPS://h:443", def: "http", want: Spec{Type: "https", Host: "h", Port: 443}},
		{in: "socks5h://h:1080", def: "http", want: Spec{Type: "socks5", Host: "h", Port: 1080}},
		{in: "1.2.3.4:3128", def: "http", want: Spec{Type: "http", Host: "1.2.3.4", Port: 3128}},
		{in: "  1.2.3.4:3128  ", def: "socks5", want: Spec{Type: "socks5", Host: "1.2.3.4", Port: 3128}},
		{in: "1.2.3.4:3128:bob:se:cret", def: "socks5", want: Spec{"socks5", "1.2.3.4", 3128, "bob", "se:cret"}},
		{in: "bob:pw@1.2.3.4:3128", def: "http", want: Spec{"http", "1.2.3.4", 3128, "bob", "pw"}},
		{in: "[2001:db8::1]:8080", def: "http", want: Spec{Type: "http", Host: "2001:db8::1", Port: 8080}},
		{in: "", def: "http", wantErr: "empty line"},
		{in: "1.2.3.4", def: "http", wantErr: "expected host:port"},
		{in: "1.2.3.4:3128:bob", def: "http", wantErr: "expected host:port"},
		{in: "1.2.3.4:99999", def: "http", wantErr: "invalid port"},
		{in: "1.2.3.4:abc", def: "http", wantErr: "invalid port"},
		{in: "ftp://1.2.3.4:21", def: "http", wantErr: "unsupported scheme"},
		{in: "http://1.2.3.4", def: "http", wantErr: "missing port"},
		{in: "http://1.2.3.4:80/path", def: "http", wantErr: "unexpected path"},
		{in: ":8080", def: "http", wantErr: "missing host"},
		{in: "1.2.3.4:80::pw", def: "http", wantErr: "empty username"},
		{in: "1.2.3.4 8080", def: "http", wantErr: "whitespace"},
	}
	for _, c := range cases {
		got, err := ParseLine(c.in, c.def)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("ParseLine(%q) error = %v, want %q", c.in, err, c.wantErr)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("ParseLine(%q) = %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
}

func TestSpecURLAndIdentity(t *testing.T) {
	s := Spec{Type: "socks5", Host: "h", Port: 1080, Username: "u", Password: "p:w"}
	if got := s.URL().String(); got != "socks5://u:p%3Aw@h:1080" {
		t.Fatalf("URL = %s", got)
	}
	a := Spec{Type: "http", Host: "Example.com", Port: 80}
	b := Spec{Type: "http", Host: "example.com", Port: 80, Password: "different"}
	if a.Identity() != b.Identity() {
		t.Fatal("identity should ignore host case and password")
	}
}

func TestAssignRoundRobin(t *testing.T) {
	got := Assign([]int64{1, 2, 3, 4, 5}, []int64{10, 20})
	want := map[int64]int64{1: 10, 2: 20, 3: 10, 4: 20, 5: 10}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("Assign = %v, want %v", got, want)
		}
	}
	if len(Assign([]int64{1}, nil)) != 0 {
		t.Fatal("no proxies should assign nothing")
	}
}

func TestClientsCache(t *testing.T) {
	c := NewClients()
	u1, _ := url.Parse("http://a:1")
	u2, _ := url.Parse("socks5://b:2")
	if c.Get(nil) != c.Get(nil) || c.Get(u1) != c.Get(u1) {
		t.Fatal("expected cached clients")
	}
	if c.Get(u1) == c.Get(u2) || c.Get(nil) == c.Get(u1) {
		t.Fatal("different proxies must use different clients")
	}
	before := c.Get(u1)
	c.Reset()
	if c.Get(u1) == before {
		t.Fatal("Reset should drop cached clients")
	}
}

// TestHTTPProxyIsUsed checks that requests go through an HTTP proxy with basic auth.
func TestHTTPProxyIsUsed(t *testing.T) {
	var sawAuth atomic.Value
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth.Store(r.Header.Get("Proxy-Authorization"))
		if r.URL.Host != "upstream.invalid" {
			http.Error(w, "unexpected target "+r.URL.String(), http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer proxySrv.Close()
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(proxySrv.URL, "http://"))
	p, _ := strconv.Atoi(port)
	spec := Spec{Type: "http", Host: host, Port: p, Username: "u", Password: "pw"}

	res := Test(context.Background(), NewClients().Get(spec.URL()), "http://upstream.invalid/generate_204")
	if !res.OK || res.StatusCode != http.StatusNoContent {
		t.Fatalf("Test = %+v", res)
	}
	if auth, _ := sawAuth.Load().(string); auth == "" {
		t.Fatal("proxy did not receive credentials")
	}
}

func TestReportsFailureForUnreachableProxy(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().(*net.TCPAddr)
	l.Close() // nothing listens here any more
	spec := Spec{Type: "socks5", Host: "127.0.0.1", Port: addr.Port}
	res := Test(context.Background(), NewClients().Get(spec.URL()), "http://example.invalid/")
	if res.OK || res.Error == "" {
		t.Fatalf("expected failure, got %+v", res)
	}
}

// TestSOCKS5ProxyWithAuth runs a minimal SOCKS5 server (RFC 1928/1929) that
// requires username/password and forwards the connection to a local upstream.
func TestSOCKS5ProxyWithAuth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "via socks")
	}))
	defer upstream.Close()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var authed atomic.Bool
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go serveSOCKS5(t, conn, "alice", "s3cret", &authed)
		}
	}()

	spec := Spec{Type: "socks5", Host: "127.0.0.1", Port: l.Addr().(*net.TCPAddr).Port, Username: "alice", Password: "s3cret"}
	resp, err := NewClients().Get(spec.URL()).Get(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "via socks" || !authed.Load() {
		t.Fatalf("body=%q authed=%v", body, authed.Load())
	}

	bad := spec
	bad.Password = "wrong"
	if _, err := NewClients().Get(bad.URL()).Get(upstream.URL); err == nil {
		t.Fatal("expected authentication failure")
	}
}

func serveSOCKS5(t *testing.T, c net.Conn, user, pass string, authed *atomic.Bool) {
	defer c.Close()
	buf := make([]byte, 512)
	// Greeting: VER NMETHODS METHODS...
	if _, err := io.ReadFull(c, buf[:2]); err != nil {
		return
	}
	io.ReadFull(c, buf[:buf[1]])
	c.Write([]byte{5, 2}) // username/password
	// Auth: VER ULEN UNAME PLEN PASSWD
	io.ReadFull(c, buf[:2])
	u := make([]byte, buf[1])
	io.ReadFull(c, u)
	io.ReadFull(c, buf[:1])
	p := make([]byte, buf[0])
	io.ReadFull(c, p)
	if string(u) != user || string(p) != pass {
		c.Write([]byte{1, 1})
		return
	}
	authed.Store(true)
	c.Write([]byte{1, 0})
	// Request: VER CMD RSV ATYP DST.ADDR DST.PORT
	io.ReadFull(c, buf[:4])
	var host string
	switch buf[3] {
	case 1:
		io.ReadFull(c, buf[:4])
		host = net.IP(buf[:4]).String()
	case 3:
		io.ReadFull(c, buf[:1])
		name := make([]byte, buf[0])
		io.ReadFull(c, name)
		host = string(name)
	default:
		return
	}
	io.ReadFull(c, buf[:2])
	port := binary.BigEndian.Uint16(buf[:2])
	dst, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		c.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer dst.Close()
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	go io.Copy(dst, c)
	io.Copy(c, dst)
}
