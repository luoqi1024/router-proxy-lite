#!/bin/sh
# Offline entrypoint: run from a trusted complete bundle uploaded to the router.
set -eu
BUNDLE=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd -P)
. "$BUNDLE/scripts/setup-common.sh"
REQUEST=auto
CHECK=0
while [ "$#" -gt 0 ]; do
    case "$1" in
        --check) CHECK=1; shift;;
        --root) [ "$#" -ge 2 ] || rpl_die '--root 缺少路径'; REQUEST=$2; shift 2;;
        *) rpl_die '用法：sh scripts/setup.sh [--check] [--root auto|/data/routerlite|/overlay/routerlite]';;
    esac
done
rpl_probe
sh "$BUNDLE/scripts/verify.sh" "$BUNDLE"
NEED_KIB=$(du -sk "$BUNDLE" | awk '{print $1+2048}')
case "$BUNDLE" in /data/routerlite.stage|/overlay/routerlite.stage)
    target=${BUNDLE%.stage}
    [ "$REQUEST" = auto ] || [ "$REQUEST" = "$target" ] || rpl_die '暂存包的位置与指定目标不一致。'
    REQUEST=$target
    NEED_KIB=2048;;
esac
rpl_select_root "$REQUEST"
[ "$CHECK" = 0 ] || { echo '检查通过，未安装、未启动服务。'; exit 0; }
exec sh "$BUNDLE/scripts/install.sh" --experimental --root "$ROOT" --autostart
