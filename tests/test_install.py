"""Linux sandbox: all device paths and commands are replaced, never touch a router."""
import hashlib
import importlib.util
import json
import os
import re
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

REPO = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('packager', REPO / 'tools/package-install.py')
packager = importlib.util.module_from_spec(spec)
spec.loader.exec_module(packager)

MOCK = r'''#!/usr/bin/python3
import os,sys,pathlib,shutil
name=pathlib.Path(sys.argv[0]).name; a=sys.argv[1:]; root=pathlib.Path(os.environ['LAB'])
with (root/'commands.log').open('a') as log: log.write(name+' '+' '.join(a)+'\n')
if name=='id':print('0')
elif name=='uname':print('Linux' if a==['-s'] else os.environ.get('ARCH','armv7l'))
elif name=='ip':
 if a[:3]==['-4','addr','show']:print('    inet 192.168.31.1/24 scope global br-lan')
 elif a[:4]==['-4','route','show','default']:print('default via 192.168.1.1 dev eth4')
elif name=='iptables':sys.exit(0 if os.environ.get('CONFLICT') else 1)
elif name=='pidof':
 if a==['routerlite'] and (root/'proc/4242/exe').exists():print('4242')
 else:sys.exit(0 if os.environ.get('RUNNING') else 1)
elif name=='df':
 available=os.environ.get('DATA_FREE','20000') if a[-1].endswith('/data') else os.environ.get('OVERLAY_FREE','20000')
 print('Filesystem 1024-blocks Used Available Capacity Mounted on\nmock 30000 10000 '+available+' 34% '+a[-1])
elif name=='sleep':pass
elif name=='curl':
 if a[-1].endswith('/api/state'):print('000' if os.environ.get('UNHEALTHY') else '401',end='')
 else:
  assert '--proto' in a and a[a.index('--proto')+1]=='=https'
  assert '--proto-redir' in a and '-k' not in a
  if os.environ.get('DOWNLOAD_FAIL'):sys.exit(60)
  source=root/'release'/a[-1].rsplit('/',1)[1]
  target=pathlib.Path(a[a.index('--output')+1]);shutil.copyfile(source,target)
  if os.environ.get('CORRUPT'):target.write_bytes(b'x'*source.stat().st_size)
else:sys.exit(2)
'''


class InstallFlow(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='rpl-install-test-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        for relative in ['data', 'overlay', 'etc/init.d', 'etc/rc.d', 'lib/functions', 'proc/sys/net/ipv4', 'dev/net', 'tmp', 'commands']:
            (self.root / relative).mkdir(parents=True, exist_ok=True)
        for relative in ['etc/rc.common', 'lib/functions/procd.sh', 'dev/net/tun']:
            (self.root / relative).touch()
        (self.root / 'proc/sys/net/ipv4/ip_forward').write_text('1\n')
        (self.root / 'proc/meminfo').write_text('MemAvailable: 80000 kB\n')
        self.set_mounts()
        for name in ['id', 'uname', 'ip', 'iptables', 'pidof', 'df', 'curl', 'sleep']:
            p = self.root / 'commands' / name
            p.write_text(MOCK); p.chmod(0o755)
        self.env = dict(os.environ, LAB=str(self.root), PATH=str(self.root/'commands')+':'+os.environ['PATH'])
        self.bundle = self.root / 'bundle'
        shutil.copytree(REPO / 'scripts', self.bundle / 'scripts')
        shutil.copytree(REPO / 'licenses', self.bundle / 'licenses')
        for p in (self.bundle / 'scripts').iterdir():
            p.write_text(self.map_script(p.read_text()))
        for relative in ['bin/routerlite', 'bin/sing-box']:
            p = self.bundle / relative; p.parent.mkdir(exist_ok=True)
            p.write_text('#!/bin/sh\nexit 0\n'); p.chmod(0o755)
        for relative in ['assets/ca-certificates.crt', 'assets/geoip-cn.srs', 'assets/geosite-cn.srs', 'LICENSE', 'THIRD_PARTY.md']:
            p = self.bundle / relative; p.parent.mkdir(exist_ok=True)
            p.write_text('public test fixture\n')
        (self.bundle / 'scripts/network.sh').write_text('#!/bin/sh\nexit 0\n')
        init = '''#!/bin/sh
ROOT=$(readlink -f "$0"); ROOT=${ROOT%/scripts/routerlite.init}
echo "$1" >> "$LAB/service.log"
case "$1" in
 start) printf 'fixture-only-key' > "$ROOT/state/admin.key"; mkdir -p "$LAB/proc/4242"; ln -s "$ROOT/bin/routerlite" "$LAB/proc/4242/exe";;
 enable) ln -s "$0" "$LAB/etc/rc.d/S99routerlite";;
 enabled) test -L "$LAB/etc/rc.d/S99routerlite";;
 disable) rm -f "$LAB/etc/rc.d/S99routerlite";;
 stop) rm -f "$LAB/proc/4242/exe";;
esac
'''
        (self.bundle / 'scripts/routerlite.init').write_text(init)
        self.manifest()

    def map_script(self, text):
        pattern = r'''/(?:data|overlay|etc|proc)(?=/|[\s"';|)]|$)|/lib/functions(?=/)|/dev/net/tun|/tmp/routerlite'''
        text = re.sub(pattern, lambda match: match[0] if text[:match.start()].endswith(str(self.root)) else str(self.root) + match[0], text)
        return text.replace('[ -c ', '[ -f ')

    def set_mounts(self, overlay_type='ubifs'):
        (self.root / 'proc/mounts').write_text(f'mock {self.root}/data ubifs rw 0 0\nmock {self.root}/overlay {overlay_type} rw 0 0\n')

    def manifest(self):
        lines = []
        for p in sorted(self.bundle.rglob('*')):
            if p.is_file() and p.name != 'SHA256SUMS':
                lines.append(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+p.relative_to(self.bundle).as_posix())
        (self.bundle / 'SHA256SUMS').write_text('\n'.join(lines)+'\n')

    def run_setup(self, *args, **env):
        return subprocess.run(['sh', str(self.bundle/'scripts/setup.sh'), *args], env=dict(self.env, **env), capture_output=True, text=True)

    def bootstrap(self):
        target = self.root / 'release'
        packager.package(self.bundle, target, 'https://releases.example.invalid/v0.1-test', 'v0.1-test')
        entry = target / 'install-routerlite.sh'
        entry.write_text(self.map_script(entry.read_text()))
        return entry

    def test_compact_mode_is_explicit_and_requires_native_core(self):
        (self.root/'proc/meminfo').write_text('MemTotal: 182868 kB\nMemAvailable: 40000 kB\n')
        self.assertNotEqual(self.run_setup('--check').returncode, 0)
        result = self.run_setup('--check', '--memory-profile', 'compact')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('native-small', result.stderr)
        core = self.bundle/'bin/sing-box'
        core.write_text('#!/bin/sh\necho "sing-box version 1.14.2-routerlite-native-small"\n')
        self.manifest()
        self.assertEqual(self.run_setup('--check', '--memory-profile', 'compact').returncode, 0)
        self.assertFalse((self.root/'data/routerlite').exists())
        result = self.run_setup('--memory-profile', 'compact')
        self.assertEqual(result.returncode, 0, result.stderr+result.stdout)
        self.assertEqual((self.root/'data/routerlite/state/memory-profile').read_text().strip(), 'compact')

    def test_compact_mode_rejects_insufficient_ram(self):
        for total, available in [(182868, 24000), (65536, 40000)]:
            with self.subTest(total=total, available=available):
                (self.root/'proc/meminfo').write_text(f'MemTotal: {total} kB\nMemAvailable: {available} kB\n')
                self.assertNotEqual(self.run_setup('--memory-profile', 'compact').returncode, 0)
                self.assertFalse((self.root/'data/routerlite').exists())
                self.assertFalse((self.root/'service.log').exists())
        self.assertNotEqual(self.run_setup('--memory-profile', 'unknown').returncode, 0)

    def test_offline_install_starts_ui_and_enables_boot(self):
        result = self.run_setup()
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertIn('192.168.31.1:8787', result.stdout)
        self.assertNotIn('fixture-only-key', result.stdout)
        self.assertIn('首次打开网页请设置管理密码', result.stdout)
        self.assertTrue((self.root/'etc/rc.d/S99routerlite').is_symlink())
        self.assertFalse((self.root/'data/routerlite/state/state.json').exists())
        self.assertTrue((self.root/'data/routerlite/LICENSE').is_file())

    def test_check_selects_overlay_without_writing(self):
        result = self.run_setup('--check', DATA_FREE='1')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(str(self.root/'overlay/routerlite'), result.stdout)
        self.assertFalse((self.root/'overlay/routerlite').exists())
        self.assertFalse((self.root/'service.log').exists())

    def test_offline_sibling_stage_needs_only_reserve(self):
        target = self.root/'overlay/routerlite.stage'
        self.bundle.rename(target); self.bundle = target
        result = self.run_setup(DATA_FREE='1', OVERLAY_FREE='2048')
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertFalse(target.exists())
        self.assertTrue((self.root/'overlay/routerlite/bin/routerlite').exists())

    def test_legacy_boot_service_is_not_modified(self):
        legacy = self.root/'etc/init.d/axproxy'
        legacy.write_text('keep original')
        (self.root/'etc/rc.d/S99axproxy').symlink_to(legacy)
        result = self.run_setup()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('旧代理自启动', result.stderr)
        self.assertEqual(legacy.read_text(), 'keep original')

    def test_rejects_unsupported_environment_before_writes(self):
        for env in [dict(ARCH='aarch64'), dict(CONFLICT='1'), dict(RUNNING='1'), dict(DATA_FREE='1', OVERLAY_FREE='1')]:
            with self.subTest(env=env):
                result = self.run_setup(**env)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse((self.root/'data/routerlite').exists())
        (self.root/'proc/meminfo').write_text('MemAvailable: 24000 kB\n')
        self.assertNotEqual(self.run_setup().returncode, 0)

    def test_tmpfs_existing_install_and_staging_are_preserved(self):
        self.set_mounts('tmpfs')
        self.assertNotEqual(self.run_setup(DATA_FREE='1').returncode, 0)
        self.set_mounts()
        stage = self.root/'data/routerlite.stage'; stage.mkdir()
        (stage/'keep').write_text('mine')
        self.assertNotEqual(self.run_setup().returncode, 0)
        self.assertEqual((stage/'keep').read_text(), 'mine')
        (self.root/'overlay/routerlite').mkdir()
        self.assertNotEqual(self.run_setup().returncode, 0)

    def test_unhealthy_manager_is_stopped_and_not_enabled(self):
        result = self.run_setup(UNHEALTHY='1')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.root/'service.log').read_text().splitlines(), ['start', 'disable', 'stop'])
        self.assertFalse((self.root/'etc/init.d/routerlite').is_symlink())
        self.assertTrue((self.root/'data/routerlite/state/admin.key').exists())

    def test_online_download_install_and_hash_failure(self):
        entry = self.bootstrap()
        result = subprocess.run(['sh', str(entry)], env=dict(self.env, CORRUPT='1'), capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.root/'data/routerlite.stage').exists())
        self.assertFalse((self.root/'service.log').exists())
        result = subprocess.run(['sh', str(entry)], env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertTrue((self.root/'data/routerlite/bin/routerlite').exists())
        self.assertFalse((self.root/'data/routerlite.stage').exists())

    def test_tls_failure_cleans_only_own_downloads(self):
        entry = self.bootstrap()
        result = subprocess.run(['sh', str(entry)], env=dict(self.env, DOWNLOAD_FAIL='1'), capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('CA', result.stderr)
        self.assertFalse((self.root/'data/routerlite.stage').exists())
        self.assertFalse((self.root/'service.log').exists())

    def test_packager_rejects_unsafe_or_private_inputs(self):
        for url in ['http://example.invalid', 'https://user:pass@example.invalid', 'https://example.invalid/?token=private']:
            with self.assertRaises(ValueError):
                packager.package(self.bundle, self.root/'bad', url, 'v1')
        (self.bundle/'private.json').write_text('not a release file')
        with self.assertRaises(ValueError):
            packager.package(self.bundle, self.root/'bad', 'https://example.invalid/v1', 'v1')

    def test_offline_package_has_no_download_entry_or_private_files(self):
        import tarfile
        output = self.root/'offline'
        packager.package(self.bundle, output, None, 'v0.1-test')
        self.assertFalse((output/'install-routerlite.sh').exists())
        with tarfile.open(output/'routerlite-v0.1-test-armv7.tar.gz') as archive:
            self.assertIn('scripts/setup.sh', archive.getnames())
            self.assertTrue(all(m.isfile() and not m.name.startswith('/') and '..' not in m.name for m in archive))
        manifest = (output/'DOWNLOAD-SHA256SUMS').read_text()
        self.assertIn('  release-info.json\n', manifest)

    def test_bootstrap_check_does_not_download_or_create_stage(self):
        entry = self.bootstrap()
        result = subprocess.run(['sh', str(entry), '--check'], env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse((self.root/'data/routerlite.stage').exists())
        self.assertFalse((self.root/'service.log').exists())
        self.assertNotIn('curl ', (self.root/'commands.log').read_text())


if __name__ == '__main__':
    unittest.main(verbosity=2)
