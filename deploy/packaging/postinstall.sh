#!/bin/sh
# Register and start the hive service after package installation / upgrade.
set -e
if command -v systemctl >/dev/null 2>&1; then
  /usr/bin/hive install >/dev/null 2>&1 || true   # idempotent: already installed on upgrade
  systemctl daemon-reload
  systemctl enable --now camhive-hive >/dev/null 2>&1 || /usr/bin/hive start || true
fi
