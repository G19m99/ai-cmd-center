// Package quotapoll keeps per-account quota snapshots fresh for the
// reset-aware routing strategy by polling the Claude and Codex usage
// endpoints. It only ever reads usage; bonus resets are applied manually.
package quotapoll

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	defaultInterval      = 5 * time.Minute
	defaultClientTimeout = 20 * time.Second
	// maxBackoff caps the per-account retry delay after repeated failures.
	maxBackoff = 30 * time.Minute
	// refreshSoonDebounce is the minimum gap between RefreshSoon fetches per auth.
	refreshSoonDebounce = 30 * time.Second
	// jitterFraction widens each sweep interval by ±10%.
	jitterFraction = 0.10
	// sweepConcurrency bounds how many accounts a sweep fetches at once.
	sweepConcurrency = 4
)

// Config configures a Poller.
type Config struct {
	Manager *coreauth.Manager
	Store   *coreauth.QuotaSnapshotStore
	// Interval is the sweep period; default 5m.
	Interval  time.Duration
	Endpoints Endpoints
	// Client returns the HTTP client for one auth so callers can honour
	// per-auth or global proxies. Default: a plain client with a 20s timeout.
	Client func(a *coreauth.Auth) *http.Client
}

// Poller sweeps every claude/codex auth on an interval and on demand.
type Poller struct {
	manager   *coreauth.Manager
	store     *coreauth.QuotaSnapshotStore
	interval  time.Duration
	endpoints Endpoints
	client    func(a *coreauth.Auth) *http.Client

	// now and newTimer are injectable for tests.
	now      func() time.Time
	newTimer func(d time.Duration) (<-chan time.Time, func() bool)
	rand     *rand.Rand

	mu sync.Mutex
	// backoff tracks consecutive failures and the earliest next attempt per auth.
	backoff map[string]backoffState
	// lastSoon records the last RefreshSoon fetch per auth for debouncing.
	lastSoon map[string]time.Time
	// fetchLocks serializes fetches per auth so a sweep and a RefreshSoon
	// never run the same account concurrently.
	fetchLocks sync.Map

	baseCtx context.Context
	wg      sync.WaitGroup
}

type backoffState struct {
	failures int
	next     time.Time
}

// New builds a Poller, filling in defaults for zero-value config fields.
func New(cfg Config) *Poller {
	p := &Poller{
		manager:   cfg.Manager,
		store:     cfg.Store,
		interval:  cfg.Interval,
		endpoints: cfg.Endpoints.withDefaults(),
		client:    cfg.Client,
		now:       time.Now,
		rand:      rand.New(rand.NewSource(time.Now().UnixNano())),
		backoff:   make(map[string]backoffState),
		lastSoon:  make(map[string]time.Time),
		baseCtx:   context.Background(),
	}
	if p.interval <= 0 {
		p.interval = defaultInterval
	}
	if p.client == nil {
		shared := &http.Client{Timeout: defaultClientTimeout}
		p.client = func(*coreauth.Auth) *http.Client { return shared }
	}
	p.newTimer = func(d time.Duration) (<-chan time.Time, func() bool) {
		timer := time.NewTimer(d)
		return timer.C, timer.Stop
	}
	return p
}

var defaultPoller atomic.Pointer[Poller]

// SetDefault publishes the process-wide poller; nil clears it.
func SetDefault(p *Poller) {
	defaultPoller.Store(p)
}

// Default returns the process-wide poller, or nil when none is running.
func Default() *Poller {
	return defaultPoller.Load()
}

// Start sweeps immediately, then every Interval ±10% jitter until ctx is
// done. It blocks; run it in a goroutine.
func (p *Poller) Start(ctx context.Context) {
	if p == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	p.baseCtx = ctx
	p.mu.Unlock()
	for {
		p.sweep(ctx)
		if ctx.Err() != nil {
			return
		}
		wait, stop := p.newTimer(p.jitteredInterval())
		select {
		case <-ctx.Done():
			stop()
			return
		case <-wait:
		}
	}
}

func (p *Poller) jitteredInterval() time.Duration {
	p.mu.Lock()
	factor := 1 + jitterFraction*(2*p.rand.Float64()-1)
	p.mu.Unlock()
	return time.Duration(float64(p.interval) * factor)
}

// sweep fetches every eligible auth that is not backing off and removes
// snapshots whose auth no longer exists.
func (p *Poller) sweep(ctx context.Context) {
	if p == nil || p.manager == nil {
		return
	}
	auths := p.manager.List()
	present := make(map[string]struct{}, len(auths))
	for _, auth := range auths {
		if auth != nil && auth.ID != "" {
			present[auth.ID] = struct{}{}
		}
	}
	for _, id := range p.store.IDs() {
		if _, ok := present[id]; !ok {
			p.store.Delete(id)
			p.forget(id)
		}
	}

	now := p.now()
	var wg sync.WaitGroup
	slots := make(chan struct{}, sweepConcurrency)
	for _, auth := range auths {
		if !eligible(auth) || ctx.Err() != nil {
			continue
		}
		if next := p.nextAttempt(auth.ID); !next.IsZero() && now.Before(next) {
			continue
		}
		wg.Add(1)
		slots <- struct{}{}
		go func(auth *coreauth.Auth) {
			defer wg.Done()
			defer func() { <-slots }()
			if err := p.refreshAuth(ctx, auth); err != nil && ctx.Err() == nil {
				log.WithError(err).Debugf("quota poll failed for %s auth %s", auth.Provider, auth.ID)
			}
		}(auth)
	}
	wg.Wait()
}

// Refresh fetches one account synchronously, ignoring any backoff.
func (p *Poller) Refresh(ctx context.Context, authID string) error {
	if p == nil || p.manager == nil {
		return errors.New("quotapoll: poller not running")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	auth, ok := p.manager.GetByID(authID)
	if !ok || auth == nil {
		p.store.Delete(authID)
		p.forget(authID)
		return fmt.Errorf("quotapoll: auth %q not found", authID)
	}
	if auth.Disabled {
		return fmt.Errorf("quotapoll: auth %q is disabled", authID)
	}
	if !supportedProvider(auth.Provider) {
		return fmt.Errorf("quotapoll: provider %q is not polled", auth.Provider)
	}
	return p.refreshAuth(ctx, auth)
}

// RefreshSoon schedules an asynchronous Refresh, at most once per 30s per
// auth. The 429 hook uses it so a limited account is re-ranked promptly.
func (p *Poller) RefreshSoon(authID string) {
	if p == nil || p.manager == nil || authID == "" {
		return
	}
	auth, ok := p.manager.GetByID(authID)
	if !ok || auth == nil || auth.Disabled || !supportedProvider(auth.Provider) {
		return
	}
	now := p.now()
	p.mu.Lock()
	if last, seen := p.lastSoon[authID]; seen && now.Sub(last) < refreshSoonDebounce {
		p.mu.Unlock()
		return
	}
	p.lastSoon[authID] = now
	ctx := p.baseCtx
	p.mu.Unlock()

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		if err := p.refreshAuth(ctx, auth); err != nil && ctx.Err() == nil {
			log.WithError(err).Debugf("quota refresh after 429 failed for %s auth %s", auth.Provider, auth.ID)
		}
	}()
}

// wait blocks until every RefreshSoon goroutine has finished (tests).
func (p *Poller) wait() {
	p.wg.Wait()
}

func (p *Poller) refreshAuth(ctx context.Context, auth *coreauth.Auth) error {
	lock := p.lockFor(auth.ID)
	lock.Lock()
	defer lock.Unlock()

	provider := strings.ToLower(strings.TrimSpace(auth.Provider))
	client := p.client(auth)
	if client == nil {
		client = &http.Client{Timeout: defaultClientTimeout}
	}
	var (
		result fetchResult
		err    error
	)
	switch provider {
	case "claude":
		result, err = fetchClaude(ctx, client, p.endpoints, auth)
	case "codex":
		result, err = fetchCodex(ctx, client, p.endpoints, auth)
	default:
		return fmt.Errorf("quotapoll: provider %q is not polled", auth.Provider)
	}
	now := p.now()
	if err != nil {
		p.store.MarkFailed(auth.ID, provider, err, now)
		p.recordFailure(auth.ID, now)
		return err
	}
	p.store.Put(coreauth.QuotaSnapshot{
		AuthID:    auth.ID,
		Provider:  provider,
		Windows:   result.Windows,
		Bonus:     result.Bonus,
		FetchedAt: now,
	})
	p.forget(auth.ID)
	return nil
}

func (p *Poller) lockFor(authID string) *sync.Mutex {
	lock, _ := p.fetchLocks.LoadOrStore(authID, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// recordFailure schedules the next attempt at min(Interval*2^n, 30m).
func (p *Poller) recordFailure(authID string, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.backoff[authID]
	state.failures++
	delay := p.interval
	for i := 0; i < state.failures && delay < maxBackoff; i++ {
		delay *= 2
	}
	if delay > maxBackoff {
		delay = maxBackoff
	}
	state.next = now.Add(delay)
	p.backoff[authID] = state
}

// nextAttempt returns when the account may next be swept; zero when it is
// not backing off.
func (p *Poller) nextAttempt(authID string) time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.backoff[authID].next
}

func (p *Poller) forget(authID string) {
	p.mu.Lock()
	delete(p.backoff, authID)
	p.mu.Unlock()
}

func eligible(auth *coreauth.Auth) bool {
	return auth != nil && auth.ID != "" && !auth.Disabled && supportedProvider(auth.Provider)
}

func supportedProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "claude", "codex":
		return true
	}
	return false
}
