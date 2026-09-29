#!/bin/sh
# Development installer. Staging beside the target avoids a second flash copy.
set -eu
ROOT=/data/routerlite
BUNDLE=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd -P)
[ "${1:-}" = '--experimental' ] || { echo 'Usage: install.sh --experimental [--root /data/routerlite|/overlay/routerlite]'; exit 2; }
shift
if [ "${1:-}" = --root ] && [ "$#" = 2 ]; then ROOT=$2; shift 2; fi
[ "$#" = 0 ] || exit 2
case "$ROOT" in /data/routerlite|/overlay/routerlite) :;; *) echo 'Unsupported install root'; exit 2;; esac
PARENT=${ROOT%/*}
[ "$(id -u)" = 0 ] || { echo 'root required'; exit 1; }
[ ! -e "$ROOT" ] && [ ! -L "$ROOT" ] || { echo 'Existing installation found; refusing to overwrite it'; exit 1; }
[ ! -e /etc/init.d/routerlite ] && [ ! -L /etc/init.d/routerlite ] || { echo 'Service name already in use'; exit 1; }
[ -f /etc/rc.common ] && [ -f /lib/functions/procd.sh ] || { echo 'This preview installer needs an existing procd service manager'; exit 1; }
[ -d "$PARENT" ] && [ ! -L "$PARENT" ] || exit 1
cd "$BUNDLE"
sh scripts/verify.sh "$BUNDLE"
need=$(du -sk bin assets scripts | awk '{s+=$1} END {print s+2048}')
if [ "$BUNDLE" = "$ROOT.stage" ]; then need=2048; fi
avail=$(df -Pk "$PARENT" | awk 'END {print $4}')
[ "$avail" -ge "$need" ] || { echo 'Insufficient persistent storage (2 MiB reserve required)'; exit 1; }
mem=$(awk '/MemAvailable:/ {print $2}' /proc/meminfo)
[ "${mem:-0}" -ge 65536 ] || { echo 'At least 64 MiB available RAM required; stop the previous proxy before retrying'; exit 1; }
chmod 755 bin/routerlite bin/sing-box
[ -x bin/routerlite ] && [ -x bin/sing-box ] || { echo 'Missing executables'; exit 1; }
bin/routerlite --version
bin/sing-box version
bin/routerlite --mode router --check --lan br-lan --core "$BUNDLE/bin/sing-box" --assets "$BUNDLE/assets"
sh scripts/network.sh check
if pidof routerlite >/dev/null 2>&1; then echo 'Another manager is running'; exit 1; fi
# Installing does not enable interception; it only starts the management page.
if [ "$BUNDLE" = "$ROOT.stage" ]; then
    mv "$BUNDLE" "$ROOT"
else
    mkdir -m 700 "$ROOT"
    cp -R bin assets scripts "$ROOT/"
fi
cd "$ROOT"
chmod 700 "$ROOT"
mkdir -m 700 "$ROOT/state"
chmod 755 "$ROOT"/bin/* "$ROOT"/scripts/*.sh "$ROOT/scripts/routerlite.init"
ln -s "$ROOT/scripts/routerlite.init" /etc/init.d/routerlite
/etc/init.d/routerlite start
echo 'Management service started. Proxy interception and boot autostart are NOT enabled.'
echo "Read the local key with: sh $ROOT/scripts/manage.sh info"
echo 'After testing, explicitly enable boot startup with: /etc/init.d/routerlite enable'
