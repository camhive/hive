#!/bin/sh
# Stop and unregister the hive service before the package is removed.
# Recorded footage is left in place; it belongs to the owner.
set -e
if command -v systemctl >/dev/null 2>&1; then
  /usr/bin/hive stop >/dev/null 2>&1 || true
  /usr/bin/hive uninstall >/dev/null 2>&1 || true
  systemctl daemon-reload || true
fi
