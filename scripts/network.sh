#!/bin/sh
# IPv4 only. Never flush the router's built-in chains or the main routing table.
set -eu
LAN=${RPL_LAN:-br-lan}
DATA=${RPL_DATA:-/data/routerlite/state}
TABLE=3180
PREF=10820
MASK=0x40000000
MARK=$MASK/$MASK
OWNER=$DATA/network.owner
LOCK=/tmp/routerlite-network.lock
case "$LAN" in ''|*[!a-zA-Z0-9_.:-]*) echo 'Invalid LAN interface' >&2; exit 2;; esac
[ "$(id -u)" = 0 ] || { echo 'root required' >&2; exit 1; }
mkdir "$LOCK" 2>/dev/null || { echo 'Network operation already running' >&2; exit 1; }
trap 'rmdir "$LOCK" 2>/dev/null || true' EXIT

preflight() {
    command -v ip >/dev/null
    command -v iptables >/dev/null
    ip link show "$LAN" >/dev/null
    [ -c /dev/net/tun ]
    [ "$(cat /proc/sys/net/ipv4/ip_forward)" = 1 ]
    # Do not stack transparent proxies; their route marks can conflict.
    for chain in AXPROXY_MARK SHELLCRASH clash; do
        if iptables -t mangle -nL "$chain" >/dev/null 2>&1; then
            echo 'An existing transparent proxy must be stopped first' >&2; return 1
        fi
    done
    if ip -6 addr show dev "$LAN" scope global 2>/dev/null | grep -q inet6; then
        echo 'LAN IPv6 is not supported in this preview' >&2; return 1
    fi
    if [ ! -f "$OWNER" ]; then
        [ -z "$(ip -4 route show table "$TABLE" 2>/dev/null || true)" ] || { echo 'Route table occupied' >&2; return 1; }
        if ip -4 rule show | grep -q "^$PREF:"; then echo 'Rule priority occupied' >&2; return 1; fi
        for spec in 'mangle RPL_MARK' 'nat RPL_DNS'; do
            set -- $spec
            if iptables -t "$1" -nL "$2" >/dev/null 2>&1; then echo 'Unowned chain exists' >&2; return 1; fi
        done
    fi
}

stop_network() {
    [ -f "$OWNER" ] || return 0
    # Recover the original interface even if the user's LAN setting changed.
    old_lan=$(cat "$OWNER")
    case "$old_lan" in ''|*[!a-zA-Z0-9_.:-]*) return 1;; esac
    failed=0
    if ip -4 rule show | grep -q "^$PREF:"; then
        ip -4 rule del pref "$PREF" fwmark "$MARK" iif "$old_lan" lookup "$TABLE" || failed=1
    fi
    for spec in "mangle PREROUTING $old_lan RPL_MARK" "nat PREROUTING $old_lan RPL_DNS"; do
        set -- $spec
        if iptables -t "$1" -C "$2" -i "$3" -j "$4" 2>/dev/null; then
            iptables -t "$1" -D "$2" -i "$3" -j "$4" || failed=1
        fi
    done
    if iptables -C INPUT -i rpltun -j ACCEPT 2>/dev/null; then iptables -D INPUT -i rpltun -j ACCEPT || failed=1; fi
    if iptables -C FORWARD -i "$old_lan" -o rpltun -j ACCEPT 2>/dev/null; then iptables -D FORWARD -i "$old_lan" -o rpltun -j ACCEPT || failed=1; fi
    if iptables -C FORWARD -i rpltun -o "$old_lan" -j ACCEPT 2>/dev/null; then iptables -D FORWARD -i rpltun -o "$old_lan" -j ACCEPT || failed=1; fi
    for spec in 'mangle RPL_MARK' 'nat RPL_DNS'; do
        set -- $spec
        if iptables -t "$1" -nL "$2" >/dev/null 2>&1; then
            iptables -t "$1" -F "$2" || failed=1
            iptables -t "$1" -X "$2" || failed=1
        fi
    done
    ip -4 route flush table "$TABLE" || failed=1
    [ "$failed" = 0 ] || { echo 'Cleanup incomplete; ownership retained for recovery' >&2; return 1; }
    rm -f "$OWNER"
}

start_network() {
    preflight
    ip link show rpltun >/dev/null
    stop_network
    mkdir -p "$DATA"
    chmod 700 "$DATA"
    umask 077
    printf '%s\n' "$LAN" > "$OWNER"
    trap 'stop_network; rmdir "$LOCK" 2>/dev/null || true' EXIT
    ip -4 route add default dev rpltun table "$TABLE"
    for subnet in 0.0.0.0/8 10.0.0.0/8 100.64.0.0/10 127.0.0.0/8 169.254.0.0/16 172.16.0.0/12 192.168.0.0/16 224.0.0.0/4 240.0.0.0/4; do
        ip -4 route add throw "$subnet" table "$TABLE"
    done
    iptables -I INPUT 1 -i rpltun -j ACCEPT
    iptables -I FORWARD 1 -i "$LAN" -o rpltun -j ACCEPT
    iptables -I FORWARD 1 -i rpltun -o "$LAN" -j ACCEPT
    iptables -t nat -N RPL_DNS
    iptables -t nat -A RPL_DNS -p udp --dport 53 -j REDIRECT --to-ports 1053
    iptables -t nat -A RPL_DNS -p tcp --dport 53 -j REDIRECT --to-ports 1053
    iptables -t mangle -N RPL_MARK
    iptables -t mangle -A RPL_MARK -j CONNMARK --restore-mark --nfmask "$MASK" --ctmask "$MASK"
    iptables -t mangle -A RPL_MARK -m conntrack --ctstate NEW -j MARK --set-xmark "$MARK"
    iptables -t mangle -A RPL_MARK -j CONNMARK --save-mark --nfmask "$MASK" --ctmask "$MASK"
    ip -4 rule add pref "$PREF" fwmark "$MARK" iif "$LAN" lookup "$TABLE"
    iptables -t nat -I PREROUTING 1 -i "$LAN" -j RPL_DNS
    iptables -t mangle -I PREROUTING 1 -i "$LAN" -j RPL_MARK
    trap 'rmdir "$LOCK" 2>/dev/null || true' EXIT
}

case "${1:-}" in
    check) preflight;;
    status)
        [ -f "$OWNER" ]
        ip link show rpltun >/dev/null
        ip -4 rule show | grep -q "^$PREF:"
        ip -4 route show table "$TABLE" | grep -q 'default dev rpltun'
        iptables -t mangle -C PREROUTING -i "$LAN" -j RPL_MARK
        iptables -t nat -C PREROUTING -i "$LAN" -j RPL_DNS
        if ip -6 addr show dev "$LAN" scope global 2>/dev/null | grep -q inet6; then exit 1; fi;;
    start) start_network;;
    stop) stop_network;;
    *) echo 'Usage: network.sh check|start|stop|status' >&2; exit 2;;
esac
