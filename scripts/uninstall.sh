#!/bin/sh
set -eu
[ "${1:-}" = '--remove-program-keep-state' ] || { echo 'Usage: uninstall.sh --remove-program-keep-state'; exit 2; }
[ "$(id -u)" = 0 ] || exit 1
[ "$(readlink /etc/init.d/routerlite)" = /data/routerlite/scripts/routerlite.init ] || { echo 'Unrecognized service; refusing'; exit 1; }
/etc/init.d/routerlite disable
/etc/init.d/routerlite stop
RPL_DATA=/data/routerlite/state /bin/sh /data/routerlite/scripts/network.sh stop
rm /etc/init.d/routerlite
# Fixed paths only; state/ intentionally retained for backup or reinstall recovery.
rm -rf /data/routerlite/bin /data/routerlite/assets /data/routerlite/scripts
echo 'Program removed; private state retained in /data/routerlite/state (not a public backup).'
