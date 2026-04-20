#!/usr/bin/env bash
# install-guest.sh — install rbash-mcp inside an incus guest.
#
# Copies the daemon binary into /usr/local/bin, installs the systemd
# unit, creates the persisted-output directory, and enables the service.
# Idempotent — re-running replaces the binary and restarts the service.
#
# Usage (from the host, inside the guest — depending on how you transfer):
#   # Option A — push binary + script into guest and run inside:
#   incus file push bin/rbash-mcp <guest>/tmp/rbash-mcp
#   incus file push deploy/install-guest.sh <guest>/tmp/install-guest.sh
#   incus file push deploy/systemd/rbash-mcp.service <guest>/tmp/rbash-mcp.service
#   incus exec <guest> -- bash /tmp/install-guest.sh /tmp/rbash-mcp /tmp/rbash-mcp.service
#
#   # Option B — bind-mount the project, run from inside:
#   incus exec <guest> -- bash /srv/rbash-mcp/deploy/install-guest.sh \
#       /srv/rbash-mcp/bin/rbash-mcp \
#       /srv/rbash-mcp/deploy/systemd/rbash-mcp.service

set -euo pipefail

BIN_SRC="${1:-}"
UNIT_SRC="${2:-}"

if [[ -z "$BIN_SRC" || -z "$UNIT_SRC" ]]; then
    echo "usage: $0 <path-to-rbash-mcp-binary> <path-to-rbash-mcp.service>" >&2
    exit 2
fi
[[ -f "$BIN_SRC" ]]  || { echo "binary not found: $BIN_SRC" >&2; exit 1; }
[[ -f "$UNIT_SRC" ]] || { echo "unit not found: $UNIT_SRC" >&2; exit 1; }

install -D -m 0755 "$BIN_SRC"  /usr/local/bin/rbash-mcp
install -D -m 0644 "$UNIT_SRC" /etc/systemd/system/rbash-mcp.service
install -d -m 0755 -o root -g root /var/lib/rbash/outputs

systemctl daemon-reload
systemctl enable rbash-mcp.service
systemctl restart rbash-mcp.service
systemctl --no-pager --lines=5 status rbash-mcp.service || true
