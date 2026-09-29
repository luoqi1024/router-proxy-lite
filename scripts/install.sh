#!/bin/sh
# First install only; preserve incomplete files for diagnosis.
set -eu
ROOT=/data/routerlite
AUTOSTART=0
BUNDLE=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd -P)
. "$BUNDLE/scripts/setup-common.sh"
[ "${1:-}" = '--experimental' ] || rpl_die '用法：install.sh --experimental [--root /data/routerlite|/overlay/routerlite] [--autostart]'
shift
while [ "$#" -gt 0 ]; do
    case "$1" in
        --root) [ "$#" -ge 2 ] || rpl_die '--root 缺少路径'; ROOT=$2; shift 2;;
        --autostart) AUTOSTART=1; shift;;
        *) rpl_die '未知安装参数';;
    esac
done
case "$ROOT" in /data/routerlite|/overlay/routerlite) :;; *) echo 'Unsupported install root'; exit 2;; esac
PARENT=${ROOT%/*}
LOCK=/tmp/routerlite-install.lock
mkdir "$LOCK" 2>/dev/null || rpl_die '另一个安装正在运行或留下安装锁，请先核实。'
SERVICE_CREATED=0
FILES_CREATED=0
SUCCESS=0
cleanup_install() {
    code=$?
    trap - EXIT
    if [ "$SUCCESS" = 0 ] && [ "$SERVICE_CREATED" = 1 ]; then
        if [ "$(readlink -f /etc/init.d/routerlite)" = "$ROOT/scripts/routerlite.init" ]; then
            /etc/init.d/routerlite disable || true
            /etc/init.d/routerlite stop || true
            rm -f /etc/init.d/routerlite
        fi
    fi
    if [ "$SUCCESS" = 0 ] && [ "$FILES_CREATED" = 1 ]; then
        echo "安装未完成；文件保留在 $ROOT，请检查后恢复，不会覆盖重装。" >&2
    fi
    rmdir "$LOCK" 2>/dev/null || true
    exit "$code"
}
trap cleanup_install EXIT
trap 'exit 130' HUP INT TERM
rpl_probe
cd "$BUNDLE"
sh scripts/verify.sh "$BUNDLE"
NEED_KIB=$(du -sk "$BUNDLE" | awk '{print $1+2048}')
if [ "$BUNDLE" = "$ROOT.stage" ]; then NEED_KIB=2048; fi
rpl_storage_ok "$PARENT" || rpl_die '持久分区空间不足或不受支持，需要额外保留 2 MiB。'
chmod 755 bin/routerlite bin/sing-box
[ -x bin/routerlite ] && [ -x bin/sing-box ] || { echo 'Missing executables'; exit 1; }
GOGC=50 GOMEMLIMIT=16MiB GOMAXPROCS=2 bin/routerlite --version
GOGC=50 GOMEMLIMIT=16MiB GOMAXPROCS=2 bin/sing-box version
GOGC=50 GOMEMLIMIT=16MiB GOMAXPROCS=2 bin/routerlite --mode router --check --lan br-lan --core "$BUNDLE/bin/sing-box" --assets "$BUNDLE/assets"
sh scripts/network.sh check
if pidof routerlite >/dev/null 2>&1; then echo 'Another manager is running'; exit 1; fi
# Installing does not enable interception; it only starts the management page.
if [ "$BUNDLE" = "$ROOT.stage" ]; then
    mv "$BUNDLE" "$ROOT"
    FILES_CREATED=1
else
    mkdir -m 700 "$ROOT"
    FILES_CREATED=1
    cp -R bin assets scripts licenses SHA256SUMS LICENSE THIRD_PARTY.md "$ROOT/"
fi
cd "$ROOT"
chmod 700 "$ROOT"
mkdir -m 700 "$ROOT/state"
chmod 755 "$ROOT"/bin/* "$ROOT"/scripts/*.sh "$ROOT/scripts/routerlite.init"
ln -s "$ROOT/scripts/routerlite.init" /etc/init.d/routerlite
SERVICE_CREATED=1
/etc/init.d/routerlite start
manager_alive() {
    for process_id in $(pidof routerlite 2>/dev/null); do
        [ "$(readlink -f "/proc/$process_id/exe")" = "$ROOT/bin/routerlite" ] && return 0
    done
    return 1
}
tries=0
while :; do
    status=$(curl --noproxy '*' -s --connect-timeout 1 --max-time 2 -o /dev/null -w '%{http_code}' "http://$LAN_IP:8787/api/state" 2>/dev/null) || status=000
    if [ "$status" = 401 ] && [ -s "$ROOT/state/admin.key" ] && manager_alive; then break; fi
    tries=$((tries+1))
    [ "$tries" -lt 30 ] || rpl_die '管理页面未就绪，安装失败。'
    sleep 1
done
if [ "$AUTOSTART" = 1 ]; then
    /etc/init.d/routerlite enable
    /etc/init.d/routerlite enabled || rpl_die '无法确认开机启动。'
fi
SUCCESS=1
echo '安装完成。请打开网页设置管理密码，再导入订阅、选择节点和策略。代理尚未开启。'
sh "$ROOT/scripts/manage.sh" info
if [ "$AUTOSTART" = 1 ]; then echo '管理服务已设为开机启动。';
else echo '如需开机启动：/etc/init.d/routerlite enable'; fi
