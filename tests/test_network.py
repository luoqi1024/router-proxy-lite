"""Run in Linux/WSL: python3 tests/test_network.py.

Exercises the real shell transaction with fake ip/iptables/id/cat commands.
Only the /dev/net/tun character-device probe and lock path are substituted.
No real networking commands or router connections are used.
"""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

MOCK = r'''#!/usr/bin/python3
import json, os, pathlib, sys
p=pathlib.Path(os.environ['MOCK_STATE'])
s=json.loads(p.read_text())
name=pathlib.Path(sys.argv[0]).name; a=sys.argv[1:]
line=name+' '+' '.join(a)
s['calls'].append(line)
def finish(code=0, output=''):
    p.write_text(json.dumps(s));print(output,end='');sys.exit(code)
if os.environ.get('MOCK_FAIL')==line:finish(1)
if name=='id':finish(0,'0\n')
if name=='cat':
    if a==['/proc/sys/net/ipv4/ip_forward']:finish(0,'1\n')
    finish(0,pathlib.Path(a[0]).read_text())
if name=='ip':
    if a[:3]==['-6','addr','show'] or a[:2]==['link','show']:finish()
    if a[:3]==['-4','rule','show']:finish(0,'10820: from all fwmark 0x40000000 iif br-lan lookup 3180\n' if s['rule'] else '')
    if a[:3]==['-4','route','show']:finish(0,'\n'.join(s['routes']))
    if a[:3]==['-4','route','add']:s['routes'].append(' '.join(a[3:]));finish()
    if a[:3]==['-4','route','flush']:s['routes']=[];finish()
    if a[:3]==['-4','rule','add']:s['rule']=True;finish()
    if a[:3]==['-4','rule','del']:s['rule']=False;finish()
    finish(2)
if name=='iptables':
    table='filter'
    if a[:1]==['-t']:table=a[1];a=a[2:]
    op,chain=a[:2];key=table+'/'+chain
    if op=='-nL':finish(0 if key in s['chains'] else 1)
    if op=='-N':
        if key in s['chains']:finish(1)
        s['chains'].append(key);finish()
    if op=='-F':s['rules']=[r for r in s['rules'] if not r.startswith(key+' ')];finish()
    if op=='-X':
        if key in s['chains']:s['chains'].remove(key)
        finish()
    rest=a[2:]
    if op=='-I' and rest and rest[0].isdigit():rest=rest[1:]
    rule=key+' '+' '.join(rest)
    if op=='-C':finish(0 if rule in s['rules'] else 1)
    if op in ['-I','-A']:s['rules'].append(rule);finish()
    if op=='-D':
        if rule not in s['rules']:finish(1)
        s['rules'].remove(rule);finish()
    finish(2)
finish(2)
'''


class NetworkTransactions(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='routerlite-network-test-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.data = self.root / 'data'
        self.state = self.root / 'mock.json'
        self.initial = dict(chains=[], rules=[], routes=[], rule=False, calls=[])
        self.state.write_text(json.dumps(self.initial))
        for name in ['ip', 'iptables', 'id', 'cat']:
            path = self.root / name
            path.write_text(MOCK)
            path.chmod(0o755)
        script = (Path(__file__).resolve().parents[1] / 'scripts/network.sh').read_text()
        script = script.replace('LOCK=/tmp/routerlite-network.lock', f'LOCK={self.root}/lock')
        script = script.replace('[ -c /dev/net/tun ]', '[ -d / ]')
        self.script = self.root / 'network.sh'
        self.script.write_text(script)
        self.env = dict(os.environ, PATH=str(self.root)+':'+os.environ['PATH'],
                        RPL_DATA=str(self.data), RPL_LAN='br-lan', MOCK_STATE=str(self.state))

    def run_script(self, action, fail=''):
        result = subprocess.run(['/bin/sh', str(self.script), action],
                                env=dict(self.env, MOCK_FAIL=fail), capture_output=True, text=True)
        return result.returncode

    def assert_clean(self):
        state = json.loads(self.state.read_text())
        for key in ['chains', 'rules', 'routes']:
            self.assertEqual(state[key], [], (key, state))
        self.assertFalse(state['rule'])
        self.assertFalse((self.data / 'network.owner').exists())

    def test_start_status_stop_and_idempotent_stop(self):
        self.assertEqual(self.run_script('start'), 0)
        self.assertEqual(self.run_script('status'), 0)
        self.assertEqual(self.run_script('stop'), 0)
        self.assertEqual(self.run_script('stop'), 0)
        self.assert_clean()

    def test_partial_failure_rolls_back(self):
        self.assertNotEqual(self.run_script('start', 'iptables -t nat -I PREROUTING 1 -i br-lan -j RPL_DNS'), 0)
        self.assert_clean()

    def test_foreign_table_is_not_modified(self):
        state = dict(self.initial, routes=['default dev foreign'])
        self.state.write_text(json.dumps(state))
        self.assertNotEqual(self.run_script('start'), 0)
        self.assertEqual(self.run_script('stop'), 0)
        self.assertEqual(json.loads(self.state.read_text())['routes'], ['default dev foreign'])

    def test_cleanup_failure_retains_ownership_for_retry(self):
        self.assertEqual(self.run_script('start'), 0)
        self.assertNotEqual(self.run_script('stop', 'ip -4 route flush table 3180'), 0)
        self.assertTrue((self.data / 'network.owner').exists())
        self.assertEqual(self.run_script('stop'), 0)
        self.assert_clean()


if __name__ == '__main__':
    unittest.main(verbosity=2)
