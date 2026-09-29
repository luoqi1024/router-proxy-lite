#!/bin/sh
# Keep the service registered with procd while DHCP/LAN initialization completes.
set -eu
ROOT=$(readlink -f "$0")
ROOT=${ROOT%/scripts/run.sh}
case "$ROOT" in /data/routerlite|/overlay/routerlite) :;; *) exit 1;; esac
count=0
while :; do
    LAN_IP=$(ip -4 addr show br-lan 2>/dev/null | awk '/inet / {split($2,a,"/"); print a[1]; exit}')
    WAN=$(ip -4 route show default | awk '{for(i=1;i<=NF;i++) if($i=="dev"){print $(i+1);exit}}')
    [ -z "$LAN_IP" ] || [ -z "$WAN" ] || break
    count=$((count+1))
    [ "$count" -lt 60 ] || exit 1
    sleep 1
done
exec "$ROOT/bin/routerlite" --mode router --experimental-router --listen "$LAN_IP:8787" --data-dir "$ROOT/state" --core "$ROOT/bin/sing-box" --assets "$ROOT/assets" --scripts "$ROOT/scripts" --lan br-lan --wan "$WAN"
