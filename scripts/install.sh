#!/bin/sh
# Development bundle installer. Run from an unpacked, verified release directory.
set -eu
ROOT=/data/routerlite
BUNDLE=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
[ "${1:-}" = '--experimental' ] || { echo 'Hardware validation pending. Read docs/HARDWARE-TEST.md first. To opt in: sh scripts/install.sh --experimental'; exit 2; }
[ "$(id -u)" = 0 ] || { echo 'root required'; exit 1; }
[ ! -e "$ROOT" ] || { echo 'Existing installation found; refusing to overwrite it'; exit 1; }
[ ! -e /etc/init.d/routerlite ] || { echo 'Service name already in use'; exit 1; }
[ -f /etc/rc.common ] && [ -f /lib/functions/procd.sh ] || { echo 'This preview installer needs an existing procd service manager'; exit 1; }
command -v sha256sum >/dev/null || { echo 'sha256sum required'; exit 1; }
cd "$BUNDLE"
sha256sum -c SHA256SUMS
chmod 755 bin/routerlite bin/sing-box
[ -x bin/routerlite ] && [ -x bin/sing-box ] || { echo 'Missing executables'; exit 1; }
bin/routerlite --version
bin/sing-box version
bin/routerlite --mode router --check --lan br-lan --core "$BUNDLE/bin/sing-box" --assets "$BUNDLE/assets"
sh scripts/network.sh check
need=$(du -sk bin assets scripts | awk '{s+=$1} END {print s+2048}')
avail=$(df -Pk /data | awk 'END {print $4}')
[ "$avail" -ge "$need" ] || { echo 'Insufficient persistent storage (2 MiB reserve required)'; exit 1; }
mem=$(awk '/MemAvailable:/ {print $2}' /proc/meminfo)
[ "${mem:-0}" -ge 65536 ] || { echo 'At least 64 MiB available RAM required for this preview'; exit 1; }
# Installing does not enable interception; it only starts the management page.
mkdir -m 700 "$ROOT"
trap 'echo "Installation incomplete; inspect /data/routerlite before retrying" >&2' EXIT
cp -R bin assets scripts "$ROOT/"
mkdir -m 700 "$ROOT/state"
chmod 755 "$ROOT"/bin/* "$ROOT"/scripts/*.sh "$ROOT/scripts/routerlite.init"
ln -s "$ROOT/scripts/routerlite.init" /etc/init.d/routerlite
/etc/init.d/routerlite enable
/etc/init.d/routerlite start
trap - EXIT
echo 'Installed. Find the LAN management address and initial key with:'
echo 'sh /data/routerlite/scripts/manage.sh info'
