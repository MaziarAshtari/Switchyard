// Package routing decides which upstream target and key serve a request and
// what happens after each attempt. It is pure: no I/O, the clock and the
// random source are injected so every decision is reproducible in tests.
package routing

import (
	"math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Target strategies (how a model orders its targets).
const (
	Priority   = "priority"
	RoundRobin = "round_robin"
	Weighted   = "weighted"
	Random     = "random"
)

// Key strategies (how a provider orders its keys).
const (
	KeyRoundRobin = "round_robin"
	KeyRandom     = "random"
	KeyLRU        = "least_recently_used"
)

// Key statuses.
const (
	StatusActive      = "active"
	StatusCoolingDown = "cooling_down"
	StatusDisabled    = "disabled"
)

// MaxCooldown caps cooldowns derived from Retry-After headers.
const MaxCooldown = 24 * time.Hour

// Target is a candidate (provider, upstream model) of a public model.
type Target struct {
	ID          int64
	ProviderID  int64
	KeyStrategy string // the provider's key strategy
	// KeyRecovery is how long the provider's keys stay disabled after an auth
	// error before they become active again; 0 means until reset.
	KeyRecovery time.Duration
	Weight      int
	Enabled     bool // target and its provider are enabled
}

// Key is the routing view of an upstream API key.
type Key struct {
	ID            int64
	Enabled       bool
	Status        string
	CooldownUntil time.Time
	DisabledUntil time.Time // automatic re-activation of a disabled key; zero = never
	LastUsedAt    time.Time
	ProxyOK       bool // false when the key's (inherited) proxy is disabled or missing
}

// Available reports whether k may be used at now.
func (k Key) Available(now time.Time) bool {
	if !k.Enabled || !k.ProxyOK {
		return false
	}
	switch k.Status {
	case StatusDisabled:
		return !k.DisabledUntil.IsZero() && !now.Before(k.DisabledUntil)
	case StatusCoolingDown:
		return !now.Before(k.CooldownUntil)
	}
	return true
}

// RetryOn lists the failure classes that move on to another key/target.
type RetryOn struct {
	RateLimit  bool // "429"
	Server     bool // "5xx"
	Timeout    bool // "timeout"
	Connection bool // "connection"
}

// RetryOnValues are the accepted retry_on values, in display order.
var RetryOnValues = []string{"429", "5xx", "timeout", "connection"}

// ParseRetryOn converts retry_on values; unknown values are ignored.
func ParseRetryOn(values []string) RetryOn {
	var r RetryOn
	for _, v := range values {
		switch strings.TrimSpace(v) {
		case "429":
			r.RateLimit = true
		case "5xx":
			r.Server = true
		case "timeout":
			r.Timeout = true
		case "connection":
			r.Connection = true
		}
	}
	return r
}

// Policy is a model's routing and retry configuration.
type Policy struct {
	Strategy         string
	MaxTotalAttempts int
	MaxKeysPerTarget int
	RetryOn          RetryOn
	DefaultCooldown  time.Duration // for 429 responses without Retry-After
}

// Router holds the little state routing needs across requests: round-robin
// counters and recent key picks. It is safe for concurrent use.
type Router struct {
	now func() time.Time

	mu     sync.Mutex
	rnd    *rand.Rand
	rr     map[string]uint64
	picked map[int64]time.Time
}

// NewRouter returns a Router using the given clock and random source.
func NewRouter(now func() time.Time, rnd *rand.Rand) *Router {
	return &Router{now: now, rnd: rnd, rr: map[string]uint64{}, picked: map[int64]time.Time{}}
}

// Now returns the router's current time.
func (r *Router) Now() time.Time { return r.now() }

func (r *Router) next(counter string) uint64 {
	n := r.rr[counter]
	r.rr[counter] = n + 1
	return n
}

// OrderTargets returns the enabled targets in the order they should be tried.
// The strategy picks the first target; the others follow as failover.
func (r *Router) OrderTargets(modelID int64, strategy string, targets []Target) []Target {
	out := make([]Target, 0, len(targets))
	for _, t := range targets {
		if t.Enabled {
			out = append(out, t)
		}
	}
	if len(out) < 2 {
		return out
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch strategy {
	case RoundRobin:
		rotate(out, int(r.next("model:"+strconv.FormatInt(modelID, 10))%uint64(len(out))))
	case Weighted:
		weightedOrder(r.rnd, out)
	case Random:
		r.rnd.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	}
	return out
}

// OrderKeys returns the available keys in the order they should be tried.
func (r *Router) OrderKeys(providerID int64, strategy string, keys []Key) []Key {
	now := r.now()
	out := make([]Key, 0, len(keys))
	for _, k := range keys {
		if k.Available(now) {
			out = append(out, k)
		}
	}
	if len(out) < 2 {
		return out
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch strategy {
	case KeyRandom:
		r.rnd.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	case KeyLRU:
		last := func(k Key) time.Time {
			if p := r.picked[k.ID]; p.After(k.LastUsedAt) {
				return p
			}
			return k.LastUsedAt
		}
		slices.SortStableFunc(out, func(a, b Key) int {
			if c := last(a).Compare(last(b)); c != 0 {
				return c
			}
			return cmpInt(a.ID, b.ID)
		})
	default: // round robin
		slices.SortFunc(out, func(a, b Key) int { return cmpInt(a.ID, b.ID) })
		rotate(out, int(r.next("provider:"+strconv.FormatInt(providerID, 10))%uint64(len(out))))
	}
	return out
}

// markPicked remembers that a key was just chosen, so least-recently-used
// selection spreads concurrent requests before last_used_at is persisted.
func (r *Router) markPicked(keyID int64) {
	r.mu.Lock()
	r.picked[keyID] = r.now()
	r.mu.Unlock()
}

func rotate[T any](s []T, n int) {
	if n == 0 {
		return
	}
	tmp := append(append([]T{}, s[n:]...), s[:n]...)
	copy(s, tmp)
}

// weightedOrder reorders targets by repeated weighted draws without replacement.
func weightedOrder(rnd *rand.Rand, ts []Target) {
	for i := 0; i < len(ts)-1; i++ {
		total := 0
		for _, t := range ts[i:] {
			total += max(t.Weight, 1)
		}
		n := rnd.IntN(total)
		for j := i; j < len(ts); j++ {
			n -= max(ts[j].Weight, 1)
			if n < 0 {
				ts[i], ts[j] = ts[j], ts[i]
				break
			}
		}
	}
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Outcome describes what happened during one attempt.
type Outcome struct {
	Status     int    // HTTP status; 0 when no response arrived
	Timeout    bool   // the attempt or request timed out
	ConnError  bool   // transport error (DNS, connect, TLS, proxy, reset)
	RetryAfter string // Retry-After header of the response
	Committed  bool   // the response was already (partly) sent to the client
}

// KeyAction is how an attempt affects its key's state.
type KeyAction int

const (
	KeyUsed     KeyAction = iota // only last_used_at changes (client errors)
	KeySuccess                   // success counter, status back to active
	KeyFailure                   // failure counter
	KeyCooldown                  // failure counter, cooling down until CooldownUntil
	KeyDisable                   // failure counter, disabled until reset or DisabledUntil
)

// Decision is the result of classifying an outcome.
type Decision struct {
	Retry         bool
	Action        KeyAction
	CooldownUntil time.Time
	DisabledUntil time.Time // KeyDisable only; zero = disabled until reset
}

// Decide classifies an attempt outcome. keyRecovery is how long a key
// disabled by an auth error stays disabled (0 = until reset).
func Decide(o Outcome, retry RetryOn, now time.Time, defaultCooldown, keyRecovery time.Duration) Decision {
	switch {
	case o.Committed:
		return Decision{Action: KeySuccess}
	case o.Timeout || o.Status == http.StatusRequestTimeout:
		return Decision{Retry: retry.Timeout, Action: KeyFailure}
	case o.ConnError:
		return Decision{Retry: retry.Connection, Action: KeyFailure}
	case o.Status >= 200 && o.Status < 300:
		return Decision{Action: KeySuccess}
	case o.Status == http.StatusTooManyRequests:
		d, ok := ParseRetryAfter(o.RetryAfter, now)
		if !ok {
			d = defaultCooldown
		}
		return Decision{Retry: retry.RateLimit, Action: KeyCooldown, CooldownUntil: now.Add(d)}
	case o.Status == http.StatusUnauthorized || o.Status == http.StatusPaymentRequired || o.Status == http.StatusForbidden:
		d := Decision{Retry: true, Action: KeyDisable}
		if keyRecovery > 0 {
			d.DisabledUntil = now.Add(keyRecovery)
		}
		return d
	case o.Status >= 500:
		return Decision{Retry: retry.Server, Action: KeyFailure}
	default:
		return Decision{Action: KeyUsed}
	}
}

// ParseRetryAfter parses a Retry-After value (seconds or HTTP date). The
// result is at least one second and at most MaxCooldown.
func ParseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	var d time.Duration
	if secs, err := strconv.ParseFloat(v, 64); err == nil {
		d = time.Duration(secs * float64(time.Second))
	} else if t, err := http.ParseTime(v); err == nil {
		d = t.Sub(now)
	} else {
		return 0, false
	}
	return min(max(d, time.Second), MaxCooldown), true
}

// KeySource loads the keys of a provider (with their current state).
type KeySource func(providerID int64) []Key

// Attempt is one (target, key) pair to try.
type Attempt struct {
	Number int // 1-based
	Target Target
	Key    Key
}

// Skip records a target that produced no attempt and why.
type Skip struct {
	TargetID int64
	Reason   string
}

// Plan iterates the attempts of one request. Use Next to get an attempt and
// Report to record its outcome. A Plan is not safe for concurrent use.
type Plan struct {
	router  *Router
	policy  Policy
	targets []Target
	source  KeySource

	ti, ki, tried, total int
	keys                 []Key
	loaded, done         bool
	blocked              map[int64]bool

	// Skipped lists targets that produced no attempt.
	Skipped []Skip
}

// NewPlan prepares the attempts for a request to model modelID.
func (r *Router) NewPlan(modelID int64, p Policy, targets []Target, keys KeySource) *Plan {
	plan := &Plan{router: r, policy: p, source: keys, blocked: map[int64]bool{}}
	for _, t := range targets {
		if !t.Enabled {
			plan.Skipped = append(plan.Skipped, Skip{t.ID, "disabled"})
		}
	}
	plan.targets = r.OrderTargets(modelID, p.Strategy, targets)
	return plan
}

// Next returns the next attempt, or false when the plan is exhausted.
func (p *Plan) Next() (Attempt, bool) {
	for !p.done {
		if p.total >= max(p.policy.MaxTotalAttempts, 1) || p.ti >= len(p.targets) {
			p.done = true
			break
		}
		t := p.targets[p.ti]
		if !p.loaded {
			p.keys = p.router.OrderKeys(t.ProviderID, t.KeyStrategy, p.source(t.ProviderID))
			p.ki, p.tried, p.loaded = 0, 0, true
			if len(p.keys) == 0 {
				p.Skipped = append(p.Skipped, Skip{t.ID, "no available keys"})
			}
		}
		if p.tried >= max(p.policy.MaxKeysPerTarget, 1) || p.ki >= len(p.keys) {
			p.ti++
			p.loaded = false
			continue
		}
		k := p.keys[p.ki]
		p.ki++
		if p.blocked[k.ID] {
			continue
		}
		p.tried++
		p.total++
		p.router.markPicked(k.ID)
		return Attempt{Number: p.total, Target: t, Key: k}, true
	}
	return Attempt{}, false
}

// Report records the outcome of attempt a and returns the decision. After a
// decision without Retry, Next returns false.
func (p *Plan) Report(a Attempt, o Outcome) Decision {
	d := Decide(o, p.policy.RetryOn, p.router.now(), p.policy.DefaultCooldown, a.Target.KeyRecovery)
	if d.Action == KeyCooldown || d.Action == KeyDisable {
		p.blocked[a.Key.ID] = true
	}
	if !d.Retry {
		p.done = true
	}
	return d
}

// Attempts returns the number of attempts made so far.
func (p *Plan) Attempts() int { return p.total }
