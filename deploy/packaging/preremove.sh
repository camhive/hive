#!/bin/sh
# Stop and unregister the hive service when the package is removed, not
# when it is upgraded (deb: "upgrade"; rpm: $1 >= 1): an upgrade keeps the
# service and its settings, and postinstall restarts it.
# Recorded footage is left in place; it belongs to the owner.
set -e
HIVE=${HIVE:-/usr/bin/hive}
case "${1:-remove}" in
  upgrade | failed-upgrade | 1 | 2) exit 0 ;;
esac
if command -v systemctl >/dev/null 2>&1; then
  "$HIVE" stop >/dev/null 2>&1 || true
  "$HIVE" uninstall >/dev/null 2>&1 || true
  systemctl daemon-reload || true
fi
