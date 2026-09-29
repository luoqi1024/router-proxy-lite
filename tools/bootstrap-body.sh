# Appended after trusted release constants and shared setup functions.
REQUEST=auto
CHECK=0
CA_FILE=
while [ "$#" -gt 0 ]; do
    case "$1" in
        --check) CHECK=1; shift;;
        --root) [ "$#" -ge 2 ] || rpl_die '--root 缺少路径'; REQUEST=$2; shift 2;;
        --cacert) [ "$#" -ge 2 ] || rpl_die '--cacert 缺少文件'; CA_FILE=$2; shift 2;;
        *) rpl_die '用法：sh install-routerlite.sh [--check] [--root auto|/data/routerlite|/overlay/routerlite] [--cacert 可信CA文件]';;
    esac
done
rpl_probe
rpl_select_root "$REQUEST"
[ "$CHECK" = 0 ] || { echo '检查通过，未下载或安装。'; exit 0; }
if command -v sha256sum >/dev/null 2>&1; then HASH=sha256sum
elif command -v openssl >/dev/null 2>&1; then HASH=openssl
else rpl_die '缺少 sha256sum 或 openssl，无法校验下载。'; fi
[ -z "$CA_FILE" ] || [ -r "$CA_FILE" ] || rpl_die '无法读取指定的 CA 文件。'
STAGE=$ROOT.stage
umask 077
mkdir -m 700 "$STAGE" || rpl_die '无法创建新的暂存目录。'
release_files() {
    cat <<'ROUTERLITE_RELEASE_FILES'
@@FILES@@
ROUTERLITE_RELEASE_FILES
}
cleanup_download() {
    code=$?
    trap - EXIT
    # Only remove manifest files in the directory created by this invocation.
    case "$STAGE" in /data/routerlite.stage|/overlay/routerlite.stage)
        if [ -d "$STAGE" ] && [ ! -L "$STAGE" ] && [ "$(readlink -f "$STAGE")" = "$STAGE" ]; then
            release_files | while read -r digest size relative asset; do
                target=$STAGE/$relative
                [ "$(readlink -f "$(dirname "$target")")" = "$(dirname "$target")" ] || continue
                rm -f "$target" "$target.part"
            done
            rmdir "$STAGE/bin" "$STAGE/assets" "$STAGE/scripts" "$STAGE/licenses" "$STAGE" 2>/dev/null || true
        fi;;
    esac
    exit "$code"
}
trap cleanup_download EXIT
trap 'exit 130' HUP INT TERM
fetch_file() {
    url=$1; output=$2; size=$3
    set -- --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 180 --max-filesize "$size"
    [ -z "$CA_FILE" ] || set -- "$@" --cacert "$CA_FILE"
    curl "$@" --output "$output" "$url" || rpl_die '下载失败：请检查网络、系统时间和可信 CA。也可在电脑下载完整包后离线安装。不会跳过证书校验。'
}
printf '正在安装 RouterLite %s，架构 ARMv7…\n' "$RELEASE"
while read -r digest size relative asset; do
    [ -n "$digest" ] || continue
    destination=$STAGE/$relative
    mkdir -p "$(dirname "$destination")"
    fetch_file "$BASE_URL/$asset" "$destination.part" "$size"
    actual_size=$(wc -c < "$destination.part" | tr -d ' ')
    [ "$actual_size" = "$size" ] || rpl_die "下载大小不符：$relative"
    if [ "$HASH" = sha256sum ]; then actual=$(sha256sum "$destination.part" | awk '{print $1}')
    else actual=$(openssl dgst -sha256 "$destination.part" | awk '{print $NF}'); fi
    [ "$actual" = "$digest" ] || rpl_die "下载校验失败：$relative"
    mv "$destination.part" "$destination"
done <<'ROUTERLITE_DOWNLOAD_FILES'
@@FILES@@
ROUTERLITE_DOWNLOAD_FILES
sh "$STAGE/scripts/install.sh" --experimental --root "$ROOT" --autostart
