#!/bin/sh
# Shared first-install checks. Also embedded in the generated download entrypoint.
rpl_die() { printf '%s\n' "RouterLite: $*" >&2; exit 1; }
rpl_probe() {
    RPL_MEMORY_PROFILE=${RPL_MEMORY_PROFILE:-standard}
    case "$RPL_MEMORY_PROFILE" in standard|compact) :;; *) rpl_die '未知内存模式。';; esac
    [ "$(id -u)" = 0 ] || rpl_die '请使用 root 通过 SSH 安装。'
    [ "$(uname -s)" = Linux ] || rpl_die '当前仅支持 Linux 路由器。'
    case "$(uname -m)" in armv7l|armv7) :;; *) rpl_die '当前安装包仅支持 ARMv7；其他架构尚未提供经过验证的包。';; esac
    for tool in ip iptables awk df du readlink pidof curl; do
        command -v "$tool" >/dev/null 2>&1 || rpl_die "缺少系统命令：$tool"
    done
    [ -f /etc/rc.common ] && [ -f /lib/functions/procd.sh ] || rpl_die '当前安装器需要固件已有 procd。'
    [ -c /dev/net/tun ] || rpl_die '固件没有 TUN 设备，不能安装此版本。'
    [ "$(cat /proc/sys/net/ipv4/ip_forward)" = 1 ] || rpl_die '系统没有启用 IPv4 转发。'
    LAN_IP=$(ip -4 addr show br-lan 2>/dev/null | awk '/inet / {split($2,a,"/"); print a[1]; exit}')
    [ -n "$LAN_IP" ] || rpl_die '未找到 br-lan 的 IPv4 地址，当前固件尚未适配。'
    ip -4 route show default | grep -q ' dev ' || rpl_die '未找到默认上联网口，请先让路由器正常联网。'
    if ip -6 addr show dev br-lan scope global 2>/dev/null | grep -q inet6; then
        rpl_die '检测到 LAN IPv6；当前预览版仅支持 IPv4，未更改原厂设置。'
    fi
    for existing in /data/routerlite /overlay/routerlite /etc/init.d/routerlite; do
        [ ! -e "$existing" ] && [ ! -L "$existing" ] || rpl_die "已有安装或残留：$existing；请维护现有安装，不会覆盖。"
    done
    for process in routerlite sing-box clash mihomo; do
        if pidof "$process" >/dev/null 2>&1; then rpl_die "已有 $process 运行，请先备份并停止旧代理。"; fi
    done
    for chain in AXPROXY_MARK SHELLCRASH clash RPL_MARK; do
        if iptables -t mangle -nL "$chain" >/dev/null 2>&1; then rpl_die "发现代理规则 $chain，请先通过旧程序停止并清理。"; fi
    done
    # Known legacy services must not return on the next boot.
    for entry in /etc/rc.d/S*; do
        [ -e "$entry" ] || [ -L "$entry" ] || continue
        target=$(readlink -f "$entry") || rpl_die '无法核实已有启动项。'
        case "$target" in *axproxy*|*shellcrash*|*ShellCrash*|*mihomo*|*clash*) rpl_die '检测到旧代理自启动，请先关闭旧自启动；安装器不会替你修改。';; esac
    done
    mem=$(awk '/MemAvailable:/ {print $2}' /proc/meminfo)
    case "$mem" in ''|*[!0-9]*) rpl_die '无法读取可用内存。';; esac
    minimum=65536
    if [ "$RPL_MEMORY_PROFILE" = compact ]; then
        minimum=32768
        total=$(awk '/MemTotal:/ {print $2}' /proc/meminfo)
        case "$total" in ''|*[!0-9]*) rpl_die '无法读取总内存。';; esac
        [ "$total" -ge 131072 ] || rpl_die '紧凑模式至少需要 128 MiB 总内存。'
    fi
    [ "$mem" -ge "$minimum" ] || rpl_die "可用内存 ${mem} KiB，当前模式安装至少需要 ${minimum} KiB；不会继续安装。"
    if [ "$RPL_MEMORY_PROFILE" = compact ]; then
        [ -n "${BUNDLE:-}" ] || rpl_die '紧凑模式仅支持可信的离线 native-small 安装包。'
        case "$("$BUNDLE/bin/sing-box" version 2>/dev/null)" in *1.14.2-routerlite-native-small*) :;; *) rpl_die '紧凑模式需要经过校验的 native-small 内核包。';; esac
    fi
}
rpl_storage_ok() {
    parent=$1
    [ -d "$parent" ] && [ ! -L "$parent" ] && [ -w "$parent" ] || return 1
    # Only existing, explicitly mounted persistent filesystems. Never choose tmpfs.
    awk -v p="$parent" '$2==p && $3 ~ /^(ubifs|jffs2|ext[234]|f2fs)$/ {ok=1} END {exit !ok}' /proc/mounts || return 1
    free_kib=$(df -Pk "$parent" | awk 'END {print $4}')
    case "$free_kib" in ''|*[!0-9]*) return 1;; esac
    [ "$free_kib" -ge "$NEED_KIB" ]
}
rpl_select_root() {
    request=$1
    case "$request" in auto) candidates='/data /overlay';; /data/routerlite) candidates=/data;; /overlay/routerlite) candidates=/overlay;; *) rpl_die '安装位置只支持 auto、/data/routerlite、/overlay/routerlite。';; esac
    ROOT=
    for parent in $candidates; do
        if [ -e "$parent/routerlite.stage" ] || [ -L "$parent/routerlite.stage" ]; then
            [ "${BUNDLE:-}" = "$parent/routerlite.stage" ] && [ ! -L "$parent/routerlite.stage" ] || rpl_die "发现未完成的暂存目录 $parent/routerlite.stage；请先检查其内容，不会覆盖或删除。"
        fi
        if rpl_storage_ok "$parent"; then ROOT=$parent/routerlite; break; fi
    done
    [ -n "$ROOT" ] || rpl_die "没有合适的持久分区：需要 ${NEED_KIB} KiB（包含 2 MiB 余量），不会扩分区或刷机。"
    printf '安装位置：%s；分区剩余：%s KiB；可用内存：%s KiB\n' "$ROOT" "$free_kib" "$mem"
}
