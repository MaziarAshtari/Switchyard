package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/MaziarAshtari/Switchyard/internal/db"
	"github.com/MaziarAshtari/Switchyard/internal/routing"
	"github.com/MaziarAshtari/Switchyard/internal/secret"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newStore(t *testing.T) (*Store, *clock) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	box, _ := secret.NewBox("test-encryption-key-123")
	s := New(d, box)
	c := &clock{t: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
	s.SetClock(c.now)
	return s, c
}

var ctx = context.Background()

func mustProvider(t *testing.T, s *Store, name, typ string) Provider {
	t.Helper()
	p, err := s.CreateProvider(ctx, ProviderInput{Name: name, Type: typ, BaseURL: "https://api.example.com/v1/", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func fieldErr(t *testing.T, err error, field string) {
	t.Helper()
	var fe FieldErrors
	if !errors.As(err, &fe) || fe[field] == "" {
		t.Fatalf("expected field error on %q, got %v", field, err)
	}
}

func TestProviderValidationAndDefaults(t *testing.T) {
	s, _ := newStore(t)
	_, err := s.CreateProvider(ctx, ProviderInput{Type: "nope", BaseURL: "ftp://x"})
	fieldErr(t, err, "name")
	fieldErr(t, err, "type")
	fieldErr(t, err, "base_url")

	p := mustProvider(t, s, "OpenAI", "openai")
	if p.BaseURL != "https://api.example.com/v1" || p.TimeoutSeconds != 600 || p.KeyStrategy != "round_robin" {
		t.Fatalf("defaults not applied: %+v", p)
	}
	_, err = s.CreateProvider(ctx, ProviderInput{Name: "OpenAI", Type: "openai", BaseURL: "https://x"})
	fieldErr(t, err, "name")
}

func TestKeysBulkAddDedupeAndSecret(t *testing.T) {
	s, _ := newStore(t)
	p := mustProvider(t, s, "P", "openai")
	res, err := s.BulkAddKeys(ctx, p.ID, "sk-aaaaaaaaaaaaaaaaaaaa1111|main\n\n sk-bbbbbbbbbbbbbbbbbbbb2222 \nsk-aaaaaaaaaaaaaaaaaaaa1111\nbad key\n|label-only")
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 2 || res.Skipped != 1 || len(res.Errors) != 2 {
		t.Fatalf("unexpected bulk result: %+v", res)
	}
	keys, _ := s.ListKeys(ctx, p.ID)
	if len(keys) != 2 || keys[0].Label != "main" || keys[0].SecretHint != "sk-...1111" {
		t.Fatalf("unexpected keys: %+v", keys)
	}
	got, err := s.KeySecret(ctx, keys[0].ID)
	if err != nil || got != "sk-aaaaaaaaaaaaaaaaaaaa1111" {
		t.Fatalf("KeySecret = %q, %v", got, err)
	}
	if _, err := s.CreateKey(ctx, p.ID, KeyInput{Secret: "sk-bbbbbbbbbbbbbbbbbbbb2222"}); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestKeyOutcomesAndEffectiveStatus(t *testing.T) {
	s, c := newStore(t)
	p := mustProvider(t, s, "P", "openai")
	k, _ := s.CreateKey(ctx, p.ID, KeyInput{Secret: "sk-test-key-0000000000"})

	s.RecordKeyOutcome(ctx, k.ID, routing.Decision{Action: routing.KeyCooldown, CooldownUntil: c.t.Add(time.Minute)}, "429 slow down")
	k, _ = s.GetKey(ctx, k.ID)
	if k.Status != routing.StatusCoolingDown || k.CooldownUntil == nil || k.LastError != "429 slow down" || k.FailureCount != 1 {
		t.Fatalf("after cooldown: %+v", k)
	}
	c.t = c.t.Add(2 * time.Minute)
	k, _ = s.GetKey(ctx, k.ID)
	if k.Status != routing.StatusActive || k.CooldownUntil != nil {
		t.Fatalf("expired cooldown should read as active: %+v", k)
	}

	s.RecordKeyOutcome(ctx, k.ID, routing.Decision{Action: routing.KeyDisable}, "401 invalid key")
	s.RecordKeyOutcome(ctx, k.ID, routing.Decision{Action: routing.KeyCooldown, CooldownUntil: c.t.Add(time.Hour)}, "429")
	k, _ = s.GetKey(ctx, k.ID)
	if k.Status != routing.StatusDisabled {
		t.Fatalf("a cooldown must not re-enable a disabled key: %+v", k)
	}
	s.RecordKeyOutcome(ctx, k.ID, routing.Decision{Action: routing.KeySuccess}, "")
	k, _ = s.ResetKey(ctx, k.ID)
	if k.Status != routing.StatusActive || k.SuccessCount != 1 || k.LastUsedAt == nil {
		t.Fatalf("after reset: %+v", k)
	}
}

func TestProxyResolutionAndDelete(t *testing.T) {
	s, _ := newStore(t)
	px, err := s.CreateProxy(ctx, ProxyInput{Type: "socks5", Host: "Proxy.Example", Port: 1080, Username: "u", Password: ptr("pw"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if px.Name != "proxy.example:1080" || !px.HasPassword {
		t.Fatalf("unexpected proxy: %+v", px)
	}
	p := mustProvider(t, s, "P", "openai")
	in := ProviderInput{Name: "P", Type: "openai", BaseURL: p.BaseURL, Enabled: true, ProxyID: &px.ID}
	if _, err := s.UpdateProvider(ctx, p.ID, in); err != nil {
		t.Fatal(err)
	}
	inherit, _ := s.CreateKey(ctx, p.ID, KeyInput{Secret: "sk-inherit-0000000000"})
	direct, _ := s.CreateKey(ctx, p.ID, KeyInput{Secret: "sk-direct-00000000000", ProxyMode: ProxyDirect})
	specific, _ := s.CreateKey(ctx, p.ID, KeyInput{Secret: "sk-specific-000000000", ProxyMode: ProxySpecific, ProxyID: &px.ID})
	if inherit.EffectiveProxy == nil || direct.EffectiveProxy != nil || specific.EffectiveProxy == nil {
		t.Fatalf("effective proxies wrong: %+v %+v %+v", inherit.EffectiveProxy, direct.EffectiveProxy, specific.EffectiveProxy)
	}
	spec, enabled, err := s.ProxySpec(ctx, px.ID)
	if err != nil || !enabled || spec.Password != "pw" || spec.Host != "proxy.example" {
		t.Fatalf("ProxySpec = %+v %v %v", spec, enabled, err)
	}

	// Disabling the proxy makes keys that use it unusable.
	s.UpdateProxy(ctx, px.ID, ProxyInput{Type: "socks5", Host: "proxy.example", Port: 1080, Username: "u", Enabled: false})
	inherit, _ = s.GetKey(ctx, inherit.ID)
	if inherit.ProxyOK() {
		t.Fatal("key inheriting a disabled proxy must not be usable")
	}
	if spec, _, _ = s.ProxySpec(ctx, px.ID); spec.Password != "pw" {
		t.Fatal("update without password must keep it")
	}
	px, _ = s.GetProxy(ctx, px.ID)
	if px.UsedByKeys != 1 || px.UsedByProviders != 1 {
		t.Fatalf("usage counts: %+v", px)
	}

	if err := s.DeleteProxy(ctx, px.ID); err != nil {
		t.Fatal(err)
	}
	specific, _ = s.GetKey(ctx, specific.ID)
	p, _ = s.GetProvider(ctx, p.ID)
	if specific.ProxyMode != ProxyInherit || specific.EffectiveProxy != nil || p.ProxyID != nil || !specific.ProxyOK() {
		t.Fatalf("after delete: key %+v provider proxy %v", specific, p.ProxyID)
	}
}

func ptr[T any](v T) *T { return &v }

func TestImportProxiesPreviewAndDuplicates(t *testing.T) {
	s, _ := newStore(t)
	s.CreateProxy(ctx, ProxyInput{Type: "http", Host: "1.1.1.1", Port: 80, Enabled: true})
	text := "1.1.1.1:80\nsocks5://u:p@2.2.2.2:1080\n2.2.2.2:1080:u:p\nnot-a-proxy\n3.3.3.3:3128"
	res, err := s.ImportProxies(ctx, text, "http", true)
	if err != nil {
		t.Fatal(err)
	}
	// 2.2.2.2:1080:u:p uses the default type (http), so it is not a duplicate of the socks5 line.
	if res.Valid != 3 || res.Duplicates != 1 || res.Invalid != 1 || res.Created != 0 {
		t.Fatalf("preview: %+v", res)
	}
	if list, _ := s.ListProxies(ctx); len(list) != 1 {
		t.Fatal("dry run must not create proxies")
	}
	res, _ = s.ImportProxies(ctx, text, "http", false)
	if res.Created != 3 {
		t.Fatalf("import: %+v", res)
	}
	if res, _ = s.ImportProxies(ctx, text, "http", false); res.Created != 0 || res.Duplicates != 4 {
		t.Fatalf("re-import should skip everything: %+v", res)
	}
}

func TestModelValidationAndTargets(t *testing.T) {
	s, _ := newStore(t)
	oa := mustProvider(t, s, "OA", "openai")
	an := mustProvider(t, s, "AN", "anthropic")
	in := ModelInput{Name: "tts-1", Type: "tts", MaxTotalAttempts: 3, MaxKeysPerTarget: 2, AttemptTimeoutSeconds: 60,
		RetryOn: []string{"timeout", "429", "429"},
		Targets: []TargetInput{{ProviderID: an.ID, UpstreamModel: "x", Enabled: true}}}
	_, err := s.CreateModel(ctx, in)
	fieldErr(t, err, "targets.0.provider_id")

	in.Targets = []TargetInput{{ProviderID: oa.ID, UpstreamModel: "tts-1", Enabled: true}, {ProviderID: oa.ID, UpstreamModel: "tts-2", Weight: 5}}
	m, err := s.CreateModel(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.RetryOn) != 2 || m.RetryOn[0] != "429" || m.Strategy != "priority" {
		t.Fatalf("retry_on/strategy not normalised: %+v", m)
	}
	if len(m.Targets) != 2 || m.Targets[0].UpstreamModel != "tts-1" || m.Targets[1].Weight != 5 || m.Targets[0].Weight != 1 {
		t.Fatalf("targets: %+v", m.Targets)
	}
	in.Targets = []TargetInput{in.Targets[1], in.Targets[0]}
	m, _ = s.UpdateModel(ctx, m.ID, in)
	if m.Targets[0].UpstreamModel != "tts-2" {
		t.Fatalf("reorder failed: %+v", m.Targets)
	}
	if byName, err := s.ModelByName(ctx, "tts-1"); err != nil || byName.ID != m.ID {
		t.Fatalf("ModelByName: %v", err)
	}
	if err := s.DeleteProvider(ctx, oa.ID); err != nil {
		t.Fatal(err)
	}
	if m, _ = s.GetModel(ctx, m.ID); len(m.Targets) != 0 {
		t.Fatal("deleting a provider should remove its targets")
	}
}

func TestSettingsAndSessions(t *testing.T) {
	s, c := newStore(t)
	set, _ := s.Settings(ctx)
	if set != DefaultSettings() {
		t.Fatalf("defaults: %+v", set)
	}
	set.LogRetentionDays = 0
	_, err := s.SaveSettings(ctx, set)
	fieldErr(t, err, "log_retention_days")
	set.LogRetentionDays, set.LogBodies = 7, true
	if _, err := s.SaveSettings(ctx, set); err != nil {
		t.Fatal(err)
	}
	s.settings = nil // force reload from the database
	if got, _ := s.Settings(ctx); got != set {
		t.Fatalf("reloaded settings %+v != %+v", got, set)
	}

	s.CreateSession(ctx, "h1", c.t.Add(time.Hour))
	if ok, _ := s.SessionValid(ctx, "h1"); !ok {
		t.Fatal("session should be valid")
	}
	c.t = c.t.Add(2 * time.Hour)
	if ok, _ := s.SessionValid(ctx, "h1"); ok {
		t.Fatal("session should have expired")
	}
	if err := s.CheckEncryptionKey(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckEncryptionKey(ctx); err != nil {
		t.Fatal("second check must pass with the same key:", err)
	}
	other, _ := secret.NewBox("a-different-key-456789")
	if err := New(s.db, other).CheckEncryptionKey(ctx); err == nil {
		t.Fatal("a different key must be rejected")
	}
}

func TestLogsFilterCursorPruneAndDashboard(t *testing.T) {
	s, c := newStore(t)
	since := c.t
	for i := range 5 {
		status := 200
		if i%2 == 1 {
			status = 502
		}
		in := int64(10)
		s.InsertLog(ctx, &LogDetail{LogEntry: LogEntry{CreatedAt: c.t.Add(time.Duration(i) * time.Hour), Endpoint: "/v1/chat/completions",
			Model: "m", ModelType: "chat", ProviderName: "P", StatusCode: status, LatencyMS: 100, InputTokens: &in},
			AttemptDetails: []Attempt{{ProviderName: "P", StatusCode: status}}})
	}
	page, next, err := s.ListLogs(ctx, LogFilter{Limit: 2})
	if err != nil || len(page) != 2 || next == nil {
		t.Fatalf("page 1: %d %v %v", len(page), next, err)
	}
	page2, _, _ := s.ListLogs(ctx, LogFilter{Limit: 2, BeforeID: *next})
	if len(page2) != 2 || page2[0].ID >= page[1].ID {
		t.Fatal("cursor pagination broken")
	}
	errorsOnly, _, _ := s.ListLogs(ctx, LogFilter{Status: "error"})
	if len(errorsOnly) != 2 {
		t.Fatalf("status filter: %d", len(errorsOnly))
	}
	detail, err := s.GetLog(ctx, page[0].ID)
	if err != nil || len(detail.AttemptDetails) != 1 || detail.RequestBody != nil {
		t.Fatalf("detail: %+v %v", detail, err)
	}

	d, err := s.Dashboard(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	if d.Requests != 5 || d.Errors != 2 || d.InputTokens != 50 || len(d.Hourly) != 5 || d.Hourly[4].Hour != 4 || d.ByModel[0].Requests != 5 {
		t.Fatalf("dashboard: %+v", d)
	}

	c.t = c.t.Add(10 * 24 * time.Hour)
	if n, err := s.Prune(ctx, 7*24*time.Hour); err != nil || n != 5 {
		t.Fatalf("prune: %d %v", n, err)
	}
}

func TestGatewayKeys(t *testing.T) {
	s, _ := newStore(t)
	k, err := s.CreateGatewayKey(ctx, "laptop")
	if err != nil || len(k.Secret) != 51 {
		t.Fatalf("create: %+v %v", k, err)
	}
	if got, err := s.AuthenticateGatewayKey(ctx, k.Secret); err != nil || got.ID != k.ID {
		t.Fatalf("authenticate: %v", err)
	}
	s.UpdateGatewayKey(ctx, k.ID, GatewayKeyInput{Name: "laptop", Enabled: false})
	if _, err := s.AuthenticateGatewayKey(ctx, k.Secret); !errors.Is(err, ErrNotFound) {
		t.Fatal("disabled key must not authenticate")
	}
}
