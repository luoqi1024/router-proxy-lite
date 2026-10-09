"""Check fail-closed toolchain overlays without editing an installed toolchain."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('go_compat', ROOT/'tools/go-compat-overlay.py')
compat = importlib.util.module_from_spec(spec)
spec.loader.exec_module(compat)


class GoCompatibility(unittest.TestCase):
    def test_unreviewed_toolchain_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root/'go/src/crypto/internal/fips140/drbg/entropy_fips140.go'
            source.parent.mkdir(parents=True)
            source.write_text('an unknown Go source revision')
            with self.assertRaises(ValueError):
                compat.create_overlay(root/'go', root/'out')
            self.assertEqual(source.read_text(), 'an unknown Go source revision')
            self.assertFalse((root/'out/overlay.json').exists())

    def test_reviewed_overlay_keeps_original_untouched(self):
        goroot = ROOT/'.local/tools/go'
        source = goroot/'src/crypto/internal/fips140/drbg/entropy_fips140.go'
        if not source.exists():
            self.skipTest('pinned Go source unavailable')
        original = source.read_bytes()
        with tempfile.TemporaryDirectory() as directory:
            overlay = compat.create_overlay(goroot, directory)
            mapping = json.loads(overlay.read_text())['Replace']
            self.assertEqual(source.read_bytes(), original)
            self.assertEqual(set(mapping), {str(source.resolve())})
            # Exercise idempotent generation while preserving the toolchain.
            first = Path(mapping[str(source.resolve())]).read_bytes()
            compat.create_overlay(goroot, directory)
            self.assertEqual(Path(mapping[str(source.resolve())]).read_bytes(), first)
            self.assertIn(b'Copyright 2026 The Go Authors.', first)


if __name__ == '__main__':
    unittest.main(verbosity=2)
