#!/usr/bin/env bash
# deploy.sh — build + (re)start the Bigtree Products service on Ubuntu.
#
#   ./deploy.sh          pull latest, build, restart the service (day-to-day)
#   ./deploy.sh setup     one-time: install + enable the systemd service
#
# Run as the normal (non-root) user that owns this repo; it uses sudo only for
# the systemctl steps.
set -euo pipefail

APP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE="bt-products"
cd "$APP_DIR"

build() {
  echo "==> building"
  CGO_ENABLED=0 go build -o bin/server ./cmd/server
}

case "${1:-deploy}" in
  setup)
    RUN_USER="${SUDO_USER:-$USER}"
    echo "==> installing systemd unit ($SERVICE) for user '$RUN_USER' at $APP_DIR"
    sudo tee "/etc/systemd/system/${SERVICE}.service" >/dev/null <<EOF
[Unit]
Description=Bigtree Products staff catalog
After=network.target

[Service]
Type=simple
User=${RUN_USER}
WorkingDirectory=${APP_DIR}
ExecStart=${APP_DIR}/bin/server
Restart=on-failure
RestartSec=3
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
EOF
    build
    sudo systemctl daemon-reload
    sudo systemctl enable --now "${SERVICE}"
    sudo systemctl --no-pager --lines=8 status "${SERVICE}" || true
    ;;

  deploy | "")
    echo "==> pulling latest"
    git pull --ff-only || echo "(no git pull — continuing with local files)"
    build
    echo "==> restarting ${SERVICE}"
    sudo systemctl restart "${SERVICE}"
    sleep 1
    sudo systemctl --no-pager --lines=8 status "${SERVICE}" || true
    ;;

  *)
    echo "usage: $0 [deploy|setup]" >&2
    exit 1
    ;;
esac

echo "==> done"
