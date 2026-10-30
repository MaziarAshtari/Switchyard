package routing

import (
	"math/rand/v2"
	"net/http"
	"slices"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newRouter(seed uint64) (*Router, *fakeClock) {
	c := &fakeClock{t: t0}
	return NewRouter(c.now, rand.New(rand.NewPCG(seed, seed))), c
}

func targetIDs(ts []Target) []int64 {
	var ids []int64
	for _, t := range ts {
		ids = append(ids, t.ID)
	}
	return ids
}

func keyIDs(ks []Key) []int64 {
	var ids []int64
	for _, k := range ks {
		ids = append(ids, k.ID)
	}
	return ids
}

func activeKey(id int64) Key {
	return Key{ID: id, Enabled: true, Status: StatusActive, ProxyOK: true}
}

func TestOrderTargetsPriorityKeepsOrderAndSkipsDisabled(t *testing.T) {
	r, _ := newRouter(1)
	ts := []Target{{ID: 3, Enabled: true}, {ID: 1, Enabled: false}, {ID: 2, Enabled: true}}
	if got := targetIDs(r.OrderTargets(1, Priority, ts)); !slices.Equal(got, []int64{3, 2}) {
		t.Fatalf("got %v", got)
	}
}

func TestOrderTargetsRoundRobinRotatesPerModel(t *testing.T) {
	r, _ := newRouter(1)
	ts := []Target{{ID: 1, Enabled: true}, {ID: 2, Enabled: true}, {ID: 3, Enabled: true}}
	want := [][]int64{{1, 2, 3}, {2, 3, 1}, {3, 1, 2}, {1, 2, 3}}
	for i, w := range want {
		if got := targetIDs(r.OrderTargets(7, RoundRobin, ts)); !slices.Equal(got, w) {
			t.Fatalf("call %d: got %v want %v", i, got, w)
		}
	}
	// Another model has its own counter.
	if got := targetIDs(r.OrderTargets(8, RoundRobin, ts)); !slices.Equal(got, []int64{1, 2, 3}) {
		t.Fatalf("model 8 got %v", got)
	}
}

func TestOrderTargetsWeightedFollowsWeights(t *testing.T) {
	r, _ := newRouter(42)
	ts := []Target{{ID: 1, Weight: 1, Enabled: true}, {ID: 2, Weight: 9, Enabled: true}}
	first := map[int64]int{}
	for range 10000 {
		order := r.OrderTargets(1, Weighted, ts)
		if len(order) != 2 {
			t.Fatalf("weighted order lost targets: %v", targetIDs(order))
		}
		first[order[0].ID]++
	}
	// Expect ~90% for target 2.
	if share := float64(first[2]) / 10000; share < 0.87 || share > 0.93 {
		t.Fatalf("target 2 first in %.3f of draws, want ~0.9", share)
	}
}

func TestOrderTargetsRandomIsPermutation(t *testing.T) {
	r, _ := newRouter(3)
	ts := []Target{{ID: 1, Enabled: true}, {ID: 2, Enabled: true}, {ID: 3, Enabled: true}}
	seen := map[int64]bool{}
	for range 100 {
		got := targetIDs(r.OrderTargets(1, Random, ts))
		sorted := slices.Clone(got)
		slices.Sort(sorted)
		if !slices.Equal(sorted, []int64{1, 2, 3}) {
			t.Fatalf("not a permutation: %v", got)
		}
		seen[got[0]] = true
	}
	if len(seen) != 3 {
		t.Fatalf("random strategy never started with some targets: %v", seen)
	}
}

func TestKeyAvailability(t *testing.T) {
	cases := []struct {
		name string
		key  Key
		want bool
	}{
		{"active", activeKey(1), true},
		{"user disabled", Key{ID: 1, Status: StatusActive, ProxyOK: true}, false},
		{"auto disabled", Key{ID: 1, Enabled: true, Status: StatusDisabled, ProxyOK: true}, false},
		{"cooling", Key{ID: 1, Enabled: true, Status: StatusCoolingDown, CooldownUntil: t0.Add(time.Second), ProxyOK: true}, false},
		{"cooldown expired", Key{ID: 1, Enabled: true, Status: StatusCoolingDown, CooldownUntil: t0, ProxyOK: true}, true},
		{"proxy disabled", Key{ID: 1, Enabled: true, Status: StatusActive}, false},
	}
	for _, c := range cases {
		if got := c.key.Available(t0); got != c.want {
			t.Errorf("%s: Available = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestOrderKeysRoundRobinSkipsUnavailable(t *testing.T) {
	r, _ := newRouter(1)
	keys := []Key{activeKey(1), {ID: 2, Enabled: true, Status: StatusDisabled, ProxyOK: true}, activeKey(3), activeKey(4)}
	want := [][]int64{{1, 3, 4}, {3, 4, 1}, {4, 1, 3}}
	for i, w := range want {
		if got := keyIDs(r.OrderKeys(9, KeyRoundRobin, keys)); !slices.Equal(got, w) {
			t.Fatalf("call %d: got %v want %v", i, got, w)
		}
	}
}

func TestOrderKeysLeastRecentlyUsed(t *testing.T) {
	r, clock := newRouter(1)
	keys := []Key{
		{ID: 1, Enabled: true, Status: StatusActive, ProxyOK: true, LastUsedAt: t0.Add(-time.Minute)},
		{ID: 2, Enabled: true, Status: StatusActive, ProxyOK: true}, // never used
		{ID: 3, Enabled: true, Status: StatusActive, ProxyOK: true, LastUsedAt: t0.Add(-time.Hour)},
	}
	if got := keyIDs(r.OrderKeys(1, KeyLRU, keys)); !slices.Equal(got, []int64{2, 3, 1}) {
		t.Fatalf("got %v", got)
	}
	// An in-flight pick counts as a use even before last_used_at is stored.
	r.markPicked(2)
	clock.t = clock.t.Add(time.Millisecond)
	if got := keyIDs(r.OrderKeys(1, KeyLRU, keys)); !slices.Equal(got, []int64{3, 1, 2}) {
		t.Fatalf("after pick got %v", got)
	}
}

func TestDecide(t *testing.T) {
	all := RetryOn{RateLimit: true, Server: true, Timeout: true, Connection: true}
	none := RetryOn{}
	def := 60 * time.Second
	cases := []struct {
		name   string
		o      Outcome
		retry  RetryOn
		want   Decision
		wantCD time.Duration
	}{
		{"success", Outcome{Status: 200}, all, Decision{Action: KeySuccess}, 0},
		{"committed", Outcome{Status: 200, Committed: true}, all, Decision{Action: KeySuccess}, 0},
		{"429 default cooldown", Outcome{Status: 429}, all, Decision{Retry: true, Action: KeyCooldown}, def},
		{"429 retry-after", Outcome{Status: 429, RetryAfter: "7"}, all, Decision{Retry: true, Action: KeyCooldown}, 7 * time.Second},
		{"429 not retried", Outcome{Status: 429}, none, Decision{Action: KeyCooldown}, def},
		{"401", Outcome{Status: 401}, none, Decision{Retry: true, Action: KeyDisable}, 0},
		{"402", Outcome{Status: 402}, none, Decision{Retry: true, Action: KeyDisable}, 0},
		{"403", Outcome{Status: 403}, none, Decision{Retry: true, Action: KeyDisable}, 0},
		{"500", Outcome{Status: 500}, all, Decision{Retry: true, Action: KeyFailure}, 0},
		{"503 not retried", Outcome{Status: 503}, none, Decision{Action: KeyFailure}, 0},
		{"timeout", Outcome{Timeout: true}, all, Decision{Retry: true, Action: KeyFailure}, 0},
		{"408 is a timeout", Outcome{Status: 408}, RetryOn{Timeout: true}, Decision{Retry: true, Action: KeyFailure}, 0},
		{"connection", Outcome{ConnError: true}, all, Decision{Retry: true, Action: KeyFailure}, 0},
		{"connection not retried", Outcome{ConnError: true}, none, Decision{Action: KeyFailure}, 0},
		{"400", Outcome{Status: 400}, all, Decision{Action: KeyUsed}, 0},
		{"404", Outcome{Status: 404}, all, Decision{Action: KeyUsed}, 0},
	}
	for _, c := range cases {
		got := Decide(c.o, c.retry, t0, def)
		if c.wantCD > 0 {
			c.want.CooldownUntil = t0.Add(c.wantCD)
		}
		if got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"", 0, false},
		{"abc", 0, false},
		{"30", 30 * time.Second, true},
		{"1.5", 1500 * time.Millisecond, true},
		{"0", time.Second, true},
		{"999999", MaxCooldown, true},
		{t0.Add(2 * time.Minute).Format(http.TimeFormat), 2 * time.Minute, true},
	}
	for _, c := range cases {
		got, ok := ParseRetryAfter(c.in, t0)
		if got != c.want || ok != c.ok {
			t.Errorf("ParseRetryAfter(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestParseRetryOn(t *testing.T) {
	got := ParseRetryOn([]string{"429", " timeout", "bogus"})
	if got != (RetryOn{RateLimit: true, Timeout: true}) {
		t.Fatalf("got %+v", got)
	}
}

// run drives a plan with scripted outcomes keyed by key ID and returns the
// attempted (target, key) pairs.
func run(p *Plan, outcomes map[int64]Outcome) [][2]int64 {
	var tried [][2]int64
	for {
		a, ok := p.Next()
		if !ok {
			return tried
		}
		tried = append(tried, [2]int64{a.Target.ID, a.Key.ID})
		o, found := outcomes[a.Key.ID]
		if !found {
			o = Outcome{Status: 200}
		}
		p.Report(a, o)
	}
}

func source(m map[int64][]Key) KeySource {
	return func(providerID int64) []Key { return m[providerID] }
}

func defaultPolicy() Policy {
	return Policy{Strategy: Priority, MaxTotalAttempts: 10, MaxKeysPerTarget: 10,
		RetryOn: ParseRetryOn(RetryOnValues), DefaultCooldown: time.Minute}
}

func TestPlanFailsOverAcrossKeysAndTargets(t *testing.T) {
	r, _ := newRouter(1)
	targets := []Target{
		{ID: 10, ProviderID: 1, KeyStrategy: KeyRoundRobin, Enabled: true},
		{ID: 20, ProviderID: 2, KeyStrategy: KeyRoundRobin, Enabled: true},
	}
	keys := map[int64][]Key{1: {activeKey(1), activeKey(2)}, 2: {activeKey(3)}}
	p := r.NewPlan(1, defaultPolicy(), targets, source(keys))
	got := run(p, map[int64]Outcome{1: {Status: 429}, 2: {Status: 500}})
	want := [][2]int64{{10, 1}, {10, 2}, {20, 3}}
	if !slices.Equal(got, want) {
		t.Fatalf("attempts = %v, want %v", got, want)
	}
}

func TestPlanStopsOnClientError(t *testing.T) {
	r, _ := newRouter(1)
	targets := []Target{{ID: 10, ProviderID: 1, Enabled: true}, {ID: 20, ProviderID: 2, Enabled: true}}
	keys := map[int64][]Key{1: {activeKey(1), activeKey(2)}, 2: {activeKey(3)}}
	p := r.NewPlan(1, defaultPolicy(), targets, source(keys))
	if got := run(p, map[int64]Outcome{1: {Status: 400}}); len(got) != 1 {
		t.Fatalf("400 must not be retried, attempts = %v", got)
	}
}

func TestPlanStopsWhenFailureClassNotRetried(t *testing.T) {
	r, _ := newRouter(1)
	pol := defaultPolicy()
	pol.RetryOn = RetryOn{RateLimit: true}
	keys := map[int64][]Key{1: {activeKey(1), activeKey(2)}}
	p := r.NewPlan(1, pol, []Target{{ID: 10, ProviderID: 1, Enabled: true}}, source(keys))
	if got := run(p, map[int64]Outcome{1: {Status: 502}}); len(got) != 1 {
		t.Fatalf("5xx not in retry_on must stop, attempts = %v", got)
	}
}

func TestPlanRespectsMaxKeysPerTargetAndMaxTotal(t *testing.T) {
	r, _ := newRouter(1)
	pol := defaultPolicy()
	pol.MaxKeysPerTarget = 2
	pol.MaxTotalAttempts = 3
	targets := []Target{{ID: 10, ProviderID: 1, Enabled: true}, {ID: 20, ProviderID: 2, Enabled: true}}
	keys := map[int64][]Key{1: {activeKey(1), activeKey(2), activeKey(3)}, 2: {activeKey(4), activeKey(5)}}
	fail := Outcome{Status: 503}
	p := r.NewPlan(1, pol, targets, source(keys))
	got := run(p, map[int64]Outcome{1: fail, 2: fail, 3: fail, 4: fail, 5: fail})
	want := [][2]int64{{10, 1}, {10, 2}, {20, 4}}
	if !slices.Equal(got, want) {
		t.Fatalf("attempts = %v, want %v", got, want)
	}
	if p.Attempts() != 3 {
		t.Fatalf("Attempts() = %d", p.Attempts())
	}
}

func TestPlanDoesNotReuseCooledDownKeyOnSameProvider(t *testing.T) {
	r, _ := newRouter(1)
	// Two targets on the same provider (different upstream models).
	targets := []Target{{ID: 10, ProviderID: 1, Enabled: true}, {ID: 20, ProviderID: 1, Enabled: true}}
	keys := map[int64][]Key{1: {activeKey(1), activeKey(2)}}
	p := r.NewPlan(1, defaultPolicy(), targets, source(keys))
	got := run(p, map[int64]Outcome{1: {Status: 429}, 2: {Status: 500}})
	// Key 1 is blocked after its 429; key 2 failed with 5xx and may serve the next target.
	want := [][2]int64{{10, 1}, {10, 2}, {20, 2}}
	if !slices.Equal(got, want) {
		t.Fatalf("attempts = %v, want %v", got, want)
	}
}

func TestPlanRecordsSkippedTargets(t *testing.T) {
	r, _ := newRouter(1)
	targets := []Target{{ID: 10, ProviderID: 1, Enabled: false}, {ID: 20, ProviderID: 2, Enabled: true}}
	keys := map[int64][]Key{2: {{ID: 1, Enabled: true, Status: StatusDisabled, ProxyOK: true}}}
	p := r.NewPlan(1, defaultPolicy(), targets, source(keys))
	if got := run(p, nil); len(got) != 0 {
		t.Fatalf("expected no attempts, got %v", got)
	}
	want := []Skip{{10, "disabled"}, {20, "no available keys"}}
	if !slices.Equal(p.Skipped, want) {
		t.Fatalf("Skipped = %v, want %v", p.Skipped, want)
	}
}

func TestPlanCommittedStopsEvenIfLaterFailure(t *testing.T) {
	r, _ := newRouter(1)
	keys := map[int64][]Key{1: {activeKey(1), activeKey(2)}}
	p := r.NewPlan(1, defaultPolicy(), []Target{{ID: 10, ProviderID: 1, Enabled: true}}, source(keys))
	a, _ := p.Next()
	d := p.Report(a, Outcome{Status: 200, Committed: true})
	if d.Retry {
		t.Fatal("committed response must never be retried")
	}
	if _, ok := p.Next(); ok {
		t.Fatal("plan should be finished")
	}
}
