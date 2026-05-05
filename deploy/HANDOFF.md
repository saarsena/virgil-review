# Virgil Handoff — Local Workstation → Hetzner VPS

Written 2026-05-04. The previous workstation is being wiped. Virgil was running locally
there as systemd user services with smee.io forwarding webhooks to localhost:8081. This
document is the migration plan for the Hetzner box. Read it end-to-end before doing
anything — context first, recipe second.

## User context (so the next Claude doesn't have to ask)

- Solo developer working on Godot-adjacent tooling. Virgil is personal infra, not a
  product. No team, no SLA, no shared environments — interruptions are tolerable.
- Communication style: terse, no preamble. Acknowledge mistakes flatly; don't pad
  with excuses or restatement. Hallucinated facts asserted with confidence are the
  fastest way to lose trust here.
- The user is not a sysadmin. Step-by-step commands beat "you can also..."
  alternatives. When there's a fork in the road, pick one and explain why.

## Existing infrastructure on the Hetzner box (do NOT disturb)

- **nginx** is running and **hardened** as the public front for a searxng instance.
  This is the priority service on the box. Any change to nginx is a regression risk
  for searxng. **Do not edit nginx.conf or any sites-enabled file.** Add no new
  server blocks. Reload no nginx.
- **Cloudflare** sits in front of the domain (orange cloud, free plan). The user owns
  the domain and the Cloudflare zone.
- Distro is Debian.

## Architectural decision

Expose virgil via **Cloudflare Tunnel (`cloudflared`)**, not nginx, not Caddy. The
tunnel:
- bypasses nginx entirely → searxng stays untouched,
- needs no firewall change → no port to open on Hetzner,
- requires no public port on the box,
- managed CNAME at `virgil.<domain>` is created by `cloudflared tunnel route dns`.

Webhook traffic flow:
`GitHub → Cloudflare edge → cloudflared on VPS → http://localhost:8081 (virgil-server)`

State (`state.db`) is starting fresh on the VPS. The old SQLite was abandoned with
the workstation. Worst case: GitHub redelivers a webhook for an old SHA we already
reviewed and we re-review it. Acceptable.

## Pre-wipe checklist (must be done on the laptop before it's wiped)

Save these to a password manager / secure note — they are not in the repo:

1. **GitHub App private key** at `~/.config/virgil/virgil-review.2026-04-28.private-key.pem`.
   Copy the file contents.
2. **Webhook secret** at `config.yaml` line 17 (`github.webhook_secret`). If lost,
   rotate it in the GitHub App settings — that requires updating both the App and
   the local config simultaneously.
3. **Anthropic API key** in `~/.config/virgil/env` (`ANTHROPIC_API_KEY=sk-ant-...`).
4. **GitHub App ID** is `3537633` (also in `config.yaml`, also recoverable from the
   App settings page).

Don't bother with `state.db` — fresh start.

## Deployment recipe (run on the Hetzner box)

### 1. Install cloudflared and create the tunnel

```bash
curl -L https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64.deb -o /tmp/cloudflared.deb
sudo dpkg -i /tmp/cloudflared.deb

sudo cloudflared tunnel login            # browser flow — pick the zone for the user's domain
sudo cloudflared tunnel create virgil    # NOTE THE UUID it prints — needed below
```

The credentials JSON lands at `/root/.cloudflared/<UUID>.json`.

### 2. Configure the tunnel

```bash
sudo mkdir -p /etc/cloudflared
sudo tee /etc/cloudflared/config.yml >/dev/null <<EOF
tunnel: virgil
credentials-file: /root/.cloudflared/<UUID>.json

ingress:
  - hostname: virgil.<your-domain>
    service: http://localhost:8081
  - service: http_status:404
EOF

sudo cloudflared tunnel route dns virgil virgil.<your-domain>
sudo cloudflared service install
systemctl status cloudflared
```

After this step, `https://virgil.<your-domain>/` reaches localhost:8081 — but
nothing's listening there yet, so it'll 502.

### 3. Install the virgil binary

```bash
sudo apt-get update
sudo apt-get install -y git
sudo git clone https://github.com/saarsena/virgil-review.git /opt/virgil-repo
cd /opt/virgil-repo
sudo bash deploy/setup.sh
```

`setup.sh` is idempotent. It:
- installs Go 1.25+ from go.dev (Debian's apt golang is too old),
- builds `/usr/local/bin/virgil-server` from the repo,
- creates a `virgil` system user,
- creates `/var/lib/virgil/` (state.db lives here, owned by virgil),
- creates `/etc/virgil/{config.yaml,env}` from templates if missing (will not clobber edits),
- installs `/etc/systemd/system/virgil-server.service` and enables it.

It does **not** install Caddy by default. To force Caddy install (don't, on this box):
`VIRGIL_INSTALL_CADDY=1 sudo bash deploy/setup.sh`.

### 4. Drop in the secrets

```bash
# scp'd or pasted the .pem to /tmp/key.pem first
sudo install -o virgil -g virgil -m 600 /tmp/key.pem /etc/virgil/private-key.pem
sudo rm /tmp/key.pem
```

Edit `/etc/virgil/config.yaml`:
- Set `github.webhook_secret` to the saved value.
- Paths (`private_key_path`, `storage.path`) are pre-filled correctly.
- `github.app_id` is pre-filled to `3537633`.

Edit `/etc/virgil/env` — replace `PUT_YOUR_KEY_HERE` with the real `ANTHROPIC_API_KEY`.

### 5. Start virgil-server

```bash
sudo systemctl restart virgil-server
sudo journalctl -u virgil-server -f
```

Expect log lines:
- `storage opened path=/var/lib/virgil/state.db`
- `starting virgil-server addr=:8081 app_id=3537633`

If it crashes on `loading config: anthropic.api_key required`, the env file isn't
populated — `cat /etc/virgil/env` and check.

### 6. Repoint the GitHub App webhook

GitHub App settings → General → Webhook URL: change from the `smee.io` URL to
`https://virgil.<your-domain>/webhook`.

Then GitHub App settings → Advanced → Recent Deliveries → Redeliver the latest push.
Watch `journalctl -u virgil-server -f` — should see
`check run created` → `inputs fetched` → `check run completed`. Status code in
GitHub Recent Deliveries should be 200.

### 7. Drop the smee dependency

The smee subscription URL (`https://smee.io/Yp8ocBcCPIU5SRVb`) becomes orphaned once
the App's webhook URL is changed. Nothing to clean up — it just stops getting
deliveries.

## Architectural notes — don't change without thinking

These are deliberate. The previous Claude (a few sessions ago) verified each in code
and tests:

- **Empty-diff short-circuit** (`internal/webhook/handler.go`, after `difffilter.Filter`):
  if filtering removes all paths and `strings.TrimSpace(diff) == ""`, complete the
  Check Run as `neutral` and call `MarkReviewed`. Without this, GitHub retries the
  webhook ~8 times over 3 days because the reviewer 400's on empty content blocks.
- **Initial-branch skip** (`handler.go:141`): `before == zeroSHA` returns
  "skipped: branch created" 200. Intentional v1 scope. Will eventually want to
  diff against the empty tree to review the first commit, but not now.
- **MarkReviewed runs AFTER CompleteCheckRun**: storage failure on the mark step
  produces a possible duplicate next time, never a silent skip. Lesser evil — keep
  this ordering.
- **Brain suggestions are NOT auto-applied**: they go into the `brain_suggestions`
  table with `status='pending'`. The user runs `virgil brain accept <id>` from the
  CLI to append to `.virgil/brain.md`. This is by design — the brain is a curated
  human-edited file.
- **smee→tunnel asymmetric-PartOf bug** that bit us once on local: irrelevant on
  the Hetzner box because there's no smee anymore.

## Files in this repo (you're cloning fresh)

- `cmd/server/` — webhook server entrypoint
- `cmd/virgil/` — CLI (`virgil brain list/show/accept/reject`, `virgil review`)
- `internal/webhook/` — push event pipeline
- `pkg/reviewer/` — Anthropic-backed reviewer with `submit_review` tool
- `pkg/brain/` — fetches `.virgil/brain.md` from the repo at the pushed SHA
- `pkg/storage/` — SQLite (idempotency, usage, brain suggestions)
- `pkg/difffilter/` — strips lockfiles / vendored / generated paths from diffs
- `pkg/ghclient/` — GitHub App auth, check runs, compare commits
- `pkg/config/` — YAML + env-var fallback
- `deploy/` — this directory, plus setup.sh / unit / Caddyfile.example

## What's NOT in the repo (per .gitignore)

- `config.yaml` (real config — only `config.example.yaml` is committed)
- `*.pem` private keys
- `.env`
- `bin/` and `state.db*`

## If something breaks

- Server crashes immediately: `journalctl -u virgil-server -n 50` — look for "loading
  config" errors. 90% of deploy failures are missing/wrong env vars or paths.
- Tunnel up but virgil 502: virgil-server isn't running or is on a different port.
  `ss -tlnp | grep 8081` to confirm.
- 200 deliveries but no check run on the commit: the GitHub App's installation
  permissions might not include the repo. App settings → Install App → confirm the
  repo is checked.
- Reviewer returns errors: the journal log will include a `stop_reason`. If it's
  `max_tokens`, raise `anthropic.max_tokens` in config.yaml. If it's a 401, the API
  key in `/etc/virgil/env` is wrong.
