#!/bin/sh
set -eu
case "${1:-}" in
    info)
        echo 'Management address (LAN only):'
        ip -4 addr show br-lan | awk '/inet / {split($2,a,"/"); print "http://" a[1] ":8787"; exit}'
        echo 'Management key (keep private):'
        cat /data/routerlite/state/admin.key; echo;;
    stop) /etc/init.d/routerlite stop;;
    start) /etc/init.d/routerlite start;;
    disable) /etc/init.d/routerlite disable; /etc/init.d/routerlite stop;;
    *) echo 'Usage: manage.sh info|stop|start|disable'; exit 2;;
esac
