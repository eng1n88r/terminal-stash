<p align="center">
  <img src="https://raw.githubusercontent.com/eng1n88r/terminal-stash/main/docs/banner.svg" alt="Terminal Stash — self-hosted LAN clipboard & file drop" width="880">
</p>

# Terminal Stash

A minimalist, self-hosted **shared clipboard & file drop** for your home network.
Run it on your home server, open it in a browser on any machine, and copy text or
files between them. New items appear **live** on every open browser.

- Terminal / TUI aesthetic with 8 phosphor themes
- Paste text, click to copy it back anywhere
- Drag-and-drop or paste files (and images) to upload; one-click download
- Live sync across machines via Server-Sent Events — no refresh
- Persists across restarts (SQLite + a volume), with auto-pruning
- Single shared password
- One tiny static Go binary (~9 MB image), `linux/amd64` + `linux/arm64`

> **LAN-only.** No TLS and no per-user accounts by design. Do **not** expose this
> directly to the internet — put it behind a reverse proxy / VPN / Cloudflare
> Tunnel that terminates HTTPS and forwards `X-Forwarded-Proto` and
> `X-Forwarded-For`.

## Quick start

```bash
docker run -d --name stash \
  -p 127.0.0.1:7827:7827 \
  -e APP_PASSWORD=something-only-you-know \
  -v stash-data:/data \
  exbarboss/terminal-stash:latest

# open http://localhost:7827 and log in with the password
```

`-p 127.0.0.1:7827:7827` keeps it reachable only from the host (typically behind
a reverse proxy or tunnel). For direct LAN access use `-p 7827:7827`.

## Tags

| Tag | Meaning |
|---|---|
| `latest` | Most recent release |
| `X.Y.Z` (e.g. `0.1.0`) | Exact release |
| `X.Y`, `X` | Latest patch / minor within that line |

## Configuration

| Variable | Default | Description |
|---|---|---|
| `APP_PASSWORD` | *(required)* | Shared password. The server refuses to start if unset. |
| `APP_USER` | `user` | Name shown in the UI's terminal prompt (`<name>@stash`). |
| `PORT` | `7827` | Listen port. |
| `DATA_DIR` | `/data` | Where the SQLite DB and uploaded files are stored. |
| `MAX_ITEMS` | `200` | Keep at most this many items; oldest are pruned. `0` = unlimited. |
| `MAX_AGE_DAYS` | `30` | Delete items older than this. `0` = never expire. |
| `MAX_UPLOAD_MB` | `100` | Reject uploads larger than this. |

## Notes

- Restarting the container invalidates login sessions (by design) — just log in again.
- Failed logins are rate-limited per client IP and logged.
- Data lives in `/data` — mount a volume or it's gone with the container.

---

Source, issues, and docker-compose setup: **[github.com/eng1n88r/terminal-stash](https://github.com/eng1n88r/terminal-stash)** · MIT licensed
