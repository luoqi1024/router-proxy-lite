#!/bin/sh
set -eu
ROOT=$(readlink -f "$0")
ROOT=${ROOT%/scripts/manage.sh}
case "$ROOT" in /data/routerlite|/overlay/routerlite) :;; *) echo 'Unsupported install root'; exit 1;; esac
case "${1:-}" in
    info)
        echo 'Management address (LAN only):'
        ip -4 addr show br-lan | awk '/inet / {split($2,a,"/"); print "http://" a[1] ":8787"; exit}'
        echo 'Management key (keep private):'
        cat "$ROOT/state/admin.key"; echo;;
    stop) /etc/init.d/routerlite stop;;
    start) /etc/init.d/routerlite start;;
    disable) /etc/init.d/routerlite disable; /etc/init.d/routerlite stop;;
    *) echo 'Usage: manage.sh info|stop|start|disable'; exit 2;;
esac
