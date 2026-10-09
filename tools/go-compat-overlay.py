"""Lazy FIPS entropy scratch allocation for low-memory Linux exec accounting.

Go 1.27.1's unused 32 MiB BSS buffer can prevent exec before main. This
overlay preserves the full buffer and entropy algorithm, allocating on first
FIPS use. It never edits GOROOT. See golang/go#81505; no FIPS certification is
claimed for binaries made with this local compatibility overlay.
"""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess

SOURCE_SHA256 = 'bd3c834a29e31c93e56d81da54d4a874561814a86550de1456c8d6d40a9c5fda'


def patch_source(original):
    if hashlib.sha256(original).hexdigest() != SOURCE_SHA256:
        raise ValueError('Unreviewed Go entropy source; use the documented Go 1.27.1 toolchain')
    text = original.decode()
    text = text.replace('import entropy "crypto/internal/entropy/v1.0.0"',
                        'import (\n entropy "crypto/internal/entropy/v1.0.0"\n "sync"\n)')
    text = text.replace('var memory entropy.ScratchBuffer',
                        '// RouterLite: preserve size/algorithm, avoid unused 32 MiB BSS at exec.\n'
                        'var scratchMemory = sync.OnceValue(func() *entropy.ScratchBuffer {\n'
                        ' return new(entropy.ScratchBuffer)\n})')
    text = text.replace('func getEntropy() *[SeedSize]byte {',
                        'func getEntropy() *[SeedSize]byte {\n memory := scratchMemory()')
    text = text.replace('entropy.Seed(&memory)', 'entropy.Seed(memory)')
    if text == original.decode() or 'var memory entropy.ScratchBuffer' in text:
        raise ValueError('Compatibility transformation failed')
    return text


def create_overlay(goroot, output):
    source = Path(goroot) / 'src/crypto/internal/fips140/drbg/entropy_fips140.go'
    output = Path(output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    replace = {}
    if source.exists():
        text = patch_source(source.read_bytes())
        target = output / 'entropy_fips140.go'
        target.write_text(text, encoding='utf-8', newline='\n')
        replace[str(source.resolve())] = str(target)
    path = output / 'overlay.json'
    path.write_text(json.dumps({'Replace': replace}, indent=2), encoding='utf-8')
    return path


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--go', required=True)
    parser.add_argument('--output', required=True)
    args = parser.parse_args()
    goroot = subprocess.check_output([args.go, 'env', 'GOROOT'], text=True).strip()
    print(create_overlay(goroot, args.output))
