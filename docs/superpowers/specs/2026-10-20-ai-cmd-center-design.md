# AI Command Center — Design

Date: 2026-10-20
Status: draft, awaiting approval

## Intent

One self-hosted proxy holds the user's AI subscriptions (2 Claude accounts, 1 Codex/ChatGPT account today) and decides which account serves each request. Claude Code and Codex CLI on the user's machines point at it instead of logging in directly.

The why: weekly allowances are use-it-or-lose-it. An account that resets tomorrow should be drained before one that resets in 4 days, so less paid-for capacity is wasted. At the same time, a conversation must stay on one account while it's active, or every turn pays to rewrite the prompt cache on a different account.

Inspiration: Theo's modified CLIProxyAPI setup ("CPAMC" quota page, video https://www.youtube.com/watch?v=D8PikZ1KhUo). The user wants the same, plus reset-aware routing and visible bonus resets.

## Decisions made by the user

- **Fork CLIProxyAPI** (as Theo did), not a fresh project. All code lives in `C:\dev\ai-cmd-center`.
- **Routing rule:** for each provider, new work goes to the account whose weekly limit resets soonest and still has headroom.
- **Affinity rule:** a conversation stays on its account while active. Once it has been idle longer than **that provider's prompt-cache lifetime**, its next turn is re-routed by the routing rule. This resolves the conflict where a session pinned to account B keeps running after A frees up: B keeps it while active, A gets it back after idle.
- **Urgency beats affinity:** if the account that should be drained first resets soon (e.g. tomorrow) with allowance left, sessions on other accounts move to it on their next turn even if their cache is warm. "I don't care to be inefficient and swap accounts, because I need to get this account drained in time."
- **All accounts limited → fail fast** with a clear message saying when the earliest account frees up. Requests are never held or queued.
- **Dashboard:** Theo-style quota table (mockup option A): one row per account, grouped by provider, each limit as a bar with % and reset time, plus a routing status column.
- **Bonus resets** (one-time resets that Claude/Codex occasionally grant): the dashboard makes them obvious. **The proxy never applies one itself.** The user applies it manually.
- **Hosting:** one instance runs on the user's *other* machine, reachable from both machines over Tailscale (the machines are on different networks). Running a second independent instance is a fallback, not the plan.

## Goals

1. Reset-aware account selection per provider (Claude, Codex), with 5-hour-limit spillover.
2. Session affinity with a sliding idle release matched to each provider's cache lifetime (Claude 1h, Codex 10m, configurable), overridden when an account must be drained before an imminent reset.
3. A quota dashboard with live per-account limits, reset times, routing status, rank, pinned-session counts, and bonus-reset availability.
4. A fail-fast error naming the provider, the clock time the earliest account frees up, and which account that is.
5. Tailnet-only access, with API keys for clients and a password for the dashboard.
6. Setup docs: host install, run on startup, account logins, and copy-paste client config for Claude Code and Codex on both machines.

## Non-goals

- Automatically redeeming bonus resets.
- Theo's plugins/plugin store, and providers other than Claude and Codex (whatever upstream already supports is left untouched, not removed).
- Coordinating between multiple proxy instances.
- Estimating usage locally. Usage numbers come from the providers.
- Queueing or waiting when everything is limited.

## Anti-goals (failure even if it "works")

- Switching an active conversation between accounts when it didn't have to. This silently burns cache. (A drain-urgency move counts as having to.)
- An account reaching its weekly reset with allowance left while traffic was going elsewhere.
- The proxy reachable from the public internet, or usable without a key.
- A bonus reset used without the user deciding to.
- A limit error that doesn't say when things free up.
- Changes so tangled into upstream code that pulling upstream fixes becomes impractical.

## Constraints and facts from recon of upstream (2026-10-20)

Upstream repos (both MIT):
- Server: `github.com/router-for-me/CLIProxyAPI`. Go + gin, module `.../CLIProxyAPI/v8`.
- Panel: `github.com/router-for-me/Cli-Proxy-API-Management-Center`. React 19 + TS + Vite, built into a single `management.html`. The server normally downloads it from a GitHub release at runtime (`internal/managementasset/updater.go`, config `management.panel-github-repository`, `disable-auto-update-panel`).

What already exists, and should be reused:
- **Selector plumbing:** `Selector` interface (`sdk/cliproxy/auth/conductor.go`). Built-ins are in `selector.go`, and config mapping is in `sdk/cliproxy/service_config.go` (`routing.strategy`). Unknown selector types go through the scheduler's generic custom path.
- **Session affinity:** `SessionAffinitySelector` + `session_cache.go`, config `routing.session-affinity`, `session-affinity-ttl` (sliding, default 1h), `session-affinity-rebind-on`, and `session-affinity-subagents`. Subagents bind to their parent's account.
  - Session IDs come from `X-Claude-Code-Session-Id`, Claude `metadata.user_id`, Codex `Session-Id` / `X-Codex-Turn-Metadata`, `prompt_cache_key`, and so on, with a first-message hash as the last resort.
  - It already fails over when the bound account becomes unavailable.
- **Passive quota signals:** `quota_signals.go` stores Claude `anthropic-ratelimit-unified-*` and Codex `x-codex-*` headers in `auth.Quota.Signals`.
- **Cooldown and the all-limited error:** `conductor_cooldown.go`; 429 `model_cooldown` with `Retry-After`, `reset_seconds` (`selector.go`).
- **Panel quota page:** `src/features/quota/` with Claude and Codex providers. It fetches usage through the server's `POST /v0/management/api-call` proxy.
  - Claude: `GET api.anthropic.com/api/oauth/usage`, with windows `five_hour`, `seven_day`, `seven_day_opus`, `seven_day_sonnet`, …, each `{utilization, resets_at}`.
  - Codex: `GET chatgpt.com/backend-api/wham/usage`, with `primary_window` / `secondary_window` plus per-model `additional_rate_limits`.
- **Bonus resets UI already exists:**
  - Claude `cedar_ember` grants (`?cedar_ember=1`) with a redeem call (`ClaudeResetGrants.tsx`).
  - Codex `wham/rate-limit-reset-credits`.
  - These are undocumented internal endpoints and may change without notice.

What's missing:
- The **server** never polls usage endpoints. The only polling is in the panel, which is too late for routing.
- No reset-aware strategy exists.
- No endpoint exposes routing state (rank, status, pinned sessions).
- The all-limited error reports a duration from cooldown state, not a clock time from real limits.

## Design

### 1. Server-side quota poller (new)

- A background poller in the server fetches each Claude/Codex account's usage endpoint on an interval. Default is 5 min, configurable, with jitter.
- It also fetches when an account returns a 429, and when "Refresh quota" is clicked.
- It stores the result in a per-account in-memory quota snapshot: each window's utilization and reset time, the fetch time, and the bonus-reset summary.
- Passive response headers update the snapshot between polls where they carry the same information.
- The panel reads the server's snapshot instead of calling upstream itself. Either it uses the new snapshot endpoint, or it keeps its current calls, which is the builder's call. Either way, the dashboard and the router must agree on the numbers.
- Polling must be gentle: one request per account per interval, backing off on errors. A failed poll keeps the last snapshot, marked stale.

### 2. Reset-aware selector (new: `ResetAwareSelector`)

It's used as the `Fallback` inside the existing `SessionAffinitySelector`, so affinity runs first and reset-aware picks unbound or expired sessions. Exposed as `routing.strategy: reset-aware`.

For a request on provider P and model M, it considers that provider's accounts that can serve M:

1. **Usable** = not in cooldown, and every window relevant to M (5-hour, general weekly, and the model-specific weekly window if one exists for M) is below its limit.
2. **Rank** usable accounts by the reset time of their *weekly* window, earliest first.
   - Builder's call: if a model-specific weekly window applies (for example `seven_day_opus` for Opus), use the earlier of that window and the general weekly window. Otherwise use the general weekly window.
   - Tie-break: more remaining headroom first, then config `priority`.
3. **Unknown or stale quota** (never fetched, or fetch failing): usable, but ranked after accounts with known data.
4. **5-hour spillover** falls out of rule 1. While the top account's 5-hour window is full it isn't usable, so the next one takes new work. When the window clears, it ranks first again.

**Affinity idle timeout follows each provider's cache lifetime (user decision).** The release happens when the cache would have expired anyway, so moving costs nothing.
- **Claude:** default 1h. Upstream already upgrades Claude OAuth requests to the 1-hour prompt cache (`claude_executor_cloaking.go`, `upgradeClaudeCacheControlTTL`).
- **Codex:** default 10m (my call; OpenAI's cache lifetime is shorter and not precisely documented).
- Both are configurable per provider. Upstream has one global `session-affinity-ttl`, so this needs a small change to the selector/session cache to look up the TTL by provider. Keep the global key as the fallback.
- The TTL slides from the last activity on the session.

**Drain-urgency override (user decision).** If the account that should be drained first is about to reset with allowance left, draining it in time beats cache efficiency.
- **Urgent** = the rank-1 usable account for a provider resets its weekly window within the urgency window (default 24h, configurable; my call based on "if the drain date is tomorrow") and still has weekly headroom.
- While a provider has an urgent account, any session bound to a *different* account of that provider is rebound to the urgent account on its **next request**, even if active and its cache is warm. Sessions already on the urgent account stay.
- The move happens on the next turn, never in the middle of a response.
- When the urgent account becomes unusable (5-hour full, weekly exhausted) or resets, the override ends and normal affinity applies.
- The dashboard shows "Urgent: resets in 14h, 38% left" on that account's status pill so it's clear why sessions moved.

**Limit hit mid-conversation:** the session rebinds to the next usable account (upstream already fails over). It loses its cache but doesn't stall.

The builder should confirm the existing TTL slides from the last activity (recon thinks yes) and that a binding past its TTL is re-picked by the fallback.

### 3. Fail-fast error

When no account for P is usable, return 429 with `Retry-After`. The message is readable in Claude Code and Codex:

> All Claude accounts are limited. Earliest frees at 3:40 PM (claude-a, 5-hour limit).

- The time comes from the earliest relevant reset across the snapshots and cooldowns, in the host's local time.
- Keep the existing JSON fields (`code`, `reset_time`, `reset_seconds`) for compatibility. Change only the message text and the source of the time.

### 4. Routing-state endpoint (new)

A management endpoint returns, per account:
- provider
- status: `draining` (rank 1 for its provider; flagged `urgent` when the urgency override is active), `standby`, or `limited` with a reason and free-at time
- rank
- pinned-session count (from the affinity cache)
- the quota snapshot and its age
- bonus resets available (count, what they clear, expiry)

### 5. Dashboard (fork of the panel)

It extends the existing quota page to match mockup A. The approved mockup is `docs/mockups/dashboard-options.html`; option **A** was chosen, and option B was rejected. Each row has the account cell (file/email, status pill, session count), then one column per limit window with a bar, % and reset time, then Refresh. Extra windows (Opus/Sonnet/per-model) add columns, Theo-style.

- **Per account row:**
  - masked file/email, with a "Show emails" toggle
  - plan
  - status pill (Draining · #1 / Standby · #2 / Limited until 3:40 PM)
  - "N sessions"
  - one bar per limit window with % and "in 1 day · 10/21 13:00"
  - Refresh quota
- **Provider summary cards** at the top: totals and the next reset, as in Theo's.
- **Bonus resets:**
  - a badge on the account row ("1 reset available · clears 7-day · expires 10/25")
  - a banner at the top of the page whenever any account has one
  - the existing manual redeem button stays, behind a confirm click, and is never automatic
  - if the reset-credit endpoint fails, the badge shows "unknown" rather than hiding
- **Serving:** the server serves the fork's locally built `management.html`. Set `disable-auto-update-panel`, or point `panel-github-repository` at the fork, so upstream never overwrites it. The builder picks one.

### 6. Hosting and access

- **Runs on the user's other machine** (OS not yet known). It's a single binary plus `config.yaml`, set to start automatically on boot or login. Docs cover Windows (Task Scheduler or a service), macOS (launchd) and Linux (systemd), so whichever OS it is works.
- **Listens on the host's Tailscale address only (my call), never 0.0.0.0 on a public interface.** Upstream has a single listener (`server.host`), so the host machine's own clients also use the Tailscale address. The management API needs `allow-remote: true` to work over Tailscale. That's acceptable because the bind restricts reach to the tailnet.
- **Keys:** client keys in `access.api-keys`, one per machine so logs show which machine sent what. Dashboard password via `management.secret-key`.
- **Account logins:** `-claude-login` (×2) and `-codex-login` on the host.

### 7. Client setup docs

Copy-paste config for both machines:
- **Claude Code:** `ANTHROPIC_BASE_URL` set to the proxy's tailnet URL, plus the auth token.
- **Codex CLI:** `config.toml` with a provider whose base URL is the proxy.
- **How to verify it's working:** the dashboard shows the session pinned to an account.

## Repository layout

`C:\dev\ai-cmd-center` is one git repo:
- `server/`: the fork of CLIProxyAPI.
- `panel/`: the fork of the management center.
- `docs/`: this spec and the setup guides.

Upstream history should stay pullable. The builder chooses the mechanism (git subtree, or two separate fork repos referenced from here). Keep the changes in new files where possible (new selector, poller, endpoint, and panel components) and keep edits to upstream files small.

## Testing expectations

- Unit tests for the selector:
  - ranking by weekly reset
  - 5-hour spillover and return
  - model-specific weekly windows
  - unknown and stale data
  - all-limited producing the right message and time
- Affinity tests:
  - a bound session stays while active
  - it re-routes after its provider's idle TTL (Claude 1h, Codex 10m)
  - it moves when its account becomes limited
- Urgency tests:
  - a session bound to a later-reset account moves on its next request when the rank-1 account resets within 24h with headroom
  - it does not move when there is no urgency
  - the override ends when the urgent account is limited or has reset
- Poller tests against recorded Claude and Codex usage payloads, including bonus-reset blocks and fetch failures.
- A manual end-to-end check from both machines through Tailscale with real accounts.

## Risks

- **Terms of service:** routing personal subscriptions through a proxy is a gray area under Anthropic's and OpenAI's consumer terms, and it puts the accounts at some risk. The user is aware.
- **Undocumented endpoints:** usage and bonus-reset endpoints are internal and can change. The poller must degrade to "unknown" rather than break routing.
- **Two instances:** if the fallback of two instances is ever used, affinity and drain order only hold within each instance.

## Left to the builder

- Exact config key names for the new options.
- Poll interval tuning.
- Panel styling details beyond mockup A.
- Fork/upstream sync mechanism.
- Whether the panel reads the server snapshot or keeps its own upstream calls (the numbers must agree).
