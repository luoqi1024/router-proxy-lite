#!/bin/sh
set -eu
[ "${1:-}" = '--remove-program-keep-state' ] || { echo 'Usage: uninstall.sh --remove-program-keep-state'; exit 2; }
[ "$(id -u)" = 0 ] || exit 1
ROOT=$(readlink -f "$0")
ROOT=${ROOT%/scripts/uninstall.sh}
case "$ROOT" in /data/routerlite|/overlay/routerlite) :;; *) echo 'Unsupported install root'; exit 1;; esac
[ "$(readlink -f /etc/init.d/routerlite)" = "$ROOT/scripts/routerlite.init" ] || { echo 'Unrecognized service; refusing'; exit 1; }
/etc/init.d/routerlite disable
/etc/init.d/routerlite stop
RPL_DATA="$ROOT/state" /bin/sh "$ROOT/scripts/network.sh" stop
rm /etc/init.d/routerlite
# Fixed paths only; state/ intentionally retained for backup or reinstall recovery.
rm -rf "$ROOT/bin" "$ROOT/assets" "$ROOT/scripts"
echo "Program removed; private state retained in $ROOT/state (not a public backup)."
