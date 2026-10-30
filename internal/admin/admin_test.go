package admin

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/MaziarAshtari/Switchyard/internal/db"
	"github.com/MaziarAshtari/Switchyard/internal/gateway"
	"github.com/MaziarAshtari/Switchyard/internal/proxy"
	"github.com/MaziarAshtari/Switchyard/internal/routing"
	"github.com/MaziarAshtari/Switchyard/internal/secret"
	"github.com/MaziarAshtari/Switchyard/internal/store"
)

type testEnv struct {
	t   *testing.T
	srv *httptest.Server
	st  *store.Store
	api *API
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "admin.db"))
	if err != nil {
		t.Fatal(err)
	}
	box, _ := secret.NewBox("admin-test-key-0123456")
	st := store.New(d, box)
	if err := st.SetPassword(context.Background(), "correct-password"); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	logs := gateway.NewLogWriter(st, log)
	clients := proxy.NewClients()
	gw := gateway.New(st, routing.NewRouter(time.Now, rand.New(rand.NewPCG(1, 1))), clients, logs, log, "test")
	api := New(st, gw, clients, log, "test")
	r := chi.NewRouter()
	api.Routes(r)
	srv := httptest.NewServer(r)
	t.Cleanup(func() { srv.Close(); logs.Close(); d.Close() })
	return &testEnv{t: t, srv: srv, st: st, api: api}
}

func (e *testEnv) client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func (e *testEnv) call(c *http.Client, method, path, body string, headers ...string) (int, map[string]any, []byte) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	json.Unmarshal(raw, &out)
	return resp.StatusCode, out, raw
}

func (e *testEnv) login() *http.Client {
	e.t.Helper()
	c := e.client()
	if status, _, raw := e.call(c, "POST", "/api/auth/login", `{"password":"correct-password"}`); status != http.StatusNoContent {
		e.t.Fatalf("login failed: %d %s", status, raw)
	}
	return c
}

func TestLoginSessionAndLogout(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	if status, _, _ := e.call(c, "GET", "/api/providers", ""); status != 401 {
		t.Fatalf("unauthenticated request: %d", status)
	}
	if status, body, _ := e.call(c, "POST", "/api/auth/login", `{"password":"nope"}`); status != 401 || body["error"] != "Wrong password" {
		t.Fatalf("wrong password: %d %v", status, body)
	}
	c = e.login()
	u, _ := http.NewRequest("GET", e.srv.URL, nil)
	cookies := c.Jar.Cookies(u.URL)
	if len(cookies) != 1 || cookies[0].Name != sessionCookie {
		t.Fatalf("cookies: %+v", cookies)
	}
	if status, body, _ := e.call(c, "GET", "/api/auth/me", ""); status != 200 || body["version"] != "test" {
		t.Fatalf("me: %d %v", status, body)
	}
	if status, _, _ := e.call(c, "POST", "/api/auth/logout", ""); status != 204 {
		t.Fatalf("logout: %d", status)
	}
	if status, _, _ := e.call(c, "GET", "/api/auth/me", ""); status != 401 {
		t.Fatalf("after logout: %d", status)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newTestEnv(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e.api.limiter.now = func() time.Time { return now }
	c := e.client()
	for i := range 5 {
		if status, _, _ := e.call(c, "POST", "/api/auth/login", `{"password":"bad"}`); status != 401 {
			t.Fatalf("attempt %d: %d", i, status)
		}
	}
	status, body, _ := e.call(c, "POST", "/api/auth/login", `{"password":"correct-password"}`)
	if status != 429 || !strings.Contains(body["error"].(string), "Too many failed attempts") {
		t.Fatalf("expected lockout even with the right password: %d %v", status, body)
	}
	now = now.Add(16 * time.Minute)
	if status, _, _ := e.call(c, "POST", "/api/auth/login", `{"password":"correct-password"}`); status != 204 {
		t.Fatalf("after the window: %d", status)
	}
}

func TestCrossSiteRequestsAreRejected(t *testing.T) {
	e := newTestEnv(t)
	c := e.login()
	status, _, _ := e.call(c, "POST", "/api/providers", `{}`, "Sec-Fetch-Site", "cross-site")
	if status != http.StatusForbidden {
		t.Fatalf("cross-site POST: %d", status)
	}
}

func TestValidationErrorsHaveFields(t *testing.T) {
	e := newTestEnv(t)
	c := e.login()
	status, body, _ := e.call(c, "POST", "/api/providers", `{"name":"","type":"openai","base_url":"nope"}`)
	fields, _ := body["fields"].(map[string]any)
	if status != 400 || fields["name"] == nil || fields["base_url"] == nil {
		t.Fatalf("validation: %d %v", status, body)
	}
	if status, _, _ := e.call(c, "GET", "/api/providers/999", ""); status != 404 {
		t.Fatalf("missing provider: %d", status)
	}
	if status, body, _ := e.call(c, "GET", "/api/nope", ""); status != 404 || body["error"] == nil {
		t.Fatalf("unknown endpoint: %d %v", status, body)
	}
}

func TestProviderKeysProxiesAndAssignFlow(t *testing.T) {
	e := newTestEnv(t)
	c := e.login()
	status, prov, raw := e.call(c, "POST", "/api/providers", `{"name":"OpenAI","type":"openai","base_url":"https://api.openai.com/v1","enabled":true,"headers":{"x-test":"1"}}`)
	if status != 201 {
		t.Fatalf("create provider: %d %s", status, raw)
	}
	pid := int(prov["id"].(float64))
	path := "/api/providers/" + strconv.Itoa(pid)
	status, bulk, _ := e.call(c, "POST", path+"/keys/bulk", `{"text":"sk-aaaaaaaaaaaaaaaaaaaa|a\nsk-bbbbbbbbbbbbbbbbbbbb\nsk-aaaaaaaaaaaaaaaaaaaa"}`)
	if status != 200 || bulk["added"] != float64(2) || bulk["skipped"] != float64(1) {
		t.Fatalf("bulk add: %d %v", status, bulk)
	}
	status, prev, _ := e.call(c, "POST", "/api/proxies/import", `{"text":"1.1.1.1:8080\nbad\nsocks5://u:p@2.2.2.2:1080","default_type":"http","dry_run":true}`)
	if status != 200 || prev["valid"] != float64(2) || prev["invalid"] != float64(1) || prev["created"] != float64(0) {
		t.Fatalf("import preview: %d %v", status, prev)
	}
	e.call(c, "POST", "/api/proxies/import", `{"text":"1.1.1.1:8080\nsocks5://u:p@2.2.2.2:1080","default_type":"http","dry_run":false}`)

	var proxies []store.Proxy
	_, _, raw = e.call(c, "GET", "/api/proxies", "")
	json.Unmarshal(raw, &proxies)
	var keys []store.APIKey
	_, _, raw = e.call(c, "GET", "/api/keys", "")
	json.Unmarshal(raw, &keys)
	if len(proxies) != 2 || len(keys) != 2 {
		t.Fatalf("proxies %d keys %d", len(proxies), len(keys))
	}
	body, _ := json.Marshal(map[string]any{"proxy_ids": []int64{proxies[0].ID, proxies[1].ID}, "key_ids": []int64{keys[0].ID, keys[1].ID}})
	if status, res, _ := e.call(c, "POST", "/api/proxies/assign", string(body)); status != 200 || res["updated"] != float64(2) {
		t.Fatalf("assign: %d %v", status, res)
	}
	_, _, raw = e.call(c, "GET", path+"/keys", "")
	json.Unmarshal(raw, &keys)
	if keys[0].EffectiveProxy == nil || keys[1].EffectiveProxy == nil || keys[0].EffectiveProxy.ID == keys[1].EffectiveProxy.ID {
		t.Fatalf("round-robin assignment: %+v / %+v", keys[0].EffectiveProxy, keys[1].EffectiveProxy)
	}
	if strings.Contains(string(raw), "sk-aaaa") {
		t.Fatal("secrets must never be returned")
	}
	body, _ = json.Marshal(map[string]any{"ids": []int64{keys[0].ID}, "action": "disable"})
	if status, res, _ := e.call(c, "POST", "/api/keys/batch", string(body)); status != 200 || res["updated"] != float64(1) {
		t.Fatalf("batch: %d %v", status, res)
	}
	status, list, _ := e.call(c, "GET", "/api/dashboard?since=2026-01-01T00:00:00Z", "")
	setup, _ := list["setup"].(map[string]any)
	if status != 200 || setup["providers"] != float64(1) || setup["keys"] != float64(2) {
		t.Fatalf("dashboard: %d %v", status, list)
	}
}

func TestPasswordChangeSignsOutOtherSessions(t *testing.T) {
	e := newTestEnv(t)
	a, b := e.login(), e.login()
	status, body, _ := e.call(a, "POST", "/api/settings/password", `{"current_password":"wrong","new_password":"short"}`)
	fields, _ := body["fields"].(map[string]any)
	if status != 400 || fields["current_password"] == nil || fields["new_password"] == nil {
		t.Fatalf("validation: %d %v", status, body)
	}
	if status, _, _ := e.call(a, "POST", "/api/settings/password", `{"current_password":"correct-password","new_password":"new-password-1"}`); status != 204 {
		t.Fatalf("change: %d", status)
	}
	if status, _, _ := e.call(a, "GET", "/api/auth/me", ""); status != 200 {
		t.Fatal("the current session must stay valid")
	}
	if status, _, _ := e.call(b, "GET", "/api/auth/me", ""); status != 401 {
		t.Fatal("other sessions must be signed out")
	}
}

func TestGatewayKeySecretShownOnce(t *testing.T) {
	e := newTestEnv(t)
	c := e.login()
	status, created, _ := e.call(c, "POST", "/api/gateway-keys", `{"name":"laptop"}`)
	if status != 201 || !strings.HasPrefix(created["secret"].(string), "sy-") {
		t.Fatalf("create: %d %v", status, created)
	}
	_, _, raw := e.call(c, "GET", "/api/gateway-keys", "")
	if strings.Contains(string(raw), created["secret"].(string)) || strings.Contains(string(raw), `"secret"`) {
		t.Fatalf("list must not include the secret: %s", raw)
	}
}
