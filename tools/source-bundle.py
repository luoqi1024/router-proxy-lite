"""Prepare a corresponding-source candidate from exact binary module identities.

No publication or compliance certification. Archive contains no router state.
"""
import argparse
import base64
import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import subprocess
import tarfile
import zipfile
import importlib.util

REPO = Path(__file__).resolve().parents[1]
CORE_SHA256 = '67dd8f8c37ecaaadcfcafad1f0827eed4b034c963b86fd3aa5c0d7a36876845d'


def hash_module(path):
    digest = hashlib.sha256()
    with zipfile.ZipFile(path) as archive:
        names = sorted(n for n in archive.namelist() if not n.endswith('/'))
        if len(names) != len(set(names)):
            raise ValueError('Duplicate module ZIP entry')
        for name in names:
            p = PurePosixPath(name)
            if p.is_absolute() or '..' in p.parts or '\n' in name or '\\' in name:
                raise ValueError('Unsafe module ZIP path')
            digest.update((hashlib.sha256(archive.read(name)).hexdigest()+'  '+name+'\n').encode())
    return 'h1:'+base64.b64encode(digest.digest()).decode()


def escaped(value):
    return ''.join('!'+c.lower() if c.isupper() else c for c in value)


def build(go, binaries, caches, core_archive, output, rule_inputs=None):
    if output.exists():
        raise ValueError('Output already exists')
    if hashlib.sha256(core_archive.read_bytes()).hexdigest() != CORE_SHA256:
        raise ValueError('Core source archive differs from pinned version')
    build_info = [json.loads(subprocess.check_output([str(go), 'version', '-m', '-json', str(p)])) for p in binaries]
    deps = {}
    for info in build_info:
        for module in info.get('Deps', []):
            if module.get('Replace'):
                raise ValueError('Replacement modules require explicit source review')
            key = (module['Path'], module['Version'])
            if key in deps and deps[key]['Sum'] != module['Sum']:
                raise ValueError('Conflicting module checksums')
            deps[key] = module
    payloads = {}
    inventory = []
    for (name, version), module in sorted(deps.items()):
        prefix = escaped(name) + '/@v/' + escaped(version)
        found = next((cache/'cache/download'/prefix for cache in caches
                      if Path(str(cache/'cache/download'/prefix)+'.zip').is_file()), None)
        if found is None:
            raise ValueError('Missing source module in supplied cache: '+name+'@'+version)
        archive = Path(str(found)+'.zip')
        if hash_module(archive) != module['Sum']:
            raise ValueError('Module source does not match binary: '+name)
        for suffix in ['.zip', '.mod', '.info']:
            path = Path(str(found)+suffix)
            if not path.is_file():
                raise ValueError('Missing module metadata: '+name+suffix)
            payloads['module-proxy/'+prefix+suffix] = path
        with zipfile.ZipFile(archive) as source:
            notices = [n for n in source.namelist() if PurePosixPath(n).name.lower().startswith(('license', 'copying', 'notice'))]
        inventory.append({'path': name, 'version': version, 'sum': module['Sum'],
                          'zipSHA256': hashlib.sha256(archive.read_bytes()).hexdigest(), 'noticeFiles': notices})
    tracked = subprocess.check_output(['git', 'ls-files', '-z'], cwd=REPO).decode().split('\0')
    for name in filter(None, tracked):
        path = REPO / name
        if path.is_symlink() or not path.is_file() or not path.resolve().is_relative_to(REPO):
            raise ValueError('Unsafe tracked source path: '+name)
        if name.startswith(('.local/', 'dist/', 'output/')) or '.private.' in name:
            raise ValueError('Private path must not be tracked: '+name)
        payloads['routerlite/'+name] = path
    payloads['upstream/sing-box-v1.14.2.tar.gz'] = core_archive
    if rule_inputs is not None:
        spec = importlib.util.spec_from_file_location('build_rules', REPO/'tools/build-rules.py')
        rules = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(rules)
        for name in rules.verified_inputs(rule_inputs):
            payloads['rule-inputs/'+name] = rule_inputs/name
    manifest = {
        'status': 'source candidate; complete offline rebuild and release review pending',
        'repositoryCommit': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=REPO).decode().strip(),
        'workingTreeDirty': bool(subprocess.check_output(['git', 'status', '--porcelain'], cwd=REPO)),
        'binaries': [{'name': p.name, 'sha256': hashlib.sha256(p.read_bytes()).hexdigest(), 'build': info} for p, info in zip(binaries, build_info)],
        'modules': inventory,
        'coreArchiveSHA256': CORE_SHA256,
    }
    output.parent.mkdir(parents=True, exist_ok=True)
    # Exclusive creation; failed archives are left for inspection, never overwritten.
    with output.open('xb') as raw, tarfile.open(fileobj=raw, mode='w:gz', compresslevel=1) as tar:
        hashes = []
        for name, path in sorted(payloads.items()):
            data = path.read_bytes()
            entry = tarfile.TarInfo(name); entry.size = len(data); entry.mode = 0o644
            tar.addfile(entry, io.BytesIO(data))
            hashes.append(hashlib.sha256(data).hexdigest()+'  '+name)
        for name, data in [('SOURCE-MANIFEST.json', (json.dumps(manifest, indent=2)+'\n').encode()),
                           ('SOURCE-SHA256SUMS', ('\n'.join(hashes)+'\n').encode())]:
            entry = tarfile.TarInfo(name); entry.size = len(data); entry.mode = 0o644
            tar.addfile(entry, io.BytesIO(data))
    return len(inventory)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--go', type=Path, default=REPO/'.local/tools/go/bin/go.exe')
    parser.add_argument('--manager', type=Path, default=REPO/'dist/routerlite-armv7.raw')
    parser.add_argument('--core', type=Path, default=REPO/'dist/routerlite-core-armv7.raw')
    parser.add_argument('--module-cache', type=Path, action='append', required=True)
    parser.add_argument('--core-source', type=Path, default=REPO/'.local/tools/sing-box-source.tar.gz')
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--rule-inputs', type=Path, help='Verified pinned data inputs to include for rebuilding rules')
    args = parser.parse_args()
    try:
        count = build(args.go, [args.manager, args.core], args.module_cache, args.core_source, args.output, args.rule_inputs)
    except (ValueError, OSError, subprocess.CalledProcessError) as exc:
        parser.error(str(exc))
    print(f'Packed {count} verified module sources into {args.output}; not published or release-certified.')
