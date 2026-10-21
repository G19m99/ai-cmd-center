# Join a machine to AI Command Center

Use this guide to send another computer's Claude Code (and Codex) traffic through an AI Command Center host that's already running. Setting up the host itself is covered in [setup.md](setup.md).

You need:
- The host's Tailscale IPv4 address (`tailscale ip -4` on the host). Below it's `<tailscale-ip>`.
- Access to the host's `config.yaml`, in the folder the server runs from (`<install-dir>`, e.g. `C:\ai-cmd-center`).
- The Tailscale account the host is signed in to.
- Claude Code (and Codex, if you use it) installed on the new machine.

## 1. Give the machine its own key

Every machine gets its own client key. The logs then show which machine sent a request, and you can cut one machine off without touching the others. The template ships with two keys: one for the host and one for a second machine. If the new machine is the second one, use that key and skip to step 2. Otherwise add a key:

1. Generate one, on any machine:

   ```powershell
   # PowerShell
   $b = New-Object byte[] 32; [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($b); -join ($b | ForEach-Object { $_.ToString('x2') })
   ```

   ```bash
   # macOS / Linux / Git Bash
   openssl rand -hex 32
   ```

2. On the host, add it to `access.api-keys` in `<install-dir>/config.yaml`:

   ```yaml
   access:
     api-keys:
       - "<host key>"
       - "<second machine key>"
       - "<new machine key>"
   ```

3. Save the file. The server watches `config.yaml` and starts accepting the new key without a restart.

Store the key in your password manager. Anyone holding it, on the tailnet, can use your accounts through the proxy.

## 2. Join the tailnet

The server only listens on the host's Tailscale address, so the new machine has to be on the same tailnet.

1. Install Tailscale:
   - Windows: `winget install --id Tailscale.Tailscale`
   - macOS: from the App Store or <https://tailscale.com/download>
   - Linux: `curl -fsSL https://tailscale.com/install.sh | sh`
2. Sign in with **the same account the host uses**, e.g. the same Google login. On Windows and macOS, use the Tailscale icon in the tray or menu bar. On Linux, run `sudo tailscale up`. Signing in with a different account puts the machine in a different tailnet, where it can't see the host.
3. Check that the machine reaches the host and that the key works:

   ```bash
   tailscale status
   curl -s -o /dev/null -w "%{http_code}\n" -H "x-api-key: <new machine key>" http://<tailscale-ip>:8317/v1/models
   ```

   `tailscale status` should list the host as online. The `curl` check answers:
   - `200`: connected, and the key is accepted.
   - `401`: the key isn't in `access.api-keys` yet. Check step 1.
   - A connection error: the machine isn't on the tailnet, or the server isn't running.

   On Windows PowerShell, use `curl.exe` and `-o NUL`.

## 3. Point Claude Code at the proxy

Add this to `~/.claude/settings.json` (Windows: `%USERPROFILE%\.claude\settings.json`). If the file already has an `"env"` block, add the two lines to it:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://<tailscale-ip>:8317",
    "ANTHROPIC_CUSTOM_HEADERS": "x-api-key: <new machine key>"
  }
}
```

Then restart Claude Code. Sessions that were already open keep their old connection until you restart them.

- **Stay logged in to claude.ai in Claude Code. Don't run `/logout`.** Model requests go through the proxy, and your own login keeps claude.ai connectors (Google Drive, Docs and so on) working.
- **No claude.ai login on this machine?** Use `"ANTHROPIC_AUTH_TOKEN": "<new machine key>"` instead of the header line. Claude Code then needs no login, but claude.ai connectors stay off. Each session warns: "claude.ai connectors are disabled because ANTHROPIC_API_KEY or another auth source is set".

## 4. Point Codex at the proxy (optional)

Add a provider to `~/.codex/config.toml` (Windows: `%USERPROFILE%\.codex\config.toml`) and make it the default:

```toml
model_provider = "ai-cmd-center"

[model_providers.ai-cmd-center]
name = "AI Command Center"
base_url = "http://<tailscale-ip>:8317/v1"
env_key = "AI_CMD_CENTER_KEY"
wire_api = "responses"
```

Then set the key where Codex runs: `setx AI_CMD_CENTER_KEY "<new machine key>"` on Windows (open a new terminal afterwards), or `export AI_CMD_CENTER_KEY="<new machine key>"` in `~/.bashrc` or `~/.zshrc`. This block follows Codex's documented custom-provider format. Verify it against your Codex version; [setup.md](setup.md) section 7 has an alternative if it misbehaves.

## 5. Check that it works

1. Start Claude Code on the new machine and send a message.
2. Open `http://<tailscale-ip>:8317/management.html` and log in with the dashboard password. Under **Account usage**, the account marked **In use** now has one more open conversation, and **Requests handled** has gone up.

`/usage` inside Claude Code describes your own claude.ai login, not the accounts the proxy routes to. Check the dashboard for those.

## Removing a machine

On the host, delete the machine's key from `access.api-keys` and save. From then on the proxy answers that machine's requests with `401`. To take the machine off the tailnet too, remove it in the Tailscale admin console.

## When something's wrong

| Symptom | Fix |
|---|---|
| Claude Code can't connect at all | `tailscale status` on the new machine: is the host online? Then run the `curl` check from step 2. |
| `401 Missing API key` / `Invalid API key` | The key in `ANTHROPIC_CUSTOM_HEADERS` doesn't match an entry in `access.api-keys`. |
| "claude.ai connectors are disabled because ANTHROPIC_API_KEY or another auth source is set" | `ANTHROPIC_AUTH_TOKEN` or `ANTHROPIC_API_KEY` is set. Remove it and use `ANTHROPIC_CUSTOM_HEADERS`. |
| "All Claude accounts are limited" | Every account is at a limit. The message says when the first one frees up. The dashboard shows each account's limits and any bonus reset. |

More in [setup.md](setup.md), section 9.
