#!/bin/sh
# After installing or upgrading the package: run the service on
# hive-updater (ADR-0018) and (re)start it so the new bundle is seeded.
# An existing service keeps its CAMHIVE_DATA_DIR: losing it would start
# the hive on an empty data directory, unclaimed.
set -e
HIVE=${HIVE:-/usr/bin/hive}
UNIT=camhive-hive
command -v systemctl >/dev/null 2>&1 || exit 0
if systemctl cat "$UNIT" >/dev/null 2>&1; then
  if ! systemctl cat "$UNIT" | grep -q 'hive-updater'; then
    data=$(systemctl show -p Environment --value "$UNIT" | tr ' ' '\n' | sed -n 's/^CAMHIVE_DATA_DIR=//p')
    systemctl stop "$UNIT" >/dev/null 2>&1 || true
    "$HIVE" uninstall >/dev/null 2>&1 || true
    if [ -n "$data" ]; then
      CAMHIVE_DATA_DIR="$data" "$HIVE" install
    else
      "$HIVE" install
    fi
  fi
else
  "$HIVE" install >/dev/null 2>&1 || true
fi
systemctl daemon-reload
systemctl enable "$UNIT" >/dev/null 2>&1 || true
systemctl restart "$UNIT" >/dev/null 2>&1 || "$HIVE" start || true
