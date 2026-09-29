"""Build CN rules from pinned, attributed source data (on a computer, not a router).

This parser deliberately accepts only the syntax present in the pinned DLC
snapshot. An unsupported future input fails instead of silently changing routes.
"""
import argparse
import csv
import hashlib
import io
import json
import ipaddress
from pathlib import Path, PurePosixPath
import re
import subprocess
import tarfile
import urllib.request

from assets import HTTPSRedirect

REPO = Path(__file__).resolve().parents[1]
FIELDS = {'domain': 'domain_suffix', 'full': 'domain',
          'keyword': 'domain_keyword', 'regexp': 'domain_regex'}


def cn_ranges(text):
    networks = []
    for row in csv.reader(io.StringIO(text)):
        if len(row) != 3:
            raise ValueError('Invalid country CSV row')
        first, last, country = row
        if country != 'CN':
            continue
        start, end = ipaddress.IPv4Address(first), ipaddress.IPv4Address(last)
        if start > end:
            raise ValueError('Reversed IP range')
        networks.extend(ipaddress.summarize_address_range(start, end))
    if not networks:
        raise ValueError('No CN ranges')
    return {'ip_cidr': [str(n) for n in ipaddress.collapse_addresses(networks)]}


def domain_rules(files, selected='cn'):
    parsed = {}
    for name, text in files.items():
        rows = []
        for raw in text.splitlines():
            words = raw.split('#', 1)[0].split()
            if not words:
                continue
            kind, value = words[0].split(':', 1) if ':' in words[0] else ('domain', words[0])
            if kind not in {*FIELDS, 'include'} or not value:
                raise ValueError('Unsupported domain rule in ' + name)
            if any(not re.fullmatch(r'@[A-Za-z0-9_!.-]+', word) for word in words[1:]):
                raise ValueError('Unsupported attribute or affiliation in ' + name)
            rows.append((kind, value, frozenset(w[1:] for w in words[1:])))
        parsed[name] = rows
    cache, visiting = {}, set()

    def resolve(name):
        if name in visiting:
            raise ValueError('Cyclic domain include: ' + name)
        if name not in parsed:
            raise ValueError('Missing domain list: ' + name)
        if name in cache:
            return cache[name]
        visiting.add(name)
        rules = set()
        for kind, value, attrs in parsed[name]:
            if kind != 'include':
                rules.add((kind, value, attrs))
                continue
            for rule in resolve(value):
                if all((a[1:] not in rule[2]) if a.startswith('-') else (a in rule[2]) for a in attrs):
                    rules.add(rule)
        visiting.remove(name)
        cache[name] = rules
        return rules

    result = {field: set() for field in FIELDS.values()}
    for kind, value, _ in resolve(selected):
        result[FIELDS[kind]].add(value)
    if not any(result.values()):
        raise ValueError('Empty domain list')
    return {field: sorted(values) for field, values in sorted(result.items()) if values}


def read_domains(data):
    files = {}
    with tarfile.open(fileobj=io.BytesIO(data), mode='r:gz') as archive:
        for member in archive:
            path = PurePosixPath(member.name)
            if path.is_absolute() or '..' in path.parts or member.issym() or member.islnk():
                raise ValueError('Unsafe domain archive member')
            if len(path.parts) == 3 and path.parts[1] == 'data' and member.isfile():
                if path.name in files or member.size > 2 * 1024 * 1024:
                    raise ValueError('Duplicate or oversized domain list')
                files[path.name] = archive.extractfile(member).read().decode('utf-8')
    return files


def verified_inputs(directory, fetch=False):
    rows = json.loads((REPO/'rules.lock.json').read_text())['inputs']
    result = {}
    directory.mkdir(parents=True, exist_ok=True)
    for row in rows:
        path = directory/row['name']
        if path.is_symlink():
            raise ValueError('Symlink rule input')
        if not path.exists() and fetch:
            opener = urllib.request.build_opener(HTTPSRedirect())
            with opener.open(row['url'], timeout=180) as response:
                data = response.read(row['size'] + 1)
        else:
            data = path.read_bytes()
        if len(data) != row['size'] or hashlib.sha256(data).hexdigest() != row['sha256']:
            raise ValueError('Rule input hash/size mismatch: ' + row['name'])
        if not path.exists():
            with path.open('xb') as out:
                out.write(data)
        result[row['name']] = data
    return result


def build(inputs, output, core, fetch=False):
    data = verified_inputs(inputs, fetch)
    rules = {'geoip-cn': cn_ranges(data['dbip-country-ipv4.csv'].decode()),
             'geosite-cn': domain_rules(read_domains(data['domain-list-community.tar.gz']))}
    # Never replace an earlier rules build or a running installation.
    output.mkdir(parents=True, exist_ok=False)
    hashes = {}
    for name, rule in rules.items():
        source = output/(name+'.json')
        source.write_text(json.dumps({'version': 3, 'rules': [rule]}, ensure_ascii=False,
                                     sort_keys=True, separators=(',', ':'))+'\n', encoding='utf-8', newline='\n')
        target = output/(name+'.srs')
        subprocess.run([str(core.resolve()), 'rule-set', 'compile', str(source), '-o', str(target)], check=True)
        hashes[target.name] = {'size': target.stat().st_size, 'sha256': hashlib.sha256(target.read_bytes()).hexdigest()}
    (output/'rules-build.json').write_text(json.dumps(hashes, indent=2)+'\n', encoding='utf-8')
    return hashes


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inputs', type=Path, default=REPO/'.local/rule-inputs')
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--core', type=Path, required=True, help='Host-native sing-box 1.14.2 executable')
    parser.add_argument('--fetch', action='store_true')
    args = parser.parse_args()
    try:
        print(json.dumps(build(args.inputs, args.output, args.core, args.fetch), indent=2))
    except (OSError, ValueError, subprocess.CalledProcessError) as exc:
        parser.error(str(exc))
