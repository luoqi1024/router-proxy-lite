#!/bin/sh
# Accept only regular files inside this bundle. Supports firmware without sha256sum.
set -eu
BUNDLE=$(CDPATH='' cd -- "${1:-.}" && pwd -P)
cd "$BUNDLE"
[ -s SHA256SUMS ] || { echo 'Missing SHA256SUMS' >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then HASH=sha256sum
elif command -v openssl >/dev/null 2>&1; then HASH=openssl
else echo 'sha256sum or openssl required' >&2; exit 1; fi
count=0
while read -r expected relative extra; do
    [ -z "$extra" ] && [ "${#expected}" = 64 ] || exit 1
    case "$expected" in *[!0-9a-f]*) echo 'Invalid digest' >&2; exit 1;; esac
    case "$relative" in ''|/*|*..*|*[!a-zA-Z0-9_./-]*) echo 'Invalid manifest path' >&2; exit 1;; esac
    actual_path=$(readlink -f "$relative")
    case "$actual_path" in "$BUNDLE"/*) :;; *) echo 'File escapes bundle' >&2; exit 1;; esac
    [ -f "$relative" ] && [ ! -L "$relative" ] || exit 1
    if [ "$HASH" = sha256sum ]; then actual=$(sha256sum "$relative" | awk '{print $1}')
    else actual=$(openssl dgst -sha256 "$relative" | awk '{print $NF}'); fi
    [ "$actual" = "$expected" ] || { echo "Checksum mismatch: $relative" >&2; exit 1; }
    count=$((count + 1))
done < SHA256SUMS
[ "$count" -gt 0 ] || exit 1
echo "Verified $count bundle files."
