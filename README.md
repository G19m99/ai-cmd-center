# AI Command Center

A self-hosted proxy for your Claude and ChatGPT/Codex subscriptions. Claude Code and Codex CLI on your machines point at it, and it decides which account serves each request:

- **Reset-aware routing.** New work goes to the account whose weekly limit resets soonest and still has room, so allowance that would expire unused gets spent first.
- **Session affinity.** A conversation stays on its account while active, so the prompt cache stays warm. It's re-routed after it's been idle longer than the provider's cache lifetime (Claude 1h, Codex 10m), or sooner if another account must be drained before an imminent reset.
- **Fail fast.** When every account is limited, requests get a clear 429 saying when the earliest account frees up.
- **Quota dashboard.** Per-account limits, reset times, routing status, pinned sessions and available bonus resets. Bonus resets are never redeemed automatically.

It runs on one machine and is reached over Tailscale only.

## Layout

| Path       | What                                                                 |
|------------|----------------------------------------------------------------------|
| `server/`  | Fork of [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) (Go), git subtree |
| `panel/`   | Fork of the [management center](https://github.com/router-for-me/Cli-Proxy-API-Management-Center) (React), git subtree |
| `deploy/`  | Config template and run-at-startup files for Windows, macOS and Linux |
| `scripts/` | `build.ps1` / `build.sh`: build everything into `dist/`              |
| `docs/`    | Setup guide, upstream sync notes, design spec                        |

## Quick start

```bash
./scripts/build.sh            # or: powershell -ExecutionPolicy Bypass -File scripts\build.ps1
```

Then follow [docs/setup.md](docs/setup.md): configure, log in to the accounts, run at startup, and point Claude Code and Codex at it.

- Pulling upstream fixes: [docs/UPSTREAM.md](docs/UPSTREAM.md)
- Design: [docs/superpowers/specs/2026-10-20-ai-cmd-center-design.md](docs/superpowers/specs/2026-10-20-ai-cmd-center-design.md)

## Terms of service

Routing personal subscriptions through a proxy is a gray area under Anthropic's and OpenAI's consumer terms and puts the accounts at some risk. See the note at the end of [docs/setup.md](docs/setup.md).

## License

`server/` and `panel/` are MIT-licensed by their upstream authors; see their `LICENSE` files.
