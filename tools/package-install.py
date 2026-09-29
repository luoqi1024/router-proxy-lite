"""Create pinned, flat HTTPS release assets; never publish or change a device."""
import argparse
import hashlib
import json
import io
import tarfile
from pathlib import Path
import re
import shlex
from urllib.parse import urlsplit

REPO = Path(__file__).resolve().parents[1]
ALLOWED = re.compile(r'(bin/(routerlite|sing-box)|assets/(ca-certificates\.crt|geoip-cn\.srs|geosite-cn\.srs)|scripts/[a-z-]+\.(sh|init)|licenses/[a-z0-9.-]+\.txt|LICENSE|THIRD_PARTY\.md)')
REQUIRED = {'bin/routerlite', 'bin/sing-box', 'scripts/install.sh', 'scripts/setup.sh',
            'scripts/setup-common.sh', 'scripts/verify.sh', 'scripts/manage.sh',
            'scripts/run.sh', 'scripts/network.sh', 'scripts/routerlite.init',
            'assets/ca-certificates.crt', 'assets/geoip-cn.srs', 'assets/geosite-cn.srs',
            'LICENSE', 'THIRD_PARTY.md', 'licenses/sing-box.txt', 'licenses/mozilla-mpl-2.0.txt',
            'licenses/go-runtime.txt', 'licenses/yaml-v3.txt', 'licenses/upx.txt',
            'licenses/sing-rule-generators.txt', 'licenses/domain-list-community.txt'}


def package(bundle: Path, output: Path, base_url: str | None, version: str):
    if base_url is not None:
        u = urlsplit(base_url)
        if (u.scheme != 'https' or not u.hostname or u.username or u.password or
                u.query or u.fragment or any(c.isspace() or ord(c) < 32 for c in base_url)):
            raise ValueError('Release URL must be HTTPS, with no credentials, query or fragment')
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._-]{0,63}', version):
        raise ValueError('Invalid release version')
    if output.exists():
        raise ValueError('Output already exists; will not overwrite a release')
    bundle = bundle.resolve()
    manifest = bundle / 'SHA256SUMS'
    if manifest.is_symlink() or not manifest.is_file():
        raise ValueError('Missing regular SHA256SUMS')
    files = {}
    for line in manifest.read_text(encoding='utf-8').splitlines():
        match = re.fullmatch(r'([0-9a-f]{64})  (.+)', line)
        if not match:
            raise ValueError('Invalid manifest line')
        digest, relative = match.groups()
        if not ALLOWED.fullmatch(relative) or relative in files:
            raise ValueError('Unexpected or duplicate bundle path: ' + relative)
        path = bundle / relative
        if (path.is_symlink() or any(p.is_symlink() for p in path.parents if p != bundle) or
                not path.is_file() or not path.resolve().is_relative_to(bundle)):
            raise ValueError('Unsafe bundle path: ' + relative)
        data = path.read_bytes()
        if hashlib.sha256(data).hexdigest() != digest:
            raise ValueError('Bundle checksum mismatch: ' + relative)
        files[relative] = data
    if not REQUIRED.issubset(files):
        raise ValueError('Required bundle files missing')
    # Avoid silently distributing extra private files left in a build directory.
    actual = {p.relative_to(bundle).as_posix() for p in bundle.rglob('*') if p.is_file() or p.is_symlink()}
    if actual != set(files) | {'SHA256SUMS'}:
        raise ValueError('Bundle contains unlisted files')
    files['SHA256SUMS'] = manifest.read_bytes()
    rows = []
    for relative, data in sorted(files.items()):
        asset = relative.replace('/', '--')
        rows.append(f'{hashlib.sha256(data).hexdigest()} {len(data)} {relative} {asset}')
    # Round every file to a 4 KiB block, allow directory/manifest overhead, reserve 2 MiB.
    need = sum((len(data) + 4095) // 4096 * 4 for data in files.values()) + 64 + 2048
    common = files['scripts/setup-common.sh'].decode('utf-8')
    body = (REPO / 'tools/bootstrap-body.sh').read_text(encoding='utf-8').replace('@@FILES@@', '\n'.join(rows))
    output.mkdir(parents=True)
    if base_url is not None:
        entry = '#!/bin/sh\nset -eu\n' + f'BASE_URL={shlex.quote(base_url.rstrip("/"))}\nRELEASE={shlex.quote(version)}\nNEED_KIB={need}\n' + common + '\n' + body
        for relative, data in files.items():
            (output / relative.replace('/', '--')).write_bytes(data)
        (output / 'install-routerlite.sh').write_text(entry, encoding='utf-8', newline='\n')
    archive = output / f'routerlite-{version}-armv7.tar.gz'
    with tarfile.open(archive, 'w:gz') as tar:
        for relative, data in sorted(files.items()):
            info = tarfile.TarInfo(relative)
            info.size = len(data)
            info.mode = 0o755 if relative.startswith(('bin/', 'scripts/')) else 0o644
            tar.addfile(info, io.BytesIO(data))
    (output / 'DOWNLOAD-SHA256SUMS').write_text('\n'.join(
        hashlib.sha256(p.read_bytes()).hexdigest() + '  ' + p.name
        for p in sorted(output.iterdir()) if p.is_file()) + '\n', encoding='utf-8')
    (output / 'release-info.json').write_text(json.dumps({
        'version': version, 'architecture': 'armv7', 'requiredFreeKiB': need,
        'payloadBytes': sum(map(len, files.values())), 'files': len(files),
        'status': 'development; not published; hardware acceptance still required',
    }, indent=2) + '\n', encoding='utf-8')
    return need


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--bundle', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--base-url', help='Final version-specific HTTPS asset directory; omit for offline package only')
    parser.add_argument('--version', required=True)
    args = parser.parse_args()
    try:
        need = package(args.bundle, args.output, args.base_url, args.version)
    except (ValueError, OSError) as exc:
        parser.error(str(exc))
    print(f'Prepared ARMv7 install assets in {args.output}; requires {need} KiB free. Nothing published.')
