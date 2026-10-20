# AI Command Center: setup

One instance runs on the **host** (your other machine). Both machines reach it over Tailscale. Claude Code and Codex CLI on each machine point at it instead of logging in directly.

Throughout this guide:
- `<tailscale-ip>` is the host's Tailscale IPv4 address (from `tailscale ip -4` on the host, e.g. `100.101.102.103`).
- `<install-dir>` is the folder you run it from on the host, e.g. `C:\ai-cmd-center`, `~/ai-cmd-center` or `/opt/ai-cmd-center`.
- The binary is `ai-cmd-center` (`ai-cmd-center.exe` on Windows).

## 1. Prerequisites

On the host (the build can also run on another machine with the same OS and CPU type):

- **Go**: the version in `server/go.mod` or newer. <https://go.dev/dl/>
- **bun**: <https://bun.sh>. Used to build the dashboard.
- **Git**: to clone this repo.
- **Tailscale** on the host *and* on both client machines, all signed in to the same tailnet. <https://tailscale.com/download>

The build scripts expect `go` and `bun` on `PATH`. Check with `go version` and `bun --version`.

## 2. Build

From the repo root:

```powershell
# Windows (PowerShell)
powershell -ExecutionPolicy Bypass -File scripts\build.ps1
```

```bash
# macOS / Linux / Git Bash
./scripts/build.sh
```

This produces:

```
dist/
  ai-cmd-center(.exe)        server
  static/management.html     dashboard
  config.example.yaml        config template
```

Copy the whole `dist/` folder to `<install-dir>` on the host. The server looks for `static/management.html` in the folder that holds `config.yaml`, so keep the three together.

## 3. Configure

In `<install-dir>`, copy `config.example.yaml` to `config.yaml` and edit it.

### Which IP to bind

Set `server.host` to the host's Tailscale address:

```bash
tailscale ip -4
```

```yaml
server:
  host: "100.101.102.103"   # your value
  port: 8317
```

- Don't use `""` or `0.0.0.0`. That listens on every interface, including your LAN and any public one.
- The server has one listener, so Claude Code and Codex on the host itself also use `http://<tailscale-ip>:8317`, not `127.0.0.1`.
- The Tailscale IP of a device stays fixed unless you remove the device from the tailnet.
- If a firewall prompt appears on first start (Windows), allow it. Only the Tailscale interface is bound.

### Generate keys

You need three random values: the dashboard password (`management.secret-key`) and one client key per machine (`access.api-keys`).

```powershell
# PowerShell: run once per key
$b = New-Object byte[] 32; [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($b); -join ($b | ForEach-Object { $_.ToString('x2') })
```

```bash
# macOS / Linux / Git Bash: run once per key
openssl rand -hex 32
```

Put them in `config.yaml`:

```yaml
management:
  allow-remote: true                 # needed for the dashboard over Tailscale
  secret-key: "<dashboard password>"
  disable-auto-update-panel: true    # keep our dashboard; don't fetch upstream's

access:
  api-keys:
    - "<machine-1 key>"
    - "<machine-2 key>"
```

Replace every `CHANGE-ME`. The server does not reject these placeholders: left in, `machine-1-CHANGE-ME` would work as a client key and `CHANGE-ME` as the dashboard password.

Save the plaintext dashboard password in a password manager. On first start the server replaces it in `config.yaml` with a bcrypt hash and rewrites the file, so the folder must be writable by the user that runs the server.

The routing settings in the template are already the intended defaults (`strategy: reset-aware`, session affinity on, Claude 1h and Codex 10m idle release, 24h urgency window, 5m poll). See section 9 to change them.

## 4. Log in to the accounts

On the host, from `<install-dir>`. Each login opens a browser for OAuth and saves a credential file into `oauth.auth-dir` (default in the template: `~/.ai-cmd-center/auths`).

```bash
./ai-cmd-center -config config.yaml -claude-login    # Claude account 1
./ai-cmd-center -config config.yaml -claude-login    # Claude account 2
./ai-cmd-center -config config.yaml -codex-login     # ChatGPT/Codex account
```

(On Windows: `.\ai-cmd-center.exe -config config.yaml -claude-login`, and so on.)

- For the second Claude login, make sure the browser signs in to the *other* account. Use a private window, or sign out of claude.ai first.
- **Headless host:** add `-no-browser`. The command prints the login URL instead of opening a browser; open it on any machine. The OAuth callback goes to `localhost` on the host (Claude port 54545, Codex port 1455), so when the browser is on another machine, forward that port first, e.g. `ssh -L 54545:localhost:54545 user@host` (verify: not tested here). For Codex, `-codex-device-login` uses a device-code flow with no callback.
- You can also add accounts later from the dashboard.

## 5. Run at startup

Run it once by hand first to check the config:

```bash
./ai-cmd-center -config config.yaml
```

It should log `API server started successfully on: <tailscale-ip>:8317`. Stop it with Ctrl+C, then set up one of these. All three restart the server if it exits, which also covers a start before Tailscale has its IP.

### Windows (Task Scheduler, at logon)

```powershell
powershell -ExecutionPolicy Bypass -File deploy\windows\install-task.ps1 -InstallDir C:\ai-cmd-center
Start-ScheduledTask -TaskName 'AI Command Center'
```

The task runs as you, 30 seconds after you log on. Remove it with `Unregister-ScheduledTask -TaskName 'AI Command Center' -Confirm:$false`.

### macOS (launchd)

1. Edit `deploy/macos/com.aicmdcenter.proxy.plist` and replace `/Users/CHANGE-ME` with your home folder. It expects `<install-dir>` = `~/ai-cmd-center`.
2. Install and start it:

```bash
cp deploy/macos/com.aicmdcenter.proxy.plist ~/Library/LaunchAgents/
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.aicmdcenter.proxy.plist
```

Logs go to `~/ai-cmd-center/proxy.log`. Stop it with `launchctl bootout gui/$(id -u)/com.aicmdcenter.proxy`.

### Linux (systemd)

1. Edit `deploy/linux/ai-cmd-center.service`: set `User=` to the account you logged in with in step 4 (its `~` is where the auth files are). It expects `<install-dir>` = `/opt/ai-cmd-center`, owned by that user.
2. Install and start it:

```bash
sudo cp deploy/linux/ai-cmd-center.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now ai-cmd-center
journalctl -u ai-cmd-center -f
```

## 6. Point Claude Code at it

On each machine, use that machine's own key from `access.api-keys`.

**PowerShell** (current session):

```powershell
$env:ANTHROPIC_BASE_URL = "http://<tailscale-ip>:8317"
$env:ANTHROPIC_AUTH_TOKEN = "<machine key>"
claude
```

To make it permanent for your user: `setx ANTHROPIC_BASE_URL "http://<tailscale-ip>:8317"` and `setx ANTHROPIC_AUTH_TOKEN "<machine key>"`, then open a new terminal.

**bash / zsh** (add to `~/.bashrc` or `~/.zshrc` to keep it):

```bash
export ANTHROPIC_BASE_URL="http://<tailscale-ip>:8317"
export ANTHROPIC_AUTH_TOKEN="<machine key>"
claude
```

**settings.json** (`~/.claude/settings.json`, applies to every Claude Code session for your user):

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://<tailscale-ip>:8317",
    "ANTHROPIC_AUTH_TOKEN": "<machine key>"
  }
}
```

The proxy accepts the key as `Authorization: Bearer` (what `ANTHROPIC_AUTH_TOKEN` sends) or `x-api-key`. If Claude Code warns about a conflict with an existing claude.ai login, run `/logout` in Claude Code so it uses the token (verify).

## 7. Point Codex at it

Add a provider to `~/.codex/config.toml` (Windows: `%USERPROFILE%\.codex\config.toml`) and make it the default:

```toml
model_provider = "ai-cmd-center"

[model_providers.ai-cmd-center]
name = "AI Command Center"
base_url = "http://<tailscale-ip>:8317/v1"
env_key = "AI_CMD_CENTER_KEY"
wire_api = "responses"
```

Then set the key in the environment Codex runs in:

```powershell
$env:AI_CMD_CENTER_KEY = "<machine key>"     # or: setx AI_CMD_CENTER_KEY "<machine key>"
```

```bash
export AI_CMD_CENTER_KEY="<machine key>"
```

The proxy serves the Responses API at `/v1/responses` and lists models at `/v1/models`. Upstream's in-repo docs don't spell out the Codex `config.toml` wiring (they point to <https://help.router-for.me/>), so the block above follows Codex's documented custom-provider format: **verify** it against your Codex version. Upstream also serves `/backend-api/codex/responses` "for chatgpt_base_url compatibility", an alternative if the provider block misbehaves (verify).

## 8. Verify

1. Open `http://<tailscale-ip>:8317/management.html` from either machine and log in with the dashboard password. The Quota page shows each account with its limit bars and a status pill (`Draining · #1`, `Standby · #2`, or `Limited until …`).
2. On one machine, start Claude Code and send a message.
3. Refresh the dashboard. The Claude account marked **Draining** now shows **1 session**.
4. Do the same with Codex and check its account.

From a terminal, the same state is available as JSON:

```bash
curl -s -H "Authorization: Bearer <dashboard password>" http://<tailscale-ip>:8317/v0/management/routing-state
```

It returns `"strategy":"reset-aware"` and one entry per account under `accounts`.

## 9. Troubleshooting

**"All Claude accounts are limited. Earliest frees at 3:40 PM (claude-a, 5-hour limit)."**
Every account for that provider is at a limit. The proxy fails fast and never queues requests. Wait until the time shown (host-local time). If a bonus reset is available, the dashboard shows a banner and a badge on the account; redeeming it is always a manual click.

**"Quota unknown" on an account**
The server hasn't got usage data for that account yet. It polls every `poll-interval` (5m), and the usage and bonus-reset endpoints are undocumented, so a fetch can fail. Routing keeps working: an account with unknown quota is treated as usable and ordered after accounts with known data. Click **Refresh** on the row, check the host log for poll errors, or send one request through that account. A persistent "unknown" after a login usually means the account's credential needs a fresh `-claude-login` / `-codex-login`.

**Requests return 401 "Missing API key" or "Invalid API key"**
The client isn't sending a key from `access.api-keys`. Check `ANTHROPIC_AUTH_TOKEN` (Claude Code) or the `env_key` variable (Codex) in the shell that started the client.

**Can't connect at all**
Check that `tailscale status` shows the host online, that `server.host` in `config.yaml` still matches `tailscale ip -4`, and that the server is running (the startup log or `journalctl`).

**Changing the TTLs and urgency window**
Edit `config.yaml` on the host:

```yaml
routing:
  session-affinity-ttl-by-provider:
    claude: 1h      # idle time before a Claude conversation is re-routed
    codex: 10m      # same for Codex
  reset-aware:
    urgency-window: 24h   # drain the rank-1 account first when its weekly reset is this close
    poll-interval: 5m     # how often usage is fetched
```

Durations use Go syntax (`30m`, `1h`, `90s`). The server watches `config.yaml` and reloads it. If a change doesn't seem to apply, restart the server (verify which of these settings take effect without a restart).

## 10. Terms-of-service risk

Routing personal Claude and ChatGPT subscriptions through a proxy, shared across machines, is a gray area under Anthropic's and OpenAI's consumer terms, and it puts the accounts at some risk (up to suspension). The usage and bonus-reset endpoints it reads are internal and undocumented, and can change without notice. You run this aware of that risk. Keep it tailnet-only and for your own use.
