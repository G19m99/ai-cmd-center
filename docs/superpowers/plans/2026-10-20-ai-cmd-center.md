# AI Command Center Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fork CLIProxyAPI and its management panel so that Claude and Codex traffic drains the account whose weekly quota resets soonest. Conversations stay on the account where their prompt cache lives, and the dashboard shows routing status and bonus resets.

**Architecture:**
- A server-side poller fills an in-memory quota snapshot store. Live usage comes from Claude `api/oauth/usage` and Codex `wham/usage` + reset credits.
- A new `ResetAwareSelector` ranks accounts from those snapshots. It sits as the fallback inside upstream's `SessionAffinitySelector`, which gains per-provider idle TTLs and a drain-urgency override hook.
- A management endpoint exposes routing state. The React panel renders it as Theo-style rows.

**Tech Stack:** Go 1.26+ (gin, logrus, gjson) in `server/`; React 19 + TS + Vite + zustand, run with bun, in `panel/`.

**Spec:** `docs/superpowers/specs/2026-10-20-ai-cmd-center-design.md` (read it first). The approved mockup is `docs/mockups/dashboard-options.html`, option A.

## Global Constraints

- `server/` and `panel/` are `git subtree` squashes of upstream `main`. Put new behaviour in **new files**. Keep edits to upstream files small and surgical so `git subtree pull` stays practical.
- The proxy never redeems a bonus reset on its own. Only an explicit user click (with confirm) may call a redeem endpoint.
- Usage numbers come from providers. Nothing estimates usage locally.
- Defaults:
  - Claude affinity idle TTL: `1h`
  - Codex affinity idle TTL: `10m`
  - urgency window: `24h`
  - poll interval: `5m` with ±10% jitter
  - a snapshot is stale after `15m` without a successful fetch
- All-limited error text: `All <Provider> accounts are limited. Earliest frees at <time> (<account>, <window> limit).`
  - `<time>` uses host local time, formatted `3:04 PM`, or `Mon 3:04 PM` when it isn't today.
  - Provider display names are `Claude` and `Codex`.
- Existing JSON fields of the `model_cooldown` error (`code`, `reset_time`, `reset_seconds`, `provider`, `model`) stay unchanged.
- Run server tests with `go test ./sdk/cliproxy/... ./internal/...` from `server/`. Run panel checks with `bun run test`, `bun run type-check` and `bun run build` from `panel/`.
- Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **An account whose quota fetch has never succeeded or keeps failing** stays routable, is ranked after accounts with known data, and shows "quota unknown" in the UI. It never crashes or blocks routing. Task 1 test `TestSnapshotStore_FailureKeepsLastAndMarksStale`, Task 2 test `TestRank_UnknownRankedLast`.
2. **A window whose `resets_at` is already in the past** (stale snapshot across a reset) counts as reset: utilization 0, not limited. Task 2 test `TestRank_PastResetTreatedAsFresh`.
3. **A model-specific weekly window that is full while the general weekly window has room** blocks that account only for matching models. Task 2 test `TestRank_ModelScopedWindowBlocksOnlyThatModel`.
4. **The all-limited message when the earliest free time is tomorrow** includes the weekday. Task 2 test `TestAllLimitedMessage_TomorrowIncludesWeekday`.
5. **Drain urgency must not move sessions onto an account that can't take the model.** It also must not apply when the urgent account is the bound one. Task 3 test `TestAffinity_UrgencyIgnoresUnusableUrgent`.

---

### Task 1: Quota snapshot store and provider usage parsers  *(complex)*

**Files:**
- Create: `server/sdk/cliproxy/auth/quota_snapshot.go`, `server/sdk/cliproxy/auth/quota_snapshot_parse.go`
- Test: `server/sdk/cliproxy/auth/quota_snapshot_test.go`, fixtures under `server/sdk/cliproxy/auth/testdata/quota/` (`claude_usage.json`, `claude_usage_cedar.json`, `codex_usage.json`, `codex_reset_credits.json`)

**Interfaces:**
- Produces:
  ```go
  type QuotaWindow struct {
      Key         string    // provider key, e.g. "five_hour", "seven_day", "seven_day_opus", "primary_window"
      Label       string    // "5-hour", "7-day", "7-day Opus", "Weekly"...
      Utilization float64   // percent used, 0..100
      ResetsAt    time.Time // zero if unknown
      Weekly      bool      // window length >= 6 days
      ModelScope  string    // "" = applies to all models; else lowercase substring matched against the model name ("opus", "sonnet", "fable", or a Codex additional-limit name)
      Routing     bool      // false for display-only windows (seven_day_oauth_apps, seven_day_cowork, code review)
  }
  type BonusResets struct {
      Known     bool      // false when the credits/grants call failed or was absent
      Available int
      Clears    []string  // e.g. ["seven_day"] / ["five_hour"]
      ExpiresAt time.Time // earliest expiry among available grants, zero if none
      Error     string
  }
  type QuotaSnapshot struct {
      AuthID    string
      Provider  string // "claude" | "codex"
      Windows   []QuotaWindow
      Bonus     BonusResets
      FetchedAt time.Time // last successful fetch
      LastError string
      LastErrorAt time.Time
  }
  func (s QuotaSnapshot) Stale(now time.Time, staleAfter time.Duration) bool // true if FetchedAt zero or older than staleAfter
  type QuotaSnapshotStore struct{ /* sync.RWMutex + map */ }
  func NewQuotaSnapshotStore() *QuotaSnapshotStore
  func DefaultQuotaSnapshots() *QuotaSnapshotStore             // process-wide singleton; selectors are rebuilt on config reload, the store is not
  func (s *QuotaSnapshotStore) Get(authID string) (QuotaSnapshot, bool) // returns a deep copy
  func (s *QuotaSnapshotStore) Put(snap QuotaSnapshot)
  func (s *QuotaSnapshotStore) MarkFailed(authID, provider string, err error, at time.Time) // keeps previous Windows/Bonus/FetchedAt
  func (s *QuotaSnapshotStore) Delete(authID string)
  func ParseClaudeUsage(body []byte) ([]QuotaWindow, error)
  func ParseClaudeBonus(body []byte) BonusResets   // reads the "cedar_ember" block
  func ParseCodexUsage(body []byte) ([]QuotaWindow, error)
  func ParseCodexResetCredits(body []byte) BonusResets
  ```

The reference for field semantics is the panel's existing parsers. Mirror them exactly; don't guess.
- `panel/src/services/api/claudeUsage.ts`
- `panel/src/features/quota/providers/claude/data.ts`
- `panel/src/services/api/claudeResetGrants.ts`
- `panel/src/features/quota/providers/codex/data.ts`
- `panel/src/types/quota.ts`

Build the fixtures from the shapes those files parse. Mapping rules:
- **Claude windows:**
  - `five_hour` → Label "5-hour", Weekly false, Routing true
  - `seven_day` → "7-day", Weekly, Routing
  - `seven_day_opus` → ModelScope "opus"
  - `seven_day_sonnet` → "sonnet"
  - `iguana_necktie` → Label "7-day Fable", ModelScope "fable"
  - `seven_day_oauth_apps` and `seven_day_cowork` → Routing false
  - Null or missing windows are omitted.
  - If the panel shows utilization as a 0..1 fraction anywhere, normalize to 0..100.
- **Codex windows:**
  - `rate_limit.primary_window` and `secondary_window`: Weekly = `limit_window_seconds >= 518400`. Label "5-hour" when the window is ≤ 6h, else "Weekly".
  - `ResetsAt` comes from `reset_at` (unix seconds), else `now + reset_after_seconds`. For the `now` fallback, the parser takes the current time via a package var `quotaNow = time.Now` so tests can pin it.
  - `additional_rate_limits[]`: ModelScope = lowercased limit/model name as the panel reads it.
  - `code_review_rate_limit` → Routing false.
- **Claude bonus (`cedar_ember`):** Available = sum of `resets_left` over grants where `usable_now` or not `paused`, mirroring the panel's selectResetGrant logic. Clears is the union of their `clears`. ExpiresAt is the earliest `ends_at`.
- **Codex credits:** Available = `applicable_available_count` (fall back to `available_count`). ExpiresAt is the earliest `expiresAt` among available credits.

- [ ] **Step 1: Write failing tests**
  - `TestParseClaudeUsage_MapsWindows`: the fixture yields 5-hour (Routing, !Weekly), 7-day (Weekly, ModelScope ""), 7-day Opus (ModelScope "opus"), and 7-day Fable (ModelScope "fable"). `seven_day_oauth_apps` has Routing false. Assert exact Utilization and ResetsAt values from the fixture.
  - `TestParseClaudeBonus_CountsUsableGrants`: a fixture with 1 usable grant (resets_left 1, clears ["seven_day"]) → `Known:true, Available:1, Clears:["seven_day"]`. A missing block gives `Known:false`.
  - `TestParseCodexUsage_PrimarySecondaryAndAdditional`: primary 18000s → "5-hour", secondary 604800s → Weekly. One additional limit carries its ModelScope. `reset_after_seconds` resolves against the pinned `quotaNow`.
  - `TestParseCodexResetCredits`: `applicable_available_count: 2` → Available 2, ExpiresAt is the earliest.
  - `TestSnapshotStore_FailureKeepsLastAndMarksStale`: Put a snapshot, then MarkFailed 20m later. Get still returns the Windows and sets LastError, and `Stale(now, 15m)` is true. MarkFailed on an unknown ID creates a snapshot with zero FetchedAt (Stale true).
  - `TestSnapshotStore_GetReturnsCopy`: mutating a returned Windows slice doesn't affect the store.
- [ ] **Step 2:** Run `go test ./sdk/cliproxy/auth/ -run 'Quota|Snapshot|ParseC' -v`. Expected: FAIL (undefined).
- [ ] **Step 3:** Implement the two files. Use `gjson`, which is already a dependency, for parsing.
- [ ] **Step 4:** Rerun. Expected: PASS. Also run `go vet ./sdk/cliproxy/auth/`.
- [ ] **Step 5:** Commit `feat(server): quota snapshot store and Claude/Codex usage parsers`.

---

### Task 2: ResetAwareSelector and the all-limited message  *(complex)*

**Files:**
- Create: `server/sdk/cliproxy/auth/resetaware_selector.go`
- Modify: `server/sdk/cliproxy/auth/selector.go`. Only `modelCooldownError`: add optional fields and the friendly message.
- Test: `server/sdk/cliproxy/auth/resetaware_selector_test.go`

**Interfaces:**
- Consumes: Task 1 `QuotaSnapshotStore`, `QuotaSnapshot`, `QuotaWindow`.
- Produces:
  ```go
  type ResetAwareConfig struct {
      Store         *QuotaSnapshotStore // nil → DefaultQuotaSnapshots()
      UrgencyWindow time.Duration       // default 24h
      StaleAfter    time.Duration       // default 15m
      Now           func() time.Time    // default time.Now
  }
  type RankedAuth struct {
      Auth          *Auth
      Usable        bool
      Rank          int       // 1-based among usable for this provider+model; 0 when !Usable
      Known         bool      // snapshot present and not stale
      WeeklyResetAt time.Time // ranking key; zero if unknown
      Headroom      float64   // 100 - max utilization over relevant weekly windows
      LimitedBy     string    // label of the blocking window, e.g. "5-hour"; "" when usable
      FreeAt        time.Time // when the blocking window resets; zero when usable/unknown
      Urgent        bool      // rank 1, Known, WeeklyResetAt within UrgencyWindow, Headroom > 0
  }
  func NewResetAwareSelector(cfg ResetAwareConfig) *ResetAwareSelector
  func (s *ResetAwareSelector) Pick(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, auths []*Auth) (*Auth, error)
  func (s *ResetAwareSelector) Rank(provider, model string, auths []*Auth) []RankedAuth // usable first by rank, then unusable by FreeAt
  func (s *ResetAwareSelector) Urgent(provider, model string, auths []*Auth) (*Auth, bool)
  func NewAllLimitedError(provider, model string, freeAt time.Time, account, window string) error // *modelCooldownError, 429
  ```

**Ranking algorithm.** The spec fixes it, so here it is in full:
```
relevant(window, model) = window.Routing && (window.ModelScope == "" || strings.Contains(lower(model), window.ModelScope))
effective utilization   = 0 if window.ResetsAt non-zero and <= now, else window.Utilization
usable                  = no relevant window has effective utilization >= 100
weekly key              = earliest ResetsAt among relevant Weekly windows with effective utilization < 100
headroom                = 100 - max effective utilization among relevant Weekly windows
order usable: Known before unknown; then weekly key asc (zero last); then headroom desc; then authPriority desc; then Auth.ID asc
```
- If no snapshot exists or it's stale, the account is usable and unknown, but it keeps the last windows' "limited" verdict only if the blocking window's ResetsAt is still in the future.
- `Pick` gets auths that upstream has already filtered for cooldowns and availability. It returns the rank-1 usable one and keeps upstream's `preferCodexWebsocketAuths` behaviour.
- If none is usable, it returns `NewAllLimitedError`. Use the unusable account with the earliest `FreeAt`, with its `Label` (falling back to ID) and the `LimitedBy` window.

**Error message.** Add `freeAt time.Time; account, window string` to `modelCooldownError`.
- When provider is `claude` or `codex`, `message` becomes the Global Constraints text.
- When `freeAt` is zero (the upstream cooldown path), the time comes from `now + resetIn` and the parenthetical is dropped.
- All other providers keep the upstream message.

- [ ] **Step 1: Write failing tests**, using a fake store and a fixed `Now`.
  - `TestRank_SoonestWeeklyResetFirst`: A resets in 1 day at 60% weekly used, B in 4 days at 10%. Pick → A, Rank → A#1, B#2.
  - `TestRank_FiveHourFullSpillsAndReturns`: A's 5-hour window is at 100% resetting in 2h. Pick → B, and A has `LimitedBy:"5-hour"` and `FreeAt` +2h. Advance Now by 2h1m: Pick → A.
  - `TestRank_ModelScopedWindowBlocksOnlyThatModel`: A has `seven_day_opus` at 100% and general weekly at 40%. Model `claude-opus-5-5` → B. Model `claude-sonnet-5-5` → A.
  - `TestRank_UnknownRankedLast`: A has no snapshot, B is known. Pick → B. With only A → A.
  - `TestRank_PastResetTreatedAsFresh`: A's weekly window is at 100% with ResetsAt 1m ago. A is usable.
  - `TestUrgent_WithinWindowWithHeadroom`: A resets in 20h with 30% headroom → `Urgent` returns A. In 30h → false. At 0% headroom → false.
  - `TestPick_AllLimitedReturnsFriendlyError`: A's 5-hour window frees in 40m and B's in 3h. The error status is 429, the message is exactly `All Claude accounts are limited. Earliest frees at 3:40 PM (claude-a, 5-hour limit).` with Now pinned at 3:00 PM local, and `reset_seconds` is 2400.
  - `TestAllLimitedMessage_TomorrowIncludesWeekday`: freeAt is the next day → `Earliest frees at Wed 9:00 AM`, matching the pinned date.
  - `TestModelCooldownError_UpstreamPathFriendlyForClaude`: `NewModelCooldownError("m","claude",10*time.Minute)` gives the message `All Claude accounts are limited. Earliest frees at <time>.` Gemini keeps the upstream text.
- [ ] **Step 2:** Run `go test ./sdk/cliproxy/auth/ -run 'Rank|Urgent|AllLimited|ModelCooldownError_Upstream' -v`. Expected: FAIL.
- [ ] **Step 3:** Implement. Make time formatting injectable via the error's own `now` so tests are deterministic. Add an unexported `cooldownNow = time.Now` package var.
- [ ] **Step 4:** Run the new tests, then the full package: `go test ./sdk/cliproxy/auth/`. Expected: PASS. Existing cooldown tests that assert the old message for claude/codex may need their expected string updated. Change only the expectation, and list each such test in the commit body.
- [ ] **Step 5:** Commit `feat(server): reset-aware selector and friendly all-limited error`.

---

### Task 3: Affinity: per-provider idle TTL, drain-urgency override, session counts, config wiring  *(complex)*

**Files:**
- Modify:
  - `server/sdk/cliproxy/auth/session_cache.go`: TTL resolver and `CountByAuth`
  - `server/sdk/cliproxy/auth/selector.go`: `SessionAffinityConfig.ProviderTTL`, the override hook in `Pick`, and `BindingCounts`
  - `server/internal/config/config_types.go`: `RoutingConfig` fields
  - `server/sdk/cliproxy/service_config.go`: the `reset-aware` strategy
  - `server/internal/api/handlers/management/config_basic.go` (~line 319): accept `reset-aware`
  - `server/sdk/cliproxy/auth/conductor_selection.go`: a `Selector()` getter
- Test: `server/sdk/cliproxy/auth/resetaware_affinity_test.go`, `server/sdk/cliproxy/service_config_resetaware_test.go`

**Interfaces:**
- Consumes: Task 2 `ResetAwareSelector` (`Urgent`, `Rank`), `NewResetAwareSelector`, `ResetAwareConfig`.
- Produces:
  ```go
  // session_cache.go
  func (c *SessionCache) SetTTLResolver(fn func(cacheKey string) time.Duration) // nil/<=0 result → default ttl
  func (c *SessionCache) CountByAuth() map[string]int // distinct live alias groups per authID
  // selector.go
  type SessionAffinityConfig struct { /* existing */ ; ProviderTTL map[string]time.Duration }
  type BindingOverrider interface {
      // OverrideBinding returns a different auth the session must move to now, or false to keep the binding.
      OverrideBinding(provider, model, boundAuthID string, available []*Auth) (*Auth, bool)
  }
  func (s *SessionAffinitySelector) BindingCounts() map[string]int
  func (s *SessionAffinitySelector) Fallback() Selector
  // ResetAwareSelector implements BindingOverrider: returns Urgent(provider, model, available) when that auth != boundAuthID.
  // conductor_selection.go
  func (m *Manager) Selector() Selector
  // config_types.go RoutingConfig additions
  SessionAffinityTTLByProvider map[string]string `yaml:"session-affinity-ttl-by-provider,omitempty" json:"session-affinity-ttl-by-provider,omitempty"`
  ResetAware ResetAwareRoutingConfig `yaml:"reset-aware,omitempty" json:"reset-aware,omitempty"`
  type ResetAwareRoutingConfig struct {
      UrgencyWindow string `yaml:"urgency-window,omitempty" json:"urgency-window,omitempty"` // default "24h"
      PollInterval  string `yaml:"poll-interval,omitempty" json:"poll-interval,omitempty"`   // default "5m", consumed by Task 4
  }
  ```

Details:
- **Cache keys** are `provider + "::" + session + "::" + model` (see `SessionAffinitySelector.Pick`). The resolver installed by `NewSessionAffinitySelectorWithConfig` reads the prefix before the first `::` and looks it up in `ProviderTTL`.
- **Apply the resolver** wherever `c.ttl` sets an expiry: `GetAndRefresh`, `Set`/`SetAliases`, `Touch`. The cleanup interval must use the minimum TTL in effect.
- **Strategy `reset-aware`** in `normalizedRoutingRuntimeState`/`newRoutingSelector` builds `NewResetAwareSelector(ResetAwareConfig{Store: DefaultQuotaSnapshots(), UrgencyWindow: parsed})`.
  - It forces session affinity on, wrapping the selector in `SessionAffinitySelector`.
  - Default `ProviderTTL` is `{claude: 1h, codex: 10m}` when `session-affinity-ttl-by-provider` is unset. Explicit entries override per provider.
  - The global `session-affinity-ttl` stays the default for other providers.
- **Override hook in `SessionAffinitySelector.Pick`:**
  - Both cache-hit branches use it: the primary `GetAndRefresh` hit, and the `fallbackKey` hit for non-subagent sessions.
  - When the bound auth is in `available` and `s.fallback` implements `BindingOverrider`, call it with `fallbackAuths`. If it returns `(a, true)`, `bind(a.ID)`, log `session-affinity: drain-urgency rebind | session=… from=… to=…`, and return `a`.
  - Subagents follow their parent's binding as upstream does, and need no extra handling.

- [ ] **Step 1: Write failing tests**
  - `TestSessionCache_ProviderTTL`: resolver claude=1h, codex=10m. A codex binding is gone after 11m (inject the clock as session_cache already allows, or use `Get` after manipulating expiresAt via a test helper in the same package). A claude binding survives 11m and is refreshed by `GetAndRefresh`.
  - `TestSessionCache_CountByAuth`: 2 sessions on A (one with 2 aliases), 1 on B → `{A:2, B:1}`. Expired entries aren't counted.
  - `TestAffinity_StaysOnBoundWhileActive`: the session is bound to B. A becomes rank 1 but isn't urgent (resets in 3 days) → it stays on B.
  - `TestAffinity_ReroutesAfterProviderIdleTTL`: a codex session is idle 11m and then gets the rank-1 auth. A claude session idle 11m stays.
  - `TestAffinity_UrgencyRebindsOnNextRequest`: bound to B, A resets in 20h with headroom → the next Pick returns A and the binding is now A.
  - `TestAffinity_UrgencyIgnoresUnusableUrgent`: A is urgent but its 5-hour window is full, or A can't serve the model → it stays on B. Also, a session already bound to A is unaffected.
  - `TestAffinity_MovesWhenBoundLimited`: B is removed from `available` → rebinds to the rank-1 account (upstream behaviour preserved).
  - `TestRoutingState_ResetAwareStrategy` (in `service_config_resetaware_test.go`): `routing.strategy: reset-aware` produces a `*SessionAffinitySelector` whose `Fallback()` is a `*ResetAwareSelector`, with claude TTL 1h and codex 10m. The override `{codex: 5m}` is honoured.
- [ ] **Step 2:** Run `go test ./sdk/cliproxy/... -run 'ProviderTTL|CountByAuth|Affinity_|ResetAwareStrategy' -v`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test ./sdk/cliproxy/... ./internal/config/... ./internal/api/handlers/management/...`. Expected: PASS, with all existing affinity tests still green.
- [ ] **Step 5:** Commit `feat(server): per-provider affinity TTL, drain-urgency rebind, reset-aware strategy wiring`.

---

### Task 4: Server-side quota poller  *(complex)*

**Files:**
- Create: `server/internal/quotapoll/poller.go`, `server/internal/quotapoll/fetch.go`
- Modify: the service startup and shutdown, where the auto-refresh loop or management asset updater is started (find via `StartAutoUpdater` / `Manager` construction in `sdk/cliproxy/builder.go` / `service.go`). Also add a result hook that triggers a refresh on 429.
- Test: `server/internal/quotapoll/poller_test.go`

**Interfaces:**
- Consumes: Task 1 store and parsers; `coreauth.Manager.List()`; Task 3 `RoutingConfig.ResetAware.PollInterval`.
- Produces:
  ```go
  package quotapoll
  type Endpoints struct { ClaudeUsage, ClaudeBonus, CodexUsage, CodexCredits string } // defaults = real URLs; tests point at httptest
  type Config struct {
      Manager   *coreauth.Manager
      Store     *coreauth.QuotaSnapshotStore
      Interval  time.Duration // default 5m
      Endpoints Endpoints
      Client    func(a *coreauth.Auth) *http.Client // honours per-auth/global proxy; default plain client with 20s timeout
  }
  func New(cfg Config) *Poller
  func (p *Poller) Start(ctx context.Context) // first sweep immediately, then every Interval ±10% jitter; stops on ctx done
  func (p *Poller) Refresh(ctx context.Context, authID string) error // synchronous single-account fetch
  func (p *Poller) RefreshSoon(authID string)  // debounced (30s per auth), async; used by the 429 hook
  func SetDefault(p *Poller); func Default() *Poller // accessor for the management handler (nil-safe)
  ```

Requests (copy the headers from `panel/src/utils/quota/constants.ts` and the codex `data.ts`):
- **Claude usage:** `GET https://api.anthropic.com/api/oauth/usage?cedar_ember=1&skip_spend=1`, with `Authorization: Bearer <metadata.access_token>`, `anthropic-beta: oauth-2025-04-20`, and the panel's `User-Agent`. One call gives both windows (`ParseClaudeUsage`) and bonus (`ParseClaudeBonus`).
- **Codex usage:** `GET https://chatgpt.com/backend-api/wham/usage`, with `Authorization: Bearer <token>` and `Chatgpt-Account-Id` from auth metadata exactly as the codex `data.ts` resolves it. Then `GET …/wham/rate-limit-reset-credits` for the bonus. A failed credits call sets `Bonus.Known=false` with Error, and keeps the windows.
- Token resolution follows `tokenValueForAuth` in `internal/api/handlers/management/api_tools.go`. Move the small helper into a shared spot if needed, without changing its behaviour.
- Skip disabled auths and providers other than claude and codex. Remove snapshots for auths that no longer exist.
- Never call any redeem/consume/reset endpoint. Add a test that fails if one is hit.
- On 401, 403, 5xx or a network error, call `MarkFailed`. Back off that account: the next attempt is at `min(Interval*2^n, 30m)`.
- Wire the poller in only when the routing strategy is `reset-aware`. Hook 429 results for claude/codex auths to call `RefreshSoon`.

- [ ] **Step 1: Write failing tests** using `httptest.Server` serving the Task 1 fixtures.
  - `TestPoller_RefreshClaudeStoresWindowsAndBonus`: the request carries the right headers and token, and the store has the windows and Bonus.Available 1.
  - `TestPoller_RefreshCodexUsesAccountHeaderAndCredits`: `Chatgpt-Account-Id` is sent and Bonus.Available is 2. A 500 from the credits endpoint → windows stored, `Bonus.Known=false`.
  - `TestPoller_FailureMarksFailedAndBacksOff`: 401 → `LastError` set, previous windows kept, and the next scheduled attempt is after 2×Interval.
  - `TestPoller_NeverCallsRedeem`: the server fails the test on any path containing `reset_rate_limits` or `consume`.
  - `TestPoller_RefreshSoonDebounces`: 3 calls within 30s → 1 fetch.
  - `TestPoller_SkipsDisabledAndOtherProviders`.
- [ ] **Step 2:** Run `go test ./internal/quotapoll/ -v`. Expected: FAIL.
- [ ] **Step 3:** Implement, then wire into startup and shutdown.
- [ ] **Step 4:** Run `go test ./internal/quotapoll/ ./sdk/cliproxy/...` and `go build ./cmd/server`. Expected: PASS and the build succeeds.
- [ ] **Step 5:** Commit `feat(server): background quota poller for Claude and Codex`.

---

### Task 5: Routing-state management endpoint  *(simpler)*

**Files:**
- Create: `server/internal/api/handlers/management/routing_state.go`
- Modify: `server/internal/api/server_management.go`, to register the routes next to `mgmt.POST("/reset-quota", …)`
- Test: `server/internal/api/handlers/management/routing_state_test.go`

**Interfaces:**
- Consumes:
  - `h.authManager.List()`, `h.authManager.Selector()` (Task 3)
  - `*SessionAffinitySelector.BindingCounts()` / `Fallback()` (Task 3)
  - `*ResetAwareSelector.Rank(provider, "", auths)` (Task 2; an empty model means only general windows count)
  - `DefaultQuotaSnapshots()` (Task 1), `quotapoll.Default().Refresh` (Task 4)
- Produces:
  - `GET /v0/management/routing-state` returns
    ```json
    {"strategy":"reset-aware","generated_at":"RFC3339",
     "providers":{"claude":{"urgent_auth_index":"…|\"\"","earliest_free_at":"RFC3339|\"\""}, "codex":{…}},
     "accounts":[{"auth_index":"…","provider":"claude","label":"…","status":"draining|standby|limited|unknown",
       "urgent":false,"rank":1,"sessions":3,"limited_by":"","free_at":"",
       "weekly_resets_at":"RFC3339|\"\"","headroom":38.0,
       "quota":{"fetched_at":"","stale":false,"last_error":"",
         "windows":[{"key":"five_hour","label":"5-hour","utilization":28.0,"resets_at":"","weekly":false,"model_scope":"","routing":true}],
         "bonus":{"known":true,"available":1,"clears":["seven_day"],"expires_at":""}}}]}
    ```
  - `status` is `draining` for rank 1, `standby` for other usable accounts, `limited` when not usable, and `unknown` when usable without Known data.
  - When the strategy isn't reset-aware, return the same shape with `rank` 0 and status from upstream availability only.
  - `POST /v0/management/routing-state/refresh` with body `{"auth_index":"…"}` (omit it for all) calls the poller synchronously and returns the fresh state. If the poller is nil, return 409 `{"error":"quota poller not running (routing.strategy is not reset-aware)"}`.
  - Use `auth_index` (not the auth ID) the way `ResetQuota` does, and copy how `ResetQuota` resolves it.

- [ ] **Step 1: Write failing tests**
  - `TestRoutingState_ReportsRankStatusSessions`: 2 claude auths with snapshots → A draining #1 and B standby #2, with session counts from a seeded affinity cache.
  - `TestRoutingState_LimitedAndUnknown`.
  - `TestRoutingState_RefreshWithoutPoller409`.
  - `TestRoutingState_RequiresManagementAuth`: hits the route through the router with no key → 401/404, the same as other management routes.
- [ ] **Step 2:** Run `go test ./internal/api/handlers/management/ -run RoutingState -v`. Expected: FAIL.
- [ ] **Step 3:** Implement and register the routes.
- [ ] **Step 4:** Rerun, plus `go test ./internal/api/...`. Expected: PASS.
- [ ] **Step 5:** Commit `feat(server): routing-state management endpoint`.

---

### Task 6: Panel: Theo-style rows with routing status and bonus-reset banner  *(complex)*

**Files:**
- Create:
  - `panel/src/services/api/routingState.ts`: fetch and normalize, following the style of `authFileCooldowns.ts`
  - `panel/src/features/quota/routing/RoutingStatusPill.tsx`
  - `panel/src/features/quota/routing/BonusResetBanner.tsx`
  - `panel/src/features/quota/routing/useRoutingState.ts`: zustand slice or hook. Polls every 30s while the page is visible and refetches after "Refresh quota".
  - Tests next to each file, as `*.test.ts(x)` (bun test).
- Modify:
  - `panel/src/features/quota/QuotaPage.tsx`: banner at the top, and the routing state passed down
  - `panel/src/features/quota/components/QuotaCard.tsx` + scss: row layout and the status cell
  - `panel/src/features/quota/providers/claude/ClaudeQuotaBody.tsx` and `codex/CodexQuotaBody.tsx`: windows as columns in the row
  - locale files under `panel/src/i18n` (or wherever `claude_quota.*` keys live): new keys in `en` (other locales may fall back to en)

**Interfaces:**
- Consumes: the Task 5 JSON.
- Produces: `type RoutingAccount = { authIndex: string; provider: 'claude'|'codex'|string; status: 'draining'|'standby'|'limited'|'unknown'; urgent: boolean; rank: number; sessions: number; limitedBy: string; freeAt?: string; weeklyResetsAt?: string; headroom: number; bonus: { known: boolean; available: number; clears: string[]; expiresAt?: string } }` and `fetchRoutingState(): Promise<{ strategy: string; accounts: RoutingAccount[] } | null>`. It returns null on a 404, for older servers.

Behaviour (matching mockup A):
- **Claude/Codex accounts render as rows:**
  - First, the account cell: masked name, plan, and status pill.
  - Then one column per window, with label, %, bar and "in 1 day · 10/21 13:00" (reuse `QuotaMeter`/`QuotaResetLabel`).
  - Then Refresh quota.
- **Pill text:**
  - `● Draining · #1`
  - `Standby · #2`
  - `Limited until 3:40 PM (5-hour)`
  - `Quota unknown`
  - When urgent: `⚡ Urgent: resets in 14h, 38% left`
  - Next to the pill: `N sessions`
- **Bonus resets:**
  - A badge on the row: `1 reset available · clears 7-day · expires 10/25`, or `resets: unknown` when `known:false`.
  - `BonusResetBanner` at the top lists every account with `available > 0` and links to the existing redeem UI (`ClaudeResetGrants` / Codex credits section).
  - The existing redeem buttons stay, and must go through a confirm step. Add one if upstream doesn't have it. Nothing redeems automatically.
- **Refresh quota** on a row calls the existing per-card refresh and also `POST /routing-state/refresh` for that `auth_index`.
- Provider summary cards at the top (Theo's "409% of 500%" style) and the masked-email "Show emails" toggle: reuse what upstream's quota page already has. Add each provider's next weekly reset to the card if it's missing.
- Other providers' cards are unchanged.

- [ ] **Step 1: Write failing tests**
  - `routingState.test.ts`: normalizes the Task 5 sample and drops malformed accounts. A 404 → null.
  - `RoutingStatusPill.test.tsx`: renders each of the 5 texts above for fixture inputs, with a fixed clock.
  - `BonusResetBanner.test.tsx`: hidden when no account has `available > 0`. It lists 2 accounts when 2 do, and has no redeem action of its own.
- [ ] **Step 2:** Run `bun test src/features/quota/routing src/services/api/routingState`. Expected: FAIL.
- [ ] **Step 3:** Implement the components and the layout changes.
- [ ] **Step 4:** Run `bun run test && bun run type-check && bun run lint && bun run build`. Expected: all pass, producing `dist/index.html` (or the configured single-file output; note its path).
- [ ] **Step 5:** Commit `feat(panel): routing status, Theo-style quota rows, bonus reset banner`.

---

### Task 7: Packaging, config template, startup and client docs  *(simpler)*

**Files:**
- Create:
  - `deploy/config.example.yaml`
  - `scripts/build.ps1` and `scripts/build.sh`: build the server binary into `dist/`, build the panel, and copy the single-file panel to `dist/static/management.html`
  - `deploy/windows/install-task.ps1`: Task Scheduler at logon
  - `deploy/macos/com.aicmdcenter.proxy.plist`
  - `deploy/linux/ai-cmd-center.service`
  - `docs/setup.md`
  - `docs/UPSTREAM.md`
  - root `README.md`
- Modify: nothing in `server/` or `panel/`.

**Content requirements:**
- **`config.example.yaml`** is based on `server/config.example.yaml` and includes:
  - `routing.strategy: reset-aware`
  - `routing.session-affinity: true`
  - `routing.session-affinity-ttl-by-provider: {claude: 1h, codex: 10m}`
  - `routing.reset-aware: {urgency-window: 24h, poll-interval: 5m}`
  - `remote-management.allow-remote: true`
  - `remote-management.disable-auto-update-panel: true`
  - `remote-management.secret-key: CHANGE-ME`
  - `server.host: <TAILSCALE_IP>` (comment: run `tailscale ip -4`) and `port: 8317`
  - `access.api-keys` with two placeholders, `machine-1-CHANGE-ME` and `machine-2-CHANGE-ME`
  - `auth-dir`
  - Verify the exact key names against `server/internal/config` before writing.
- **`docs/setup.md`**, in order:
  1. Prerequisites (Go, bun, Tailscale).
  2. Build.
  3. Configure, including which IP to bind and generating keys.
  4. Log in to the accounts: `server -claude-login` twice and `-codex-login` once, with `-no-browser` noted for headless use.
  5. Run at startup on Windows, macOS and Linux.
  6. Point Claude Code at it: `ANTHROPIC_BASE_URL=http://<tailscale-ip>:8317`, `ANTHROPIC_AUTH_TOKEN=<machine key>`, plus the PowerShell and bash forms and the settings.json `env` form.
  7. Point Codex at it: a `~/.codex/config.toml` provider block with `base_url = "http://<tailscale-ip>:8317/v1"` and the key env var. Verify against the upstream README/docs in `server/` for the exact Codex wiring.
  8. Verify: open `http://<tailscale-ip>:8317/management.html`, start Claude Code, and see "1 session" on the draining account.
  9. Troubleshooting: the all-limited message, "Quota unknown", and how to change the TTLs and urgency window.
  10. A terms-of-service risk note (one paragraph, from the spec).
- **`docs/UPSTREAM.md`:** how to `git subtree pull --prefix server https://github.com/router-for-me/CLIProxyAPI.git main --squash` (same for panel), and the list of upstream files we modified (from Tasks 2–6).

- [ ] **Step 1:** Write the files.
- [ ] **Step 2:** Run `scripts/build.ps1` (or `.sh` in Git Bash). Expected: `dist/` contains the server binary and `dist/static/management.html`.
- [ ] **Step 3:** Smoke-test: start `dist/server -config <temp copy of the example with host 127.0.0.1 and test keys>`.
  - `curl -s -H "Authorization: Bearer <secret>" http://127.0.0.1:8317/v0/management/routing-state` returns JSON with `"strategy":"reset-aware"` and an empty `accounts`.
  - `/management.html` returns 200.
  - Stop the server afterwards.
- [ ] **Step 4:** Commit `docs: setup, packaging and startup scripts`.

---

### Task 8: Whole-branch verification

- [ ] Run `cd server && go vet ./... && go test ./...`. Expected: PASS. If upstream tests that also fail on a clean upstream checkout fail here, list them separately.
- [ ] Run `cd panel && bun run verify`. Expected: PASS.
- [ ] Run `scripts/build.ps1`. Expected: artifacts in `dist/`.
- [ ] Final review against the spec's goals 1–6 and anti-goals.
