"""Fetch or verify only the public resources pinned in assets.lock.json."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import tempfile
import urllib.request
from urllib.parse import urlsplit

REPO = Path(__file__).resolve().parents[1]
NAMES = {'ca-certificates.crt', 'geoip-cn.srs', 'geosite-cn.srs'}


def https_url(url):
    u = urlsplit(url)
    if u.scheme != 'https' or not u.hostname or u.username or u.password or u.fragment:
        raise ValueError('Resource URL must use HTTPS without credentials or fragments')


class HTTPSRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        https_url(newurl)
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def load_lock(path):
    lock = json.loads(Path(path).read_text(encoding='utf-8'))
    rows = lock.get('assets', [])
    if lock.get('schema') != 1 or len(rows) != 3 or {r.get('name') for r in rows} != NAMES:
        raise ValueError('Expected exactly three named resources')
    for row in rows:
        https_url(row['url'])
        if not re.fullmatch('[0-9a-f]{64}', row['sha256']):
            raise ValueError('Invalid SHA256')
        if type(row['size']) is not int or not 0 < row['size'] <= 2 * 1024 * 1024:
            raise ValueError('Resource size is invalid')
    return rows


def matches(path, row):
    if path.is_symlink() or not path.is_file() or path.stat().st_size != row['size']:
        return False
    return hashlib.sha256(path.read_bytes()).hexdigest() == row['sha256']


def prepare(lock, directory, fetch=False, opener=None):
    rows = load_lock(lock)
    directory = Path(directory)
    if directory.is_symlink():
        raise ValueError('Resource directory must not be a symlink')
    if fetch:
        directory.mkdir(parents=True, exist_ok=True)
    opener = opener or urllib.request.build_opener(HTTPSRedirect())
    # Reject stale files before downloading any replacements; preserve old builds.
    for row in rows:
        target = directory / row['name']
        if target.exists() or target.is_symlink():
            if not matches(target, row):
                raise ValueError('Existing file differs from lock; use a fresh directory: ' + row['name'])
        elif not fetch:
            raise ValueError('Missing resource: ' + row['name'])
    for row in rows:
        target = directory / row['name']
        if matches(target, row):
            continue
        request = urllib.request.Request(row['url'], headers={'User-Agent': 'RouterLite-resource-fetch'})
        with opener.open(request, timeout=60) as response:
            https_url(response.geturl())
            data = response.read(row['size'] + 1)
        if len(data) != row['size'] or hashlib.sha256(data).hexdigest() != row['sha256']:
            raise ValueError('Downloaded file fails pinned size/hash: ' + row['name'])
        fd, tmp = tempfile.mkstemp(prefix='resource-', suffix='.part', dir=directory)
        try:
            with os.fdopen(fd, 'wb') as out:
                out.write(data)
            # Exclusive destination creation avoids overwriting another fetcher's work.
            os.link(tmp, target)
        finally:
            Path(tmp).unlink(missing_ok=True)
    return len(rows)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['fetch', 'verify'])
    parser.add_argument('--lock', type=Path, default=REPO/'assets.lock.json')
    parser.add_argument('--output', type=Path, default=REPO/'.local/assets-pinned')
    args = parser.parse_args()
    try:
        count = prepare(args.lock, args.output, args.action == 'fetch')
    except (ValueError, OSError) as exc:
        parser.error(str(exc))
    print(f'Verified {count} pinned public resources in {args.output}')
