# Installing from a package

## Debian / Ubuntu

```bash
sudo dpkg -i rookery_<version>_linux_amd64.deb
```

## Fedora / RHEL

```bash
sudo rpm -i rookery_<version>_linux_amd64.rpm
```

## Running it so it survives a reboot

The packaged unit is a **systemd user unit**, so it needs no root and keeps all
data under your own home directory:

```bash
mkdir -p ~/.config/systemd/user
cp /usr/share/rookery/rookery.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now rookery

# Without lingering, a user unit stops when your last session ends and does not
# start at boot. This is the step people miss.
sudo loginctl enable-linger "$USER"
```

Check it:

```bash
systemctl --user status rookery
curl -sS http://127.0.0.1:8080/healthz
```

## Reading `/healthz`

`/healthz` reports the build, the sandbox status and which optional host tools
are present. Two warnings are worth acting on:

- **`python3` missing** — the agent-tool AST guardrail is *inactive*. Generated
  tool scripts run without being statically checked first. Install python3.
- **`sandbox.supported: false`** — Landlock is unavailable, so coder
  subprocesses run unconfined. Expected on macOS and Windows; on Linux it means
  the kernel lacks Landlock support.

`rg`, `pdftotext` and `tesseract` are optional; without them knowledge-base
search, PDF extraction and image OCR degrade but keep working (OCR is simply
unavailable).

## Bootstrapping

```bash
rookery owner bootstrap -u <username> -p <password>
```

Then open `http://<host>:8080/`.

## OAuth and your instance URL

An OAuth provider never connects to this server — it redirects the user's
browser. So the server does not need to be reachable from the internet. What
gets validated is the **redirect URI string**, when you register it.

| How you reach the app | Redirect URI | Outcome |
|---|---|---|
| `http://localhost:8080` | `http://localhost:8080/…` | Google, GitHub, Notion work. Slack-class providers need HTTPS. |
| LAN server, plain HTTP on an IP | `http://192.168.1.50:8080/…` | Google rejects raw IP addresses. GitHub works. |
| LAN server, internal CA, `.lan` name | `https://agents.example.lan/…` | HTTPS satisfies Slack-class providers; Google rejects the reserved `.lan` suffix. |
| **Real domain, DNS-01 certificate, resolved on your LAN** | `https://agents.example.com/…` | **All providers work, with no inbound exposure.** |

The last row is the recommended setup for a self-hosted install: register a real
domain, obtain a certificate via a DNS-01 challenge (no inbound port needed),
and point the name at the server's private IP in your own DNS. Then set
`ROOKERY_PUBLIC_URL` — or the instance URL in Settings — to that address.

Settings → Owner → **Instance URL** shows what is currently in use and has a
**Test this URL** button that verifies the address actually reaches this server.
Each provider's connect panel shows the exact redirect URI to register, and
warns before you start if your current URL will not work with that provider.
