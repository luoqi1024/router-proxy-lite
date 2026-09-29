"""Linux/WSL tests for stock-firmware bundle verification. No device access."""
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

VERIFY = Path(__file__).resolve().parents[1] / 'scripts/verify.sh'

class BundleVerification(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bundle = self.root / 'bundle'
        self.bundle.mkdir()
        (self.bundle / 'file.txt').write_bytes(b'routerlite fixture\n')
        digest = hashlib.sha256((self.bundle / 'file.txt').read_bytes()).hexdigest()
        self.line = digest + '  file.txt\n'
        (self.bundle / 'SHA256SUMS').write_text(self.line)

    def verify(self, fallback=False):
        env = os.environ.copy()
        if fallback:
            commands = self.root / 'commands'
            commands.mkdir(exist_ok=True)
            for name in ['awk', 'openssl', 'readlink']:
                target = commands / name
                if not target.exists(): target.symlink_to(shutil.which(name))
            env['PATH'] = str(commands)
        return subprocess.run(['/bin/sh', str(VERIFY), str(self.bundle)], env=env,
                              capture_output=True).returncode

    def test_standard_and_openssl_fallback(self):
        self.assertEqual(self.verify(), 0)
        self.assertEqual(self.verify(fallback=True), 0)

    def test_modified_file_is_rejected(self):
        (self.bundle / 'file.txt').write_text('tampered')
        self.assertNotEqual(self.verify(), 0)
        self.assertNotEqual(self.verify(fallback=True), 0)

    def test_path_traversal_and_symlink_rejected(self):
        (self.bundle / 'SHA256SUMS').write_text(self.line.replace('file.txt', '../file.txt'))
        self.assertNotEqual(self.verify(), 0)
        (self.bundle / 'SHA256SUMS').write_text(self.line)
        (self.bundle / 'file.txt').unlink()
        (self.root / 'outside').write_bytes(b'routerlite fixture\n')
        (self.bundle / 'file.txt').symlink_to(self.root / 'outside')
        self.assertNotEqual(self.verify(), 0)

if __name__ == '__main__':
    unittest.main(verbosity=2)
