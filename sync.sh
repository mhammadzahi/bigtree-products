#!/usr/bin/env bash
# sync.sh — scheduled WooCommerce -> MariaDB product sync (cron wrapper around
# fill_prodcut.go).
#
#   ./sync.sh                 incremental: only products changed since last sync
#   ./sync.sh incremental     same, explicit
#   ./sync.sh reconcile       incremental + delete products gone from WooCommerce
#   ./sync.sh full            re-import everything (slow; seeds the sync time)
#
# Suggested crontab (crontab -e, as the user that owns this repo):
#
#   # nightly incremental sync, Mon-Sat 03:00
#   0 3 * * 1-6  /path/to/bigtree-products/sync.sh incremental
#   # weekly reconcile (also does the incremental work), Sunday 03:00
#   0 3 * * 0    /path/to/bigtree-products/sync.sh reconcile
#
# Behaviour:
#   - Only one sync runs at a time (flock). An overlapping run exits quietly.
#   - Each run logs to logs/sync-<date>-<mode>.log. Logs older than
#     LOG_RETENTION_DAYS (default 30) are deleted.
#   - Exit code is non-zero on failure, so cron's MAILTO (if set) reports it.
#   - The importer binary is rebuilt only when fill_prodcut.go changes.
set -euo pipefail

APP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODE="${1:-incremental}"
LOG_DIR="${LOG_DIR:-$APP_DIR/logs}"
LOG_RETENTION_DAYS="${LOG_RETENTION_DAYS:-30}"
TIMEOUT="${SYNC_TIMEOUT:-6h}"          # hard stop for a hung run
LOCK_FILE="${LOCK_FILE:-/tmp/bt-products-sync.lock}"
BIN="$APP_DIR/bin/sync"

# cron runs with a minimal PATH; make the Go toolchain reachable.
export PATH="/usr/local/go/bin:$HOME/go/bin:/usr/local/bin:/usr/bin:/bin:$PATH"

case "$MODE" in
  incremental | reconcile | full) ;;
  *)
    echo "usage: $0 [incremental|reconcile|full]" >&2
    exit 2
    ;;
esac

cd "$APP_DIR" # .env is read from the working directory
mkdir -p "$LOG_DIR" "$APP_DIR/bin"

# Single instance: hold an exclusive lock for the whole run.
exec 9>"$LOCK_FILE"
if ! flock -n 9; then
  echo "$(date -u +%FT%TZ) another sync is running; skipping $MODE" >&2
  exit 0
fi

LOG_FILE="$LOG_DIR/sync-$(date -u +%Y%m%d-%H%M%S)-$MODE.log"
exec >>"$LOG_FILE" 2>&1
echo "==> $(date -u +%FT%TZ) start $MODE"

# Rebuild the importer only when its source is newer than the binary.
if [[ ! -x "$BIN" || fill_prodcut.go -nt "$BIN" || go.mod -nt "$BIN" ]]; then
  echo "==> building $BIN"
  CGO_ENABLED=0 go build -o "$BIN" fill_prodcut.go
fi

status=0
timeout --signal=INT --kill-after=2m "$TIMEOUT" "$BIN" "$MODE" || status=$?

echo "==> $(date -u +%FT%TZ) end $MODE (exit $status)"

find "$LOG_DIR" -name 'sync-*.log' -type f -mtime +"$LOG_RETENTION_DAYS" -delete || true

exit "$status"
