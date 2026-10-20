# Pulling upstream changes

Both forks are git subtrees of upstream `main`, squashed:

| Prefix    | Upstream                                                              |
|-----------|-----------------------------------------------------------------------|
| `server/` | <https://github.com/router-for-me/CLIProxyAPI> (Go)                   |
| `panel/`  | <https://github.com/router-for-me/Cli-Proxy-API-Management-Center> (React) |

## Pull

From the repo root, on a clean working tree (commit or stash first), ideally on a new branch:

```bash
git checkout -b upstream-sync
git subtree pull --prefix server https://github.com/router-for-me/CLIProxyAPI.git main --squash
git subtree pull --prefix panel https://github.com/router-for-me/Cli-Proxy-API-Management-Center.git main --squash
```

Each pull creates a squash commit plus a merge commit. Conflicts can only appear in the upstream files we modified (listed below). Resolve them, then:

```bash
cd server && go build ./... && go test ./sdk/cliproxy/... ./internal/config/... ./internal/api/... ./internal/quotapoll/...
cd ../panel && bun install && bun run test && bun run build
cd .. && ./scripts/build.sh                                     # or scripts\build.ps1
```

Then do the smoke test (start `dist/ai-cmd-center` with a test config, check `/v0/management/routing-state` and `/management.html`) before merging back.

Things to watch in upstream changes:
- `routing.*` config keys and `sdk/cliproxy/auth/selector.go` / `session_cache.go`, which our selector and per-provider TTL hook into.
- The Claude (`api.anthropic.com/api/oauth/usage`) and Codex (`chatgpt.com/backend-api/wham/usage`) usage payloads, which our poller parses.
- The panel's `src/features/quota/` page, which our dashboard extends.

## Upstream files we modified

New files are kept separate where possible. These upstream files carry edits (from the reset-aware routing work, Tasks 2–6):

**server/**
- `internal/api/handlers/management/api_tools.go`
- `internal/api/handlers/management/config_basic.go`
- `internal/api/server_management.go`
- `internal/api/server_management_v8.go`
- `internal/config/config_types.go`
- `sdk/cliproxy/auth/selector.go`
- `sdk/cliproxy/auth/session_cache.go`
- `sdk/cliproxy/builder.go`
- `sdk/cliproxy/service.go`
- `sdk/cliproxy/service_config.go`
- `sdk/cliproxy/service_lifecycle.go`

**panel/**
- `src/features/quota/QuotaPage.tsx`
- `src/features/quota/components/QuotaBody.module.scss`
- `src/features/quota/components/QuotaCard.module.scss`
- `src/features/quota/components/QuotaCard.tsx`
- `src/features/quota/providers/claude/ClaudeQuotaBody.tsx`
- `src/features/quota/providers/codex/CodexQuotaBody.tsx`
- `src/features/quota/types.ts`
- `src/i18n/locales/en.json`, `ko.json`, `ru.json`, `vi.json`, `zh-CN.json`, `zh-TW.json`
- `tsconfig.json`

## Our new files (no upstream conflicts expected)

**server/**
- `internal/api/handlers/management/routing_state.go` (+ test), `config_basic_resetaware_test.go`
- `internal/api/server_routing_state_test.go`
- `internal/quotapoll/` (`fetch.go`, `poller.go`, test)
- `sdk/cliproxy/auth/quota_snapshot.go`, `quota_snapshot_parse.go`, `resetaware_affinity.go`, `resetaware_selector.go`, `session_cache_ttl.go` (+ tests and `testdata/quota/`)
- `sdk/cliproxy/service_config_resetaware.go`, `service_quotapoll.go` (+ tests)

**panel/**
- `src/features/quota/components/QuotaBodyGroup.tsx`
- `src/features/quota/routing/` (routing status pill, provider summary cards, bonus reset badge and banner, hooks, formatting, tests)
- `src/services/api/routingState.ts` (+ test)

Regenerate either list with:

```bash
git diff --name-status <subtree-merge-commit> HEAD -- server panel
```
