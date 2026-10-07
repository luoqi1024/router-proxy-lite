"""Rebuild manager/core from an extracted source bundle using only its module proxy.

Requires an installed matching Go toolchain. Does not download a toolchain or
modules; outputs and an isolated module cache go into a fresh work directory.
"""
import argparse
import hashlib
import json
import os
import re
from pathlib import Path
import subprocess
import sys
import tarfile


def verify(source, work, go):
    source, work, go = source.resolve(), work.resolve(), go.resolve()
    manifest = json.loads((source/'SOURCE-MANIFEST.json').read_text())
    for line in (source/'SOURCE-SHA256SUMS').read_text().splitlines():
        digest, name = line.split('  ', 1)
        path = source/name
        if path.is_symlink() or not path.resolve().is_relative_to(source):
            raise ValueError('Unsafe source manifest path')
        if hashlib.sha256(path.read_bytes()).hexdigest() != digest:
            raise ValueError('Source checksum mismatch: '+name)
    expected_go = {b['build']['GoVersion'] for b in manifest['binaries']}
    installed_go = subprocess.check_output([str(go), 'version'], text=True).split()[2]
    if expected_go != {installed_go}:
        raise ValueError('Use the recorded Go toolchain: '+', '.join(sorted(expected_go)))
    work.mkdir(parents=True, exist_ok=False)
    with tarfile.open(source/'upstream/sing-box-v1.14.2.tar.gz') as t:
        t.extractall(work/'core', filter='data')
    core = work/'core/sing-box-1.14.2'
    subprocess.run([sys.executable, str(source/'routerlite/tools/core-profile.py'), str(core)], check=True)
    env = dict(os.environ, GOPROXY=(source/'module-proxy').as_uri(), GOSUMDB='off',
               GOTOOLCHAIN='local', GONOPROXY='', GOPRIVATE='', GONOSUMDB='', GOWORK='off',
               GOPATH=str(work/'gopath'), GOMODCACHE=str(work/'gopath/pkg/mod'),
               GOCACHE=str(work/'gocache'), CGO_ENABLED='0', GOOS='linux', GOARCH='arm', GOARM='7')
    result = {}
    for index, (name, directory, extra, ldflags) in enumerate([
        ('routerlite', source/'routerlite', [], '-s -w'),
        ('sing-box', core, ['-tags', 'with_utls'], '-s -w -X github.com/sagernet/sing-box/constant.Version=1.14.2-routerlite')]):
        target = work/(name+'.raw')
        entrypoint = './cmd/'+name
        if name == 'sing-box':
            build = manifest['binaries'][index]['build']
            settings = {s['Key']: s['Value'] for s in build.get('Settings', [])}
            path = build.get('Path', '')
            if path not in ('github.com/sagernet/sing-box/cmd/sing-box', 'github.com/sagernet/sing-box/cmd/routerlite-core'):
                raise ValueError('Unsupported core entrypoint')
            if path.endswith('/routerlite-core'):
                entrypoint = './cmd/routerlite-core'
                profile = 'native-small' if settings.get('-gcflags') == 'all=-l' else 'slim-cli'
                ldflags += '-'+profile
            ldflags = settings.get('-ldflags', ldflags)
            if not re.fullmatch(r'-s -w -X github.com/sagernet/sing-box/constant.Version=1\.14\.2-routerlite(?:-slim-cli|-native-small)?', ldflags):
                raise ValueError('Unsupported core version flags')
            gcflags = settings.get('-gcflags', '')
            if gcflags not in ('', 'all=-l'):
                raise ValueError('Unsupported core compiler flags')
            if gcflags:
                extra = extra + ['-gcflags='+gcflags]
        with (work/(name+'.log')).open('wb') as log:
            subprocess.run([str(go), 'build', '-buildvcs=false', '-trimpath', *extra,
                            '-ldflags='+ldflags, '-o', str(target), entrypoint],
                           cwd=directory, env=env, stdout=log, stderr=subprocess.STDOUT, check=True)
        digest = hashlib.sha256(target.read_bytes()).hexdigest()
        if digest != manifest['binaries'][index]['sha256']:
            raise ValueError('Rebuilt binary differs: '+name)
        result[name] = digest
        print(name+': offline rebuild matches', flush=True)
    (work/'verified.json').write_text(json.dumps(result, indent=2)+'\n')


if __name__ == '__main__':
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--source', type=Path, required=True, help='Extracted corresponding-source bundle')
    p.add_argument('--work', type=Path, required=True, help='New empty output path')
    p.add_argument('--go', type=Path, required=True)
    args = p.parse_args()
    try:
        verify(args.source, args.work, args.go)
    except (OSError, ValueError, subprocess.CalledProcessError) as exc:
        p.error(str(exc))
