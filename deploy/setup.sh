#!/usr/bin/env bash
# Virgil VPS bootstrap (Debian / Ubuntu).
#
# Run as root from a clone of the virgil repo:
#   sudo VIRGIL_HOSTNAME=virgil.example.com bash deploy/setup.sh
#
# VIRGIL_HOSTNAME is optional — if set, it's substituted into the
# Caddyfile so you don't have to hand-edit it.
#
# Idempotent — re-runnable. After it finishes, follow the printed steps
# (edit config, drop in private key, point GitHub App at this VPS).

set -euo pipefail

if [[ $EUID -ne 0 ]]; then
	echo "Run as root: sudo bash deploy/setup.sh" >&2
	exit 1
fi

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HOSTNAME_OPT="${VIRGIL_HOSTNAME:-}"
INSTALL_CADDY="${VIRGIL_INSTALL_CADDY:-0}"

apt-get update
apt-get install -y --no-install-recommends \
	ca-certificates curl gnupg apt-transport-https git

if [[ "$INSTALL_CADDY" == "1" ]] && ! command -v caddy >/dev/null; then
	apt-get install -y debian-keyring debian-archive-keyring
	curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
		| gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
	curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
		> /etc/apt/sources.list.d/caddy-stable.list
	apt-get update
	apt-get install -y caddy
fi

# Go 1.25+ — Debian's apt golang is too old, so install from the
# official tarball. Re-runs upgrade in place.
GO_REQ="$(awk '/^go /{print $2; exit}' "$REPO_ROOT/go.mod")"
GO_LATEST="$(curl -fsSL 'https://go.dev/VERSION?m=text' | head -n1)"
if ! /usr/local/go/bin/go version 2>/dev/null | grep -q "$GO_LATEST"; then
	echo ">> installing $GO_LATEST (go.mod requires >= $GO_REQ)"
	curl -fsSL "https://go.dev/dl/${GO_LATEST}.linux-amd64.tar.gz" -o "/tmp/${GO_LATEST}.tar.gz"
	rm -rf /usr/local/go
	tar -C /usr/local -xzf "/tmp/${GO_LATEST}.tar.gz"
	rm "/tmp/${GO_LATEST}.tar.gz"
fi
export PATH="/usr/local/go/bin:$PATH"

cd "$REPO_ROOT"
go build -trimpath -ldflags '-s -w' -o /usr/local/bin/virgil-server ./cmd/server

if ! id virgil &>/dev/null; then
	useradd --system --home-dir /var/lib/virgil --shell /usr/sbin/nologin virgil
fi
install -d -o virgil -g virgil -m 750 /var/lib/virgil
install -d -o root   -g virgil -m 750 /etc/virgil

if [[ ! -f /etc/virgil/config.yaml ]]; then
	install -o root -g virgil -m 640 "$REPO_ROOT/deploy/config.yaml.example" /etc/virgil/config.yaml
	echo ">> wrote /etc/virgil/config.yaml — edit before starting"
fi
if [[ ! -f /etc/virgil/env ]]; then
	install -o root -g virgil -m 640 /dev/null /etc/virgil/env
	echo "ANTHROPIC_API_KEY=PUT_YOUR_KEY_HERE" >> /etc/virgil/env
	echo ">> wrote /etc/virgil/env — edit before starting"
fi

install -o root -g root -m 644 "$REPO_ROOT/deploy/virgil-server.service" /etc/systemd/system/virgil-server.service
systemctl daemon-reload
systemctl enable virgil-server.service

if [[ "$INSTALL_CADDY" == "1" && ! -f /etc/caddy/Caddyfile.virgil-installed ]]; then
	install -o root -g root -m 644 "$REPO_ROOT/deploy/Caddyfile.example" /etc/caddy/Caddyfile
	if [[ -n "$HOSTNAME_OPT" ]]; then
		sed -i "s/virgil\.example\.com/$HOSTNAME_OPT/g" /etc/caddy/Caddyfile
		echo ">> wrote /etc/caddy/Caddyfile, hostname set to $HOSTNAME_OPT"
	else
		echo ">> wrote /etc/caddy/Caddyfile — edit hostname before reloading"
	fi
	touch /etc/caddy/Caddyfile.virgil-installed
	systemctl enable caddy
fi

cat <<'EOF'

Bootstrap complete. Finish the install:

  1. Copy the GitHub App private key onto the box, then:
       install -o virgil -g virgil -m 600 /path/to/key.pem /etc/virgil/private-key.pem

  2. Edit /etc/virgil/config.yaml — set the real webhook_secret.
  3. Edit /etc/virgil/env       — paste the real ANTHROPIC_API_KEY.
  4. Edit /etc/caddy/Caddyfile  — replace virgil.example.com with your hostname.
  5. systemctl restart caddy virgil-server
  6. In the GitHub App settings, change the webhook URL to:
       https://YOUR_HOSTNAME/webhook
  7. On your local machine:
       systemctl --user disable --now virgil-server smee-client

Then push a commit and watch:  journalctl -u virgil-server -f

EOF
