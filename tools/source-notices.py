"""Collect exact upstream notices from a verified corresponding-source archive."""
import argparse
import hashlib
import io
import json
from pathlib import Path
import tarfile
import zipfile


def notices(source):
    sections = ['Third-party Go module notices\nGenerated from binary module identities.\n']
    with tarfile.open(source) as archive:
        manifest = json.load(archive.extractfile('SOURCE-MANIFEST.json'))
        for module in manifest['modules']:
            def esc(value):
                return ''.join('!'+c.lower() if c.isupper() else c for c in value)
            name = 'module-proxy/'+esc(module['path'])+'/@v/'+esc(module['version'])+'.zip'
            data = archive.extractfile(name).read()
            if hashlib.sha256(data).hexdigest() != module['zipSHA256']:
                raise ValueError('Module ZIP checksum mismatch')
            if not module['noticeFiles']:
                raise ValueError('No notice for '+module['path'])
            with zipfile.ZipFile(io.BytesIO(data)) as z:
                for path in module['noticeFiles']:
                    sections.append('\n'+'='*72+'\n'+module['path']+' '+module['version']+'\n'+path+'\n\n'+z.read(path).decode('utf-8'))
    # Normalize line endings/trailing spaces for Git; original bytes remain in
    # the verified module ZIPs distributed in the corresponding-source archive.
    return '\n'.join(line.rstrip() for line in '\n'.join(sections).splitlines()).rstrip()+'\n'


if __name__ == '__main__':
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('source', type=Path)
    p.add_argument('--output', type=Path, required=True)
    args = p.parse_args()
    with args.output.open('x', encoding='utf-8', newline='\n') as out:
        out.write(notices(args.source))
