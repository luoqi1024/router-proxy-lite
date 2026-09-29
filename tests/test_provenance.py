"""Resource integrity and module-source hash tests; no router or network access."""
import base64
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
import zipfile

REPO = Path(__file__).resolve().parents[1]


def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, REPO/'tools'/filename)
    mod = importlib.util.module_from_spec(spec); spec.loader.exec_module(mod)
    return mod


assets = module('assets', 'assets.py')
sources = module('sources', 'source-bundle.py')


class Response(io.BytesIO):
    def geturl(self):
        return 'https://public.example/resource'


class Opener:
    def __init__(self, body): self.body = body
    def open(self, request, timeout): return Response(self.body)


class Provenance(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(); self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name); self.lock = self.root/'lock.json'
        self.rows = [{'name': name, 'url': 'https://public.example/'+name, 'size': 3,
                      'sha256': hashlib.sha256(b'abc').hexdigest()} for name in sorted(assets.NAMES)]
        self.save()

    def save(self): self.lock.write_text(json.dumps({'schema': 1, 'assets': self.rows}))

    def test_fetch_and_offline_verify(self):
        output = self.root/'assets'
        self.assertEqual(assets.prepare(self.lock, output, True, Opener(b'abc')), 3)
        self.assertEqual(assets.prepare(self.lock, output), 3)
        self.assertEqual(len(list(output.iterdir())), 3)

    def test_corrupt_or_oversize_download_not_installed(self):
        for body in [b'xyz', b'abcd', b'a']:
            with self.assertRaises(ValueError):
                assets.prepare(self.lock, self.root/'assets', True, Opener(body))
            self.assertEqual(list((self.root/'assets').iterdir()), [])

    def test_stale_files_are_preserved(self):
        output = self.root/'assets'; output.mkdir()
        stale = output/self.rows[0]['name']; stale.write_bytes(b'old')
        with self.assertRaises(ValueError): assets.prepare(self.lock, output, True, Opener(b'abc'))
        self.assertEqual(stale.read_bytes(), b'old')

    def test_reject_http_and_credentials(self):
        for url in ['http://example.com/a', 'https://user:secret@example.com/a']:
            self.rows[0]['url'] = url; self.save()
            with self.assertRaises(ValueError): assets.load_lock(self.lock)

    def test_reject_traversal_and_bad_size(self):
        self.rows[0]['name'] = '../file'; self.save()
        with self.assertRaises(ValueError): assets.load_lock(self.lock)
        self.rows[0]['name'] = sorted(assets.NAMES)[0]
        self.rows[0]['size'] = 10**12; self.save()
        with self.assertRaises(ValueError): assets.load_lock(self.lock)

    def test_module_hash_and_tampering(self):
        archive = self.root/'module.zip'; name = 'example.org/demo@v1.0.0/main.go'
        with zipfile.ZipFile(archive, 'w') as z: z.writestr(name, b'package demo')
        line = hashlib.sha256(b'package demo').hexdigest()+'  '+name+'\n'
        expected = 'h1:'+base64.b64encode(hashlib.sha256(line.encode()).digest()).decode()
        self.assertEqual(sources.hash_module(archive), expected)
        with zipfile.ZipFile(archive, 'w') as z: z.writestr(name, b'changed')
        self.assertNotEqual(sources.hash_module(archive), expected)

    def test_unsafe_module_zip_rejected(self):
        archive = self.root/'module.zip'
        with zipfile.ZipFile(archive, 'w') as z: z.writestr('../escape', b'x')
        with self.assertRaises(ValueError): sources.hash_module(archive)


if __name__ == '__main__': unittest.main(verbosity=2)
